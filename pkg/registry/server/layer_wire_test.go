package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// layerObjectMembers is the §7.3.1 layer object minus the two members
// §7.3.1 states are conditional (force_push_policy and last_ingested_at).
// Every arm below asserts set equality against this list plus whichever
// conditional member its fixture supplies, so a member added later without
// a spec decision fails the test rather than reaching the wire unnoticed,
// which is how the tenant identifier reached it.
var layerObjectMembers = []string{
	"id", "source_type", "repo", "ref", "root", "local_path", "order",
	"user_defined", "owner", "public", "organization", "groups", "users",
	"git_provider", "last_ingested_ref", "created_at", "deleted_at",
}

// mustPut sends a PUT with a JSON body, which is the verb the update route
// documents beside POST.
func mustPut(t *testing.T, base, path string, body any) (*http.Response, []byte) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPut, base+path, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("build PUT %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", path, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}

// layerEnvelope decodes a response body into its top-level members without
// interpreting any of them, so an assertion reads the wire names rather than
// a Go struct's fields. Sharing an encoder and a decoder is what made the
// existing layer tests blind to the field names.
func layerEnvelope(t *testing.T, body []byte) map[string]json.RawMessage {
	t.Helper()
	var env map[string]json.RawMessage
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope: %v\nbody: %s", err, body)
	}
	return env
}

// layerMembersOf returns the member names of one layer object.
func layerMembersOf(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("decode layer object: %v\nobject: %s", err, raw)
	}
	names := make([]string, 0, len(obj))
	for k := range obj {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// wantMembers returns the base member list plus the named conditional ones.
func wantMembers(extra ...string) []string {
	out := append(append([]string{}, layerObjectMembers...), extra...)
	sort.Strings(out)
	return out
}

// assertMembers asserts set equality between a layer object's members and
// the expected set.
func assertMembers(t *testing.T, where string, raw json.RawMessage, want []string) {
	t.Helper()
	got := layerMembersOf(t, raw)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: layer members = %v, want %v", where, got, want)
	}
}

// singleLayer returns the one layer object under "layer" in an envelope.
func singleLayer(t *testing.T, body []byte) json.RawMessage {
	t.Helper()
	raw, ok := layerEnvelope(t, body)["layer"]
	if !ok {
		t.Fatalf("response carries no \"layer\" member\nbody: %s", body)
	}
	return raw
}

// layerList returns the array under "layers" in an envelope.
func layerList(t *testing.T, body []byte) []json.RawMessage {
	t.Helper()
	raw, ok := layerEnvelope(t, body)["layers"]
	if !ok {
		t.Fatalf("response carries no \"layers\" member\nbody: %s", body)
	}
	var out []json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode layer array: %v\nbody: %s", err, body)
	}
	return out
}

// findLayer returns the layer object in a list whose "id" is id.
func findLayer(t *testing.T, body []byte, id string) json.RawMessage {
	t.Helper()
	for _, raw := range layerList(t, body) {
		var obj struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &obj); err != nil {
			t.Fatalf("decode layer object: %v", err)
		}
		if obj.ID == id {
			return raw
		}
	}
	t.Fatalf("layer %q not in list\nbody: %s", id, body)
	return nil
}

