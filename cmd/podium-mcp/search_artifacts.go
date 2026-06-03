package main

import (
	"context"
	"encoding/json"
	"sort"
)

// searchArtifacts fans the §5 search_artifacts call out to the
// registry AND the §6.4.1 local index over workspace-overlay
// records, fusing the two ranked lists via RRF before returning
// to the host.
func (s *mcpServer) searchArtifacts(args map[string]any) any {
	// §7.4 offline-only: "never contact the registry; structured error if
	// cache miss." The workspace overlay is the only local source a search can
	// serve, so an offline-only search returns overlay matches without dialing
	// the registry, and an empty overlay is a structured cache miss (F-7.4.2).
	if s.cfg.cacheMode == "offline-only" {
		return s.offlineOnlySearchArtifacts(args)
	}
	body, err := s.fetchJSON("/v1/search_artifacts", args)
	if err != nil {
		// spec: §7.4 / §12 — when the registry is unreachable, return the
		// offline envelope rather than an error. always-revalidate surfaces an
		// explicit "offline" status the host can present; offline-first serves
		// silently with no status field (F-7.4.4). The workspace overlay is
		// reachable without the registry, so a fresh search still returns the
		// local matches either way.
		if isRegistryUnreachable(err) {
			return s.offlineSearchArtifacts(args)
		}
		return errorResultFrom(err)
	}
	var registry struct {
		Query        string           `json:"query"`
		TotalMatched int              `json:"total_matched"`
		Results      []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(body, &registry); err != nil {
		return errorResult("decode search_artifacts: " + err.Error())
	}
	// spec: §3.2 / §5 — the MCP meta-tool surface returns lean descriptors and
	// the agent loads full content via load_artifact. The registry carries the
	// artifact frontmatter on the wire for the §7.6.1 read-CLI/SDK schema, so
	// the bridge drops it before handing results to the agent (and keeps
	// registry hits uniform with overlay descriptors, which carry none).
	overlayRecords := s.overlaySnapshot()
	if len(overlayRecords) == 0 {
		// No overlay: pass the registry response through (frontmatter stripped)
		// so single-deployment behavior is otherwise unchanged.
		decoded := jsonAny(body)
		stripSearchResultFrontmatter(decoded)
		return decoded
	}
	for _, r := range registry.Results {
		delete(r, "frontmatter")
	}

	query, _ := args["query"].(string)
	typeFilter, _ := args["type"].(string)
	scope, _ := args["scope"].(string)
	tags := tagsArg(args)
	topK := topKArg(args)
	local := localSearch(overlayRecords, query, typeFilter, scope, tags, topK)

	// §9.1 LocalSearchProvider: when an overlay semantic backend is
	// configured, contribute a vector-ranked stream fused alongside the
	// BM25 local stream and the registry stream. Disabled by default;
	// nil index and any backend error degrade to BM25-only.
	var semantic []localSearchResult
	if s.localSem != nil {
		ctx, cancel := context.WithTimeout(context.Background(), localSemanticTimeout)
		semantic = s.localSem.search(ctx, overlayRecords, query, typeFilter, scope, tags, topK)
		cancel()
	}

	fused := rrfFuse(registry.Results, topK, local, semantic)
	return map[string]any{
		"query":         registry.Query,
		"total_matched": fusedTotalMatched(registry.TotalMatched, registry.Results, local, semantic),
		"results":       fused,
	}
}

// fusedTotalMatched defines total_matched for the §6.4.1 fused response as
// the count of distinct artifacts matched across both streams. The registry's
// own pre-truncation total is the authoritative count for its stream; an
// overlay artifact the registry already returned must not be counted again
// (it is one artifact, and rrfFuse merges it into a single descriptor), so
// only overlay artifacts absent from the registry's returned results add to
// the total. This avoids both the double-count of overlapping hits and the
// summing of a pre-truncation registry total with a post-truncation local
// count. F-6.4.4.
func fusedTotalMatched(registryTotal int, registryResults []map[string]any, locals ...[]localSearchResult) int {
	seen := make(map[string]bool, len(registryResults))
	for _, r := range registryResults {
		if id, _ := r["id"].(string); id != "" {
			seen[id] = true
		}
	}
	overlayOnly := 0
	for _, local := range locals {
		for _, r := range local {
			if r.ID == "" || seen[r.ID] {
				continue
			}
			seen[r.ID] = true
			overlayOnly++
		}
	}
	return registryTotal + overlayOnly
}

// stripSearchResultFrontmatter removes the "frontmatter" key from each search
// descriptor in a jsonAny-decoded /v1/search_artifacts response. spec: §3.2/§5
// — the MCP meta-tool surface is lean; the frontmatter rides the §7.6.1
// read-CLI/SDK schema only.
func stripSearchResultFrontmatter(decoded any) {
	m, ok := decoded.(map[string]any)
	if !ok {
		return
	}
	results, ok := m["results"].([]any)
	if !ok {
		return
	}
	for _, r := range results {
		if rm, ok := r.(map[string]any); ok {
			delete(rm, "frontmatter")
		}
	}
}

// offlineSearchArtifacts builds the §12 offline result for search_artifacts
// when the registry is unreachable. The workspace overlay (and its optional
// §9.1 semantic index) is local, so any overlay matches are still returned.
// Per §7.4, always-revalidate carries an explicit "offline" status; offline-first
// serves silently with no status field (offlineEnvelope honors the mode). F-7.4.4.
func (s *mcpServer) offlineSearchArtifacts(args map[string]any) any {
	query, _ := args["query"].(string)
	overlayRecords := s.overlaySnapshot()
	if len(overlayRecords) == 0 {
		return s.offlineEnvelope(map[string]any{
			"query":         query,
			"total_matched": 0,
			"results":       []any{},
		})
	}
	typeFilter, _ := args["type"].(string)
	scope, _ := args["scope"].(string)
	tags := tagsArg(args)
	topK := topKArg(args)
	local := localSearch(overlayRecords, query, typeFilter, scope, tags, topK)
	var semantic []localSearchResult
	if s.localSem != nil {
		ctx, cancel := context.WithTimeout(context.Background(), localSemanticTimeout)
		semantic = s.localSem.search(ctx, overlayRecords, query, typeFilter, scope, tags, topK)
		cancel()
	}
	fused := rrfFuse(nil, topK, local, semantic)
	return s.offlineEnvelope(map[string]any{
		"query": query,
		// No registry stream offline; the total is the count of distinct
		// overlay artifacts matched across the local and semantic streams.
		"total_matched": fusedTotalMatched(0, nil, local, semantic),
		"results":       fused,
	})
}

// offlineOnlySearchArtifacts serves the §7.4 offline-only contract for
// search_artifacts: the registry is never contacted, so only workspace-overlay
// matches are returned. With no local match the call is a structured cache miss
// (network.offline_cache_miss) rather than an empty offline envelope, because
// offline-only forbids reaching the registry that would otherwise fill the gap
// (F-7.4.2).
func (s *mcpServer) offlineOnlySearchArtifacts(args map[string]any) any {
	res := s.offlineSearchArtifacts(args)
	if m, ok := res.(map[string]any); ok {
		if n, _ := m["total_matched"].(int); n > 0 {
			return res
		}
	}
	return errorResult(errOfflineCacheMiss.Error())
}

// tagsArg pulls the optional tags arg in either []any or
// comma-string form.
func tagsArg(args map[string]any) []string {
	switch v := args["tags"].(type) {
	case string:
		return splitCSVMCP(v)
	case []any:
		out := []string{}
		for _, t := range v {
			if s, ok := t.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func topKArg(args map[string]any) int {
	switch v := args["top_k"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 10
}

// rrfFuse blends the registry's ordered results with one or more local
// search streams (BM25 over the overlay, and the §9.1 LocalSearchProvider
// semantic stream when configured) via reciprocal rank fusion (k=60, the
// Cormack/Clarke default mirrored from the registry's vector path). Items
// appearing in more than one stream sum their reciprocal ranks so a hit
// ranked highly by any backend rises. Empty local streams are ignored.
func rrfFuse(registry []map[string]any, topK int, locals ...[]localSearchResult) []map[string]any {
	if topK <= 0 {
		topK = 10
	}
	const k = 60.0
	type entry struct {
		descriptor map[string]any
		score      float64
		fromLocal  bool
	}
	by := map[string]*entry{}
	order := []string{}
	add := func(id string, desc map[string]any, rank int, fromLocal bool) {
		e, ok := by[id]
		if !ok {
			e = &entry{descriptor: desc, fromLocal: fromLocal}
			by[id] = e
			order = append(order, id)
		} else if fromLocal && e.descriptor["overlay"] != true {
			// Annotate the merged descriptor when the local stream
			// supplies it so the host knows the hit also lives on
			// the workspace overlay.
			e.descriptor["overlay"] = true
		}
		e.score += 1.0 / (k + float64(rank+1))
	}
	for i, r := range registry {
		id, _ := r["id"].(string)
		if id == "" {
			continue
		}
		add(id, r, i, false)
	}
	for _, local := range locals {
		for i, r := range local {
			desc := map[string]any{
				"id":          r.ID,
				"type":        r.Type,
				"version":     r.Version,
				"description": r.Description,
				"score":       r.Score,
				"overlay":     true,
			}
			if len(r.Tags) > 0 {
				desc["tags"] = r.Tags
			}
			add(r.ID, desc, i, true)
		}
	}
	out := make([]*entry, 0, len(order))
	for _, id := range order {
		out = append(out, by[id])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	if len(out) > topK {
		out = out[:topK]
	}
	merged := make([]map[string]any, 0, len(out))
	for _, e := range out {
		merged = append(merged, e.descriptor)
	}
	return merged
}

// splitCSVMCP splits comma-separated values per the registry's
// existing convention; duplicated locally so cmd/podium-mcp does
// not import the server's helper package.
func splitCSVMCP(s string) []string {
	if s == "" {
		return nil
	}
	out := []string{}
	cur := ""
	for _, r := range s {
		if r == ',' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
