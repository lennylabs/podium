package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/lennylabs/podium/pkg/registry/core"
)

// usageBodyMaxBytes bounds a POST /v1/usage body. 500 artifact IDs and 100
// layer IDs fit well within it.
const usageBodyMaxBytes = 1 << 20

// usageRequest is the POST /v1/usage body: the GET parameters as JSON.
type usageRequest struct {
	From        string   `json:"from"`
	To          string   `json:"to"`
	Days        *int     `json:"days"`
	Granularity string   `json:"granularity"`
	Layers      []string `json:"layers"`
	Artifacts   []string `json:"artifacts"`
}

// handleUsage serves the §8.7 usage report. GET /v1/usage takes from, to,
// days, granularity, and repeated layer and artifact query parameters, and POST /v1/usage
// takes the same parameters as a JSON body for a list too long for a query
// string. Any caller that can search can call it: the request passes through
// the same identity verification, tenant routing, and browser-session
// handling as /v1/search_artifacts, and every total covers only the layers
// the caller's identity can see (§4.6). Both methods are reads, so a
// read-only registry (§13.2.1) serves them.
//
// Spec: §8.7
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	var (
		query core.UsageQuery
		err   error
	)
	switch r.Method {
	case http.MethodGet:
		query, err = usageQueryFromURL(r.URL.Query())
	case http.MethodPost:
		query, err = usageQueryFromBody(r.Body)
	default:
		writeError(w, http.StatusMethodNotAllowed, "registry.invalid_argument",
			"method not allowed: "+r.Method)
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", err.Error())
		return
	}
	query.Retention = s.usageRetentionDays
	if query.Retention <= 0 {
		query.Retention = DefaultUsageRetentionDays
	}
	report, err := s.core.UsageReport(r.Context(), s.identity(r), query)
	if err != nil {
		s.writeCoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// usageQueryFromURL reads the GET parameters. A days that is not an integer
// is reported here; the range checks run in the core for both methods.
func usageQueryFromURL(q url.Values) (core.UsageQuery, error) {
	query := core.UsageQuery{From: q.Get("from"), To: q.Get("to"), Granularity: q.Get("granularity"), LayerIDs: q["layer"], ArtifactIDs: q["artifact"]}
	if q.Has("days") {
		days, err := strconv.Atoi(q.Get("days"))
		if err != nil || days < 1 {
			return core.UsageQuery{}, errors.New("days must be a positive integer: " + q.Get("days"))
		}
		query.Days = days
	}
	return query, nil
}

// usageQueryFromBody reads the POST body.
func usageQueryFromBody(body io.Reader) (core.UsageQuery, error) {
	var req usageRequest
	dec := json.NewDecoder(io.LimitReader(body, usageBodyMaxBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return core.UsageQuery{}, errors.New("usage request body: " + err.Error())
	}
	query := core.UsageQuery{From: req.From, To: req.To, Granularity: req.Granularity, LayerIDs: req.Layers, ArtifactIDs: req.Artifacts}
	if req.Days != nil {
		if *req.Days < 1 {
			return core.UsageQuery{}, errors.New("days must be a positive integer: " + strconv.Itoa(*req.Days))
		}
		query.Days = *req.Days
	}
	return query, nil
}
