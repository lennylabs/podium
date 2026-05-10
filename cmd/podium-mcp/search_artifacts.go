package main

import (
	"encoding/json"
	"sort"
)

// searchArtifacts fans the §5 search_artifacts call out to the
// registry AND the §6.4.1 local index over workspace-overlay
// records, fusing the two ranked lists via RRF before returning
// to the host.
func (s *mcpServer) searchArtifacts(args map[string]any) any {
	body, err := s.fetchJSON("/v1/search_artifacts", args)
	if err != nil {
		return errorResult(err.Error())
	}
	var registry struct {
		Query        string           `json:"query"`
		TotalMatched int              `json:"total_matched"`
		Results      []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(body, &registry); err != nil {
		return errorResult("decode search_artifacts: " + err.Error())
	}
	if len(s.overlay) == 0 {
		// No overlay: pass the registry response through untouched
		// so single-deployment behavior is unchanged.
		return jsonAny(body)
	}

	query, _ := args["query"].(string)
	typeFilter, _ := args["type"].(string)
	scope, _ := args["scope"].(string)
	tags := tagsArg(args)
	topK := topKArg(args)
	local := localSearch(s.overlay, query, typeFilter, scope, tags, topK)

	fused := rrfFuse(registry.Results, local, topK)
	return map[string]any{
		"query":         registry.Query,
		"total_matched": registry.TotalMatched + len(local),
		"results":       fused,
	}
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

// rrfFuse blends the registry's ordered results with the local
// search hits via reciprocal rank fusion (k=60, the
// Cormack/Clarke default mirrored from the registry's vector
// path). Items appearing in both streams sum their reciprocal
// ranks so a hit ranked highly by either backend rises.
func rrfFuse(registry []map[string]any, local []localSearchResult, topK int) []map[string]any {
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