// Spec: §7.2.1 / §7.3.1 — every layer-management endpoint that returns a
// layer returns the §7.3.1 layer object, whose member names are lower
// snake_case. Each arm asserts set equality rather than containment.
func TestLayerEndpoint_LayerObjectMemberNames(t *testing.T) {
	t.Parallel()
	base, st, cleanup := newLayerHarness(t)
	defer cleanup()

	// A git registration that sets a force-push policy, so the arm covers
	// the conditional member a fixture supplies.
	resp, body := mustPost(t, base, "/v1/layers", map[string]any{
		"id": "team-finance", "source_type": "git",
		"repo": "git@github.com:acme/finance.git", "ref": "main",
		"root": "artifacts/", "organization": true,
		"force_push_policy": "strict",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register git status = %d, body=%s", resp.StatusCode, body)
	}
	gitLayer := singleLayer(t, body)
	assertMembers(t, "POST /v1/layers (git)", gitLayer, wantMembers("force_push_policy"))

	// An organization registration names neither a group nor a user, so both
	// visibility members reach the wire as null rather than as [] or as an
	// omitted key.
	var gitObj map[string]json.RawMessage
	if err := json.Unmarshal(gitLayer, &gitObj); err != nil {
		t.Fatalf("decode git layer: %v", err)
	}
	for _, member := range []string{"groups", "users"} {
		if got := string(gitObj[member]); got != "null" {
			t.Errorf("%s on a layer that sets none = %s, want null", member, got)
		}
	}

	// A local registration that sets no policy and has never ingested, so
	// both conditional members are absent.
	resp, body = mustPost(t, base, "/v1/layers", map[string]any{
		"id": "alice-personal", "source_type": "local", "local_path": "/tmp/x",
		"user_defined": true, "owner": "alice",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register local status = %d, body=%s", resp.StatusCode, body)
	}
	local := singleLayer(t, body)
	assertMembers(t, "POST /v1/layers (local)", local, wantMembers())

	// groups is null when unset and deleted_at is present and null on a live
	// layer. users carries the implicit registrant entry here, because a
	// user-defined registration grants its registrant read access.
	var localObj map[string]json.RawMessage
	if err := json.Unmarshal(local, &localObj); err != nil {
		t.Fatalf("decode local layer: %v", err)
	}
	for _, member := range []string{"groups", "deleted_at"} {
		if got := string(localObj[member]); got != "null" {
			t.Errorf("%s on a layer that sets none = %s, want null", member, got)
		}
	}
	var users []string
	if err := json.Unmarshal(localObj["users"], &users); err != nil {
		t.Fatalf("decode users: %v", err)
	}
	if len(users) != 1 || users[0] != "alice" {
		t.Errorf("users = %v, want the implicit registrant entry", users)
	}

	// An update that requests no rotation.
	resp, body = mustPut(t, base, "/v1/layers/update?id=team-finance", map[string]any{
		"ref": "release-v2",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, body=%s", resp.StatusCode, body)
	}
	assertMembers(t, "PUT /v1/layers/update (no rotation)", singleLayer(t, body), wantMembers("force_push_policy"))

	// An update that rotates the webhook secret.
	resp, body = mustPut(t, base, "/v1/layers/update?id=team-finance", map[string]any{
		"rotate_webhook_secret": true,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rotating update status = %d, body=%s", resp.StatusCode, body)
	}
	assertMembers(t, "PUT /v1/layers/update (rotation)", singleLayer(t, body), wantMembers("force_push_policy"))

	// A layer that has completed an ingest carries last_ingested_at. The
	// register path sets neither conditional member, so the stamp is seeded
	// through the store the way the ingest pipeline records it.
	ingested, err := st.GetLayerConfig(context.Background(), "t", "alice-personal")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	at := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	ingested.LastIngestedAt = &at
	if err := st.PutLayerConfig(context.Background(), ingested); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}

	listBody := mustGet(t, base, "/v1/layers")
	assertMembers(t, "GET /v1/layers (ingested)", findLayer(t, listBody, "alice-personal"), wantMembers("last_ingested_at"))
	assertMembers(t, "GET /v1/layers (policy)", findLayer(t, listBody, "team-finance"), wantMembers("force_push_policy"))

	// The reorder response returns the same object.
	resp, body = mustPost(t, base, "/v1/layers/reorder", map[string]any{
		"order": []string{"alice-personal", "team-finance"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reorder status = %d, body=%s", resp.StatusCode, body)
	}
	assertMembers(t, "POST /v1/layers/reorder", findLayer(t, body, "team-finance"), wantMembers("force_push_policy"))

	// The deleted read carries the tombstone stamp under deleted_at.
	resp, body = mustDelete(t, base, "/v1/layers?id=alice-personal")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unregister status = %d, body=%s", resp.StatusCode, body)
	}
	deletedBody := mustGet(t, base, "/v1/layers?deleted=true")
	deleted := findLayer(t, deletedBody, "alice-personal")
	assertMembers(t, "GET /v1/layers?deleted=true", deleted, wantMembers("last_ingested_at"))
	var deletedObj map[string]json.RawMessage
	if err := json.Unmarshal(deleted, &deletedObj); err != nil {
		t.Fatalf("decode deleted layer: %v", err)
	}
	if got := string(deletedObj["deleted_at"]); got == "null" || got == "" {
		t.Errorf("deleted_at on a tombstoned layer = %q, want the stamp", got)
	}
}

// Spec: §7.2.1 / §7.3.1 — a caller who may read no layer reads the empty
// array, whose body is exactly {"layers":[]}.
func TestLayerEndpoint_EmptyListBodyIsExact(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()

	var compact bytes.Buffer
	if err := json.Compact(&compact, mustGet(t, base, "/v1/layers")); err != nil {
		t.Fatalf("compact the list body: %v", err)
	}
	if got := compact.String(); got != `{"layers":[]}` {
		t.Errorf("empty list body = %s, want {\"layers\":[]}", got)
	}
}

// Spec: §7.2.1 / §7.3.1 — the layer object carries neither the layer's
// inbound webhook HMAC secret nor a tenant identifier. The assertion runs
// against the raw bytes rather than a decoded key name, so it survives a
// refactor that reintroduces either under a new name.
func TestLayerEndpoint_LayerObjectWithholdsSecretAndTenant(t *testing.T) {
	t.Parallel()
	base, _, cleanup := newLayerHarness(t)
	defer cleanup()

	resp, body := mustPost(t, base, "/v1/layers", map[string]any{
		"id": "team-finance", "source_type": "git",
		"repo": "git@github.com:acme/finance.git", "ref": "main",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d, body=%s", resp.StatusCode, body)
	}
	env := layerEnvelope(t, body)
	var secret string
	if err := json.Unmarshal(env["webhook_secret"], &secret); err != nil {
		t.Fatalf("decode webhook_secret: %v\nbody: %s", err, body)
	}
	if secret == "" {
		t.Fatalf("registration returned no webhook secret\nbody: %s", body)
	}
	// The one-time credential reaches the caller through webhook_secret
	// alone: with that member removed, the value appears nowhere.
	delete(env, "webhook_secret")
	rest, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("re-marshal envelope: %v", err)
	}
	if bytes.Contains(rest, []byte(secret)) {
		t.Errorf("the registration echoes the webhook secret outside webhook_secret:\n%s", rest)
	}

	resp, updated := mustPut(t, base, "/v1/layers/update?id=team-finance", map[string]any{"ref": "v2"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, body=%s", resp.StatusCode, updated)
	}
	resp, reordered := mustPost(t, base, "/v1/layers/reorder", map[string]any{"order": []string{"team-finance"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reorder status = %d, body=%s", resp.StatusCode, reordered)
	}
	live := mustGet(t, base, "/v1/layers")

	// The deleted read is the one route whose records come from
	// ListDeletedLayerConfigs, so it is driven over a tombstoned git layer
	// rather than over an empty list. Unregistering the layer that holds the
	// captured secret makes both assertions below carry weight on that route.
	resp, unregistered := mustDelete(t, base, "/v1/layers?id=team-finance")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unregister status = %d, body=%s", resp.StatusCode, unregistered)
	}
	deletedBody := mustGet(t, base, "/v1/layers?deleted=true")
	// findLayer fails the arm when the tombstoned layer is absent, so the
	// assertions over deletedBody cannot silently run over an empty list.
	findLayer(t, deletedBody, "team-finance")

	bodies := map[string][]byte{
		"POST /v1/layers":         body,
		"PUT /v1/layers/update":   updated,
		"GET /v1/layers":          live,
		"GET /v1/layers?deleted":  deletedBody,
		"POST /v1/layers/reorder": reordered,
	}
	for where, raw := range bodies {
		if where != "POST /v1/layers" && bytes.Contains(raw, []byte(secret)) {
			t.Errorf("%s carries the layer's webhook secret:\n%s", where, raw)
		}
		for _, name := range []string{`"tenant_id"`, `"TenantID"`} {
			if bytes.Contains(raw, []byte(name)) {
				t.Errorf("%s carries a %s key:\n%s", where, name, raw)
			}
		}
	}
}

// Spec: §7.2.1 / §7.3.1 — one name for one value across a request and its response:
// every member the register request accepts is answered under the same name.
func TestLayerEndpoint_RequestAndResponseAgreeOnNames(t *testing.T) {
	t.Parallel()

	// rotate_webhook_secret is an action the request asks for rather than a
	// stored member, so the response answers no field of that name.
	requestOnly := map[string]bool{"rotate_webhook_secret": true}
	// The response carries members no register request sets: the server
	// assigns the precedence and the ingest bookkeeping.
	responseOnly := map[string]bool{
		"order": true, "last_ingested_ref": true, "created_at": true,
		"deleted_at": true, "last_ingested_at": true,
	}

	req := jsonTagNames(reflect.TypeOf(server.LayerRegisterRequest{}))
	resp := jsonTagNames(reflect.TypeOf(store.LayerConfig{}))
	for name := range req {
		if requestOnly[name] {
			continue
		}
		if !resp[name] {
			t.Errorf("the register request accepts %q and the layer object answers no member of that name", name)
		}
	}
	for name := range resp {
		if responseOnly[name] {
			continue
		}
		if !req[name] {
			t.Errorf("the layer object answers %q and the register request accepts no field of that name", name)
		}
	}
}

// jsonTagNames returns the JSON member names a struct type marshals, skipping
// the fields tagged json:"-".
func jsonTagNames(t reflect.Type) map[string]bool {
	out := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "" {
			name = f.Name
		}
		out[name] = true
	}
	return out
}
