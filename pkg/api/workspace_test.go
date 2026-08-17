package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/saker-ai/filehub/pkg/config"
	"github.com/saker-ai/filehub/pkg/processing"
	blob "github.com/saker-ai/filehub/pkg/storage"
	"github.com/saker-ai/filehub/pkg/store/gormstore"
	"github.com/saker-ai/filehub/pkg/workspace"
	"github.com/saker-ai/filehub/pkg/workspace/gormrepo"
	"github.com/saker-ai/filehub/pkg/workspaceapi"
	"github.com/saker-ai/saker-common/internaljwt"
)

// wsTestServer is a FileHub test server with the Workspace subsystem
// enabled. API-key authentication maps every request to tenant "default";
// multi-tenant scenarios use internal JWTs (see newJWTWorkspaceServer).
type wsTestServer struct {
	t      *testing.T
	router http.Handler
	db     *gormstore.Store
	blobs  *blob.Store
}

func newWorkspaceServer(t *testing.T) *wsTestServer {
	t.Helper()
	cfg := config.Defaults()
	cfg.APIKeyAuthEnabled = true
	cfg.APIKeys = []string{"test-key"}
	cfg.WebEnabled = false
	cfg.Workspaces.Enabled = true
	return newWorkspaceServerWithConfig(t, cfg)
}

func newWorkspaceServerWithConfig(t *testing.T, cfg config.Config) *wsTestServer {
	t.Helper()
	ctx := context.Background()
	cfg.DSN = "sqlite://" + filepath.Join(t.TempDir(), "workspaces.db")
	if cfg.Storage.Backend == "" || cfg.Storage.Backend == config.BackendOSFS {
		cfg.Storage.Backend = config.BackendMemFS
		cfg.Storage.DataDir = ""
	}
	cfg.PresignSecret = "test-secret"
	cfg.RatePerSec = 10000
	cfg.RateBurst = 10000
	cfg.MaxConcurrentUploads = 64
	if cfg.InternalAuth.Enabled {
		if cfg.InternalAuth.MasterSecret == "" {
			cfg.InternalAuth.MasterSecret = "workspace-test-master-secret-0123456789"
		}
		if cfg.InternalAuth.Issuer == "" {
			cfg.InternalAuth.Issuer = "synapse"
		}
		if cfg.InternalAuth.Audience == "" {
			cfg.InternalAuth.Audience = "filehub"
		}
	}

	db, err := gormstore.Open(ctx, cfg.DSN)
	if err != nil {
		t.Fatalf("gormstore.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	blobs, err := blob.New(ctx, cfg)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	pipeline := processing.New(1, blobs, db, nil)
	metrics := NewMetrics()
	deps := RouterDeps{
		Config: cfg, Assets: db, Uploads: db, AIReviews: db, Reviews: db,
		Storage: blobs, Pipeline: pipeline, Metrics: metrics,
	}
	if cfg.Workspaces.Enabled {
		repo, err := gormrepo.New(ctx, db.DB())
		if err != nil {
			t.Fatalf("gormrepo.New: %v", err)
		}
		svc := workspace.New(repo, db, workspace.DefaultLimits())
		deps.Workspaces = workspaceapi.Deps{Service: svc, Assets: db, Storage: blobs, Metrics: metrics}
	}
	router := NewRouter(deps)
	return &wsTestServer{t: t, router: router, db: db, blobs: blobs}
}

func (ws *wsTestServer) do(t *testing.T, method, target string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	req.Header.Set("Authorization", "Bearer test-key")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	ws.router.ServeHTTP(rec, req)
	return rec
}

func (ws *wsTestServer) doJSON(t *testing.T, method, target string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(data)
	}
	if headers == nil {
		headers = map[string]string{}
	}
	headers["Content-Type"] = "application/json"
	return ws.do(t, method, target, reader, headers)
}

// uploadAsset uploads file content through the existing asset API and
// returns the asset ID.
func (ws *wsTestServer) uploadAsset(t *testing.T, name string, data []byte) string {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("purpose", "user_data")
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	rec := ws.do(t, http.MethodPost, "/v1/assets?on_duplicate=reuse", &buf, map[string]string{"Content-Type": mw.FormDataContentType()})
	if rec.Code != http.StatusOK {
		t.Fatalf("upload status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode upload: %v", err)
	}
	return out["id"].(string)
}

func (ws *wsTestServer) createWorkspace(t *testing.T, name string) string {
	t.Helper()
	rec := ws.doJSON(t, http.MethodPost, "/v1/workspaces", map[string]string{"name": name}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("create workspace status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out["id"].(string)
}

type commitOp map[string]any

func (ws *wsTestServer) commit(t *testing.T, workspaceID, idempotencyKey, deviceID, sessionID, note string, ops []commitOp) *httptest.ResponseRecorder {
	t.Helper()
	body := map[string]any{
		"device_id":  deviceID,
		"session_id": sessionID,
		"note":       note,
		"operations": ops,
	}
	return ws.doJSON(t, http.MethodPost, "/v1/workspaces/"+workspaceID+"/commits", body, map[string]string{"Idempotency-Key": idempotencyKey})
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return out
}

func requireStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) map[string]any {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status=%d want %d body=%s", rec.Code, want, rec.Body.String())
	}
	return decodeBody(t, rec)
}

// --- FH-01 / FH-08: lifecycle and tenant isolation -------------------------

func TestWorkspaceLifecycleAndTenantIsolation(t *testing.T) {
	ts := newWorkspaceServer(t)
	wsID := ts.createWorkspace(t, "team-sync")

	// Get and list (FH-01).
	rec := ts.do(t, http.MethodGet, "/v1/workspaces/"+wsID, nil, nil)
	body := requireStatus(t, rec, http.StatusOK)
	if body["name"] != "team-sync" || body["id"] != wsID {
		t.Fatalf("get workspace = %#v", body)
	}
	rec = ts.do(t, http.MethodGet, "/v1/workspaces", nil, nil)
	body = requireStatus(t, rec, http.StatusOK)
	data := body["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("list workspaces len=%d want 1", len(data))
	}

	// Patch name/description.
	rec = ts.doJSON(t, http.MethodPatch, "/v1/workspaces/"+wsID, map[string]string{"description": "shared agent workspace"}, nil)
	requireStatus(t, rec, http.StatusOK)

	// Cross-tenant isolation with internal JWTs (FH-08).
	jwtTS := newJWTWorkspaceServer(t)
	tokenA := jwtTS.signToken(t, "tenant-a", "agent-a")
	tokenB := jwtTS.signToken(t, "tenant-b", "agent-b")
	rec = jwtTS.doJWT(t, http.MethodPost, "/v1/workspaces", map[string]string{"name": "iso"}, tokenA)
	created := requireStatus(t, rec, http.StatusOK)
	isoID := created["id"].(string)

	// Tenant B cannot see tenant A's workspace.
	rec = jwtTS.doJWT(t, http.MethodGet, "/v1/workspaces/"+isoID, nil, tokenB)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant get status=%d want 404 body=%s", rec.Code, rec.Body.String())
	}
	rec = jwtTS.doJWT(t, http.MethodGet, "/v1/workspaces", nil, tokenB)
	body = requireStatus(t, rec, http.StatusOK)
	if items := body["data"].([]any); len(items) != 0 {
		t.Fatalf("cross-tenant list len=%d want 0", len(items))
	}

	// Tenant B cannot commit into tenant A's workspace at all.
	assetA := jwtTS.uploadAssetJWT(t, "a.txt", []byte("tenant-a content"), tokenA)
	rec = jwtTS.commitJWT(t, isoID, "req-x", "device-b", tokenB, []commitOp{{
		"kind": "put", "path": "stolen.txt", "asset_id": assetA, "base_revision_id": "",
	}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign workspace commit status=%d want 404 body=%s", rec.Code, rec.Body.String())
	}

	// Tenant B also cannot reference tenant A's asset from its own
	// workspace (FH-08 asset isolation).
	rec = jwtTS.doJWT(t, http.MethodPost, "/v1/workspaces", map[string]string{"name": "b-ws"}, tokenB)
	wsB := requireStatus(t, rec, http.StatusOK)["id"].(string)
	rec = jwtTS.commitJWT(t, wsB, "req-y", "device-b", tokenB, []commitOp{{
		"kind": "put", "path": "stolen.txt", "asset_id": assetA, "base_revision_id": "",
	}})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("cross-tenant asset commit status=%d want 422 body=%s", rec.Code, rec.Body.String())
	}

	// Soft delete: sync endpoints return 410 (doc §8.1, R-04).
	rec = ts.do(t, http.MethodDelete, "/v1/workspaces/"+wsID, nil, nil)
	requireStatus(t, rec, http.StatusOK)
	for _, target := range []string{
		"/v1/workspaces/" + wsID + "/tree",
		"/v1/workspaces/" + wsID + "/changes",
	} {
		rec = ts.do(t, http.MethodGet, target, nil, nil)
		if rec.Code != http.StatusGone {
			t.Fatalf("deleted workspace %s status=%d want 410", target, rec.Code)
		}
	}
	// Management get still works and reports deleted_at.
	rec = ts.do(t, http.MethodGet, "/v1/workspaces/"+wsID, nil, nil)
	body = requireStatus(t, rec, http.StatusOK)
	if body["deleted_at"] == nil {
		t.Fatalf("deleted_at missing after soft delete: %#v", body)
	}
}

// jwtTestServer extends wsTestServer with internal-JWT multi-tenant auth.
type jwtTestServer struct {
	*wsTestServer
	signer *internaljwt.Signer
	cfg    config.Config
}

func newJWTWorkspaceServer(t *testing.T) *jwtTestServer {
	t.Helper()
	cfg := config.Defaults()
	cfg.APIKeyAuthEnabled = false
	cfg.InternalAuth.Enabled = true
	cfg.InternalAuth.MasterSecret = "workspace-test-master-secret-0123456789"
	cfg.WebEnabled = false
	cfg.Workspaces.Enabled = true
	base := newWorkspaceServerWithConfig(t, cfg)
	signer, err := internaljwt.NewSigner(cfg.InternalAuth.Issuer, cfg.InternalAuth.MasterSecret, 5*time.Minute)
	if err != nil {
		t.Fatalf("internaljwt.NewSigner: %v", err)
	}
	return &jwtTestServer{wsTestServer: base, signer: signer, cfg: cfg}
}

func (js *jwtTestServer) signToken(t *testing.T, tenantID, principalID string) string {
	t.Helper()
	token, _, err := js.signer.Sign(internaljwt.SignInput{
		Audience:      js.cfg.InternalAuth.Audience,
		TenantID:      tenantID,
		PrincipalType: "agent",
		PrincipalID:   principalID,
		Scopes:        []string{internaljwt.ScopeFileHubAdmin},
	})
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return token
}

func (js *jwtTestServer) doJWT(t *testing.T, method, target string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set(internaljwt.HeaderInternalAuthorization, "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	js.router.ServeHTTP(rec, req)
	return rec
}

func (js *jwtTestServer) uploadAssetJWT(t *testing.T, name string, data []byte, token string) string {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("purpose", "user_data")
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/assets?on_duplicate=allow", &buf)
	req.Header.Set(internaljwt.HeaderInternalAuthorization, "Bearer "+token)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	js.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("jwt upload status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out["id"].(string)
}

func (js *jwtTestServer) commitJWT(t *testing.T, workspaceID, requestID, deviceID, token string, ops []commitOp) *httptest.ResponseRecorder {
	t.Helper()
	body := map[string]any{"device_id": deviceID, "operations": ops}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/workspaces/"+workspaceID+"/commits", bytes.NewReader(data))
	req.Header.Set(internaljwt.HeaderInternalAuthorization, "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", requestID)
	rec := httptest.NewRecorder()
	js.router.ServeHTTP(rec, req)
	return rec
}

// --- FH-02 / FH-03 / FH-04 / CF-02: commit and idempotency ------------------

func TestWorkspaceCommitIdempotency(t *testing.T) {
	ts := newWorkspaceServer(t)
	wsID := ts.createWorkspace(t, "idem")

	assetA := ts.uploadAsset(t, "a.txt", []byte("alpha"))
	assetB := ts.uploadAsset(t, "b.txt", []byte("beta"))

	ops := []commitOp{
		{"kind": "put", "path": "docs/a.txt", "asset_id": assetA, "base_revision_id": "", "mode": 420},
		{"kind": "put", "path": "docs/b.txt", "asset_id": assetB, "base_revision_id": "", "mode": 420},
	}
	rec := ts.commit(t, wsID, "req-1", "device-1", "session-1", "first commit", ops)
	first := requireStatus(t, rec, http.StatusOK)

	// FH-02: one commit, multiple ops, contiguous sequences starting at 1.
	if first["from_sequence"].(float64) != 1 || first["to_sequence"].(float64) != 2 {
		t.Fatalf("sequences = %v..%v want 1..2", first["from_sequence"], first["to_sequence"])
	}
	results := first["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("results len=%d want 2", len(results))
	}

	// FH-03: identical retry returns the same receipt, no new changes.
	rec = ts.commit(t, wsID, "req-1", "device-1", "session-1", "first commit", ops)
	second := requireStatus(t, rec, http.StatusOK)
	if second["to_sequence"].(float64) != 2 {
		t.Fatalf("retry to_sequence=%v want 2", second["to_sequence"])
	}
	rec = ts.do(t, http.MethodGet, "/v1/workspaces/"+wsID+"/changes?after=0&limit=100", nil, nil)
	body := requireStatus(t, rec, http.StatusOK)
	if changes := body["data"].([]any); len(changes) != 2 {
		t.Fatalf("changes after retry len=%d want 2 (idempotent replay must not append)", len(changes))
	}

	// FH-04: same key with a different body is rejected.
	otherOps := []commitOp{{"kind": "put", "path": "docs/c.txt", "asset_id": assetA, "base_revision_id": ""}}
	rec = ts.commit(t, wsID, "req-1", "device-1", "session-1", "different", otherOps)
	if rec.Code != http.StatusConflict {
		t.Fatalf("digest conflict status=%d want 409 body=%s", rec.Code, rec.Body.String())
	}

	// CF-02: retrying a conflict-producing commit does not create a second
	// conflict file: device-3 submits the same stale-base request twice with
	// one request id.
	assetC := ts.uploadAsset(t, "c.txt", []byte("gamma"))
	conflictOps := []commitOp{{"kind": "put", "path": "docs/a.txt", "asset_id": assetC, "base_revision_id": "wrev-does-not-match"}}
	rec = ts.commit(t, wsID, "req-conflict", "device-3", "session-3", "conflict", conflictOps)
	conflictFirst := requireStatus(t, rec, http.StatusOK)
	conflictPath := firstConflictPath(t, conflictFirst)

	rec = ts.commit(t, wsID, "req-conflict", "device-3", "session-3", "conflict", conflictOps)
	conflictSecond := requireStatus(t, rec, http.StatusOK)
	if firstConflictPath(t, conflictSecond) != conflictPath {
		t.Fatalf("conflict retry path changed: %q vs %q", conflictPath, firstConflictPath(t, conflictSecond))
	}
	rec = ts.do(t, http.MethodGet, "/v1/workspaces/"+wsID+"/tree?prefix=docs/", nil, nil)
	body = requireStatus(t, rec, http.StatusOK)
	entries := body["data"].([]any)
	count := 0
	for _, item := range entries {
		entry := item.(map[string]any)
		if strings.Contains(entry["path"].(string), ".saker-conflict-") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("conflict entries=%d want exactly 1 after retried conflict commit", count)
	}
}

func currentRevisionID(t *testing.T, ts *wsTestServer, wsID, path string) string {
	t.Helper()
	rec := ts.do(t, http.MethodGet, "/v1/workspaces/"+wsID+"/entries?path="+path, nil, nil)
	body := requireStatus(t, rec, http.StatusOK)
	rev := body["revision"].(map[string]any)
	return rev["id"].(string)
}

func firstConflictPath(t *testing.T, commitBody map[string]any) string {
	t.Helper()
	results := commitBody["results"].([]any)
	for _, item := range results {
		res := item.(map[string]any)
		if cp, ok := res["final_path"].(string); ok && cp != "" && cp != res["path"] {
			return cp
		}
	}
	t.Fatalf("no conflict final_path in commit response: %#v", commitBody)
	return ""
}

// --- CF-01 / CF-03: conflict semantics ---------------------------------------

func TestWorkspaceConflictSemantics(t *testing.T) {
	ts := newWorkspaceServer(t)
	wsID := ts.createWorkspace(t, "conflicts")

	// Both devices start from the same base (empty path).
	asset1 := ts.uploadAsset(t, "v1.txt", []byte("device one version"))
	rec := ts.commit(t, wsID, "req-d1", "device-1", "s1", "", []commitOp{
		{"kind": "put", "path": "shared/report.md", "asset_id": asset1, "base_revision_id": ""},
	})
	requireStatus(t, rec, http.StatusOK)
	baseRev := currentRevisionID(t, ts, wsID, "shared/report.md")

	// Device 2 updates from the shared base: applied.
	asset2 := ts.uploadAsset(t, "v2.txt", []byte("device two version"))
	rec = ts.commit(t, wsID, "req-d2", "device-2", "s2", "", []commitOp{
		{"kind": "put", "path": "shared/report.md", "asset_id": asset2, "base_revision_id": baseRev},
	})
	body := requireStatus(t, rec, http.StatusOK)
	results := body["results"].([]any)
	if results[0].(map[string]any)["resolution"] != workspace.ResolutionApplied {
		t.Fatalf("expected applied resolution, got %#v", results[0])
	}
	keptRev := currentRevisionID(t, ts, wsID, "shared/report.md")

	// CF-01: device 3 commits the same path from the stale base: the main
	// path keeps device 2's content and device 3's content lands on a
	// deterministic conflict path.
	asset3 := ts.uploadAsset(t, "v3.txt", []byte("device three version"))
	rec = ts.commit(t, wsID, "req-d3", "device-3", "s3", "", []commitOp{
		{"kind": "put", "path": "shared/report.md", "asset_id": asset3, "base_revision_id": baseRev},
	})
	body = requireStatus(t, rec, http.StatusOK)
	results = body["results"].([]any)
	res := results[0].(map[string]any)
	if res["resolution"] != workspace.ResolutionConflict {
		t.Fatalf("expected conflict resolution, got %#v", res)
	}
	conflictPath := res["final_path"].(string)
	// token8 maps non-alphanumerics to '-': device-3 → "device-3",
	// req-d3 → "req-d3--" (padded to 8 chars).
	wantPrefix := "shared/report.saker-conflict-device-3-req-d3--"
	if !strings.HasPrefix(conflictPath, wantPrefix) {
		t.Fatalf("conflict path %q want prefix %q", conflictPath, wantPrefix)
	}
	if _, hasRev := res["revision"].(map[string]any); !hasRev {
		t.Fatalf("conflict result missing revision object: %#v", res)
	}
	if res["kept_revision_id"] != keptRev {
		t.Fatalf("kept_revision_id=%v want %s", res["kept_revision_id"], keptRev)
	}
	// Main path still points at device 2's revision.
	if got := currentRevisionID(t, ts, wsID, "shared/report.md"); got != keptRev {
		t.Fatalf("main path revision=%s want %s", got, keptRev)
	}
	// Both contents exist as entries.
	rec = ts.do(t, http.MethodGet, "/v1/workspaces/"+wsID+"/entries?path="+conflictPath, nil, nil)
	requireStatus(t, rec, http.StatusOK)

	// CF-03: a delete from a stale base must not remove the newer remote
	// version.
	rec = ts.commit(t, wsID, "req-d4", "device-4", "s4", "", []commitOp{
		{"kind": "delete", "path": "shared/report.md", "base_revision_id": baseRev},
	})
	body = requireStatus(t, rec, http.StatusOK)
	results = body["results"].([]any)
	res = results[0].(map[string]any)
	if res["resolution"] != workspace.ResolutionConflict {
		t.Fatalf("delete conflict resolution=%#v", res)
	}
	if res["kept_revision_id"] != keptRev {
		t.Fatalf("delete conflict kept_revision_id=%v want %s", res["kept_revision_id"], keptRev)
	}
	if got := currentRevisionID(t, ts, wsID, "shared/report.md"); got != keptRev {
		t.Fatalf("path removed by conflicting delete: revision=%s", got)
	}

	// A delete from the correct base applies.
	rec = ts.commit(t, wsID, "req-d5", "device-5", "s5", "", []commitOp{
		{"kind": "delete", "path": "shared/report.md", "base_revision_id": keptRev},
	})
	requireStatus(t, rec, http.StatusOK)
	rec = ts.do(t, http.MethodGet, "/v1/workspaces/"+wsID+"/entries?path=shared/report.md", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("deleted entry status=%d want 404", rec.Code)
	}
}

// --- FH-05 / FH-06: pagination ------------------------------------------------

func TestWorkspaceChangesPagination(t *testing.T) {
	ts := newWorkspaceServer(t)
	wsID := ts.createWorkspace(t, "paging")

	for i := 0; i < 3; i++ {
		asset := ts.uploadAsset(t, fmt.Sprintf("f%d.txt", i), []byte(fmt.Sprintf("content %d", i)))
		rec := ts.commit(t, wsID, fmt.Sprintf("req-%d", i), "device-1", "s", "", []commitOp{
			{"kind": "put", "path": fmt.Sprintf("dir/f%d.txt", i), "asset_id": asset},
			{"kind": "put", "path": fmt.Sprintf("dir/g%d.txt", i), "asset_id": asset},
		})
		requireStatus(t, rec, http.StatusOK)
	}

	seen := map[float64]bool{}
	var after int64
	pages := 0
	for {
		rec := ts.do(t, http.MethodGet, fmt.Sprintf("/v1/workspaces/%s/changes?after=%d&limit=2", wsID, after), nil, nil)
		body := requireStatus(t, rec, http.StatusOK)
		items := body["data"].([]any)
		if len(items) > 2 {
			t.Fatalf("page size %d exceeds limit 2", len(items))
		}
		for _, item := range items {
			seq := item.(map[string]any)["sequence"].(float64)
			if seen[seq] {
				t.Fatalf("duplicate sequence %v across pages", seq)
			}
			seen[seq] = true
		}
		next := int64(body["next_sequence"].(float64))
		if !body["has_more"].(bool) {
			if len(items) == 0 && pages > 0 {
				break
			}
			if next == after && len(items) == 0 {
				break
			}
			if len(items) < 2 {
				break
			}
		}
		if next == after {
			break
		}
		after = next
		pages++
		if pages > 10 {
			t.Fatal("pagination did not terminate")
		}
	}
	if len(seen) != 6 {
		t.Fatalf("paged %d distinct sequences, want 6", len(seen))
	}
	for seq := 1; seq <= 6; seq++ {
		if !seen[float64(seq)] {
			t.Fatalf("missing sequence %d", seq)
		}
	}
}

func TestWorkspaceTreePaginationAndPrefix(t *testing.T) {
	ts := newWorkspaceServer(t)
	wsID := ts.createWorkspace(t, "tree")

	paths := []string{"b/2.txt", "a/1.txt", "a/3.txt", "b/1.txt", "root.txt"}
	asset := ts.uploadAsset(t, "t.txt", []byte("tree content"))
	ops := make([]commitOp, 0, len(paths))
	for _, p := range paths {
		ops = append(ops, commitOp{"kind": "put", "path": p, "asset_id": asset})
	}
	rec := ts.commit(t, wsID, "req-tree", "device-1", "s", "", ops)
	requireStatus(t, rec, http.StatusOK)

	// Full tree sorted by path.
	rec = ts.do(t, http.MethodGet, "/v1/workspaces/"+wsID+"/tree?limit=100", nil, nil)
	body := requireStatus(t, rec, http.StatusOK)
	items := body["data"].([]any)
	got := make([]string, 0, len(items))
	for _, item := range items {
		got = append(got, item.(map[string]any)["path"].(string))
	}
	want := []string{"a/1.txt", "a/3.txt", "b/1.txt", "b/2.txt", "root.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("tree order = %v want %v", got, want)
	}

	// Prefix filter.
	rec = ts.do(t, http.MethodGet, "/v1/workspaces/"+wsID+"/tree?prefix=a/&limit=100", nil, nil)
	body = requireStatus(t, rec, http.StatusOK)
	items = body["data"].([]any)
	if len(items) != 2 {
		t.Fatalf("prefix a/ len=%d want 2", len(items))
	}

	// Cursor pagination: page size 2 walks all 5 entries without repeats.
	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 5; page++ {
		target := "/v1/workspaces/" + wsID + "/tree?limit=2"
		if cursor != "" {
			target += "&cursor=" + cursor
		}
		rec = ts.do(t, http.MethodGet, target, nil, nil)
		body = requireStatus(t, rec, http.StatusOK)
		items = body["data"].([]any)
		for _, item := range items {
			p := item.(map[string]any)["path"].(string)
			if seen[p] {
				t.Fatalf("duplicate path %q across pages", p)
			}
			seen[p] = true
		}
		if !body["has_more"].(bool) {
			break
		}
		cursor = body["next_cursor"].(string)
		if cursor == "" {
			t.Fatal("has_more without next_cursor")
		}
	}
	if len(seen) != 5 {
		t.Fatalf("paged %d paths, want 5", len(seen))
	}
}

// --- FH-07: restore ------------------------------------------------------------

func TestWorkspaceRestore(t *testing.T) {
	ts := newWorkspaceServer(t)
	wsID := ts.createWorkspace(t, "restore")

	v1 := ts.uploadAsset(t, "v1.txt", []byte("version one"))
	rec := ts.commit(t, wsID, "req-r1", "device-1", "s", "", []commitOp{{"kind": "put", "path": "notes.md", "asset_id": v1}})
	requireStatus(t, rec, http.StatusOK)
	rev1 := currentRevisionID(t, ts, wsID, "notes.md")

	v2 := ts.uploadAsset(t, "v2.txt", []byte("version two"))
	rec = ts.commit(t, wsID, "req-r2", "device-1", "s", "", []commitOp{{"kind": "put", "path": "notes.md", "asset_id": v2, "base_revision_id": rev1}})
	requireStatus(t, rec, http.StatusOK)
	rev2 := currentRevisionID(t, ts, wsID, "notes.md")
	if rev2 == rev1 {
		t.Fatal("second commit did not create a new revision")
	}

	// Restore the first revision: creates a new revision, history intact.
	rec = ts.doJSON(t, http.MethodPost, "/v1/workspaces/"+wsID+"/restore", map[string]string{"path": "notes.md", "revision_id": rev1, "note": "rollback"}, nil)
	body := requireStatus(t, rec, http.StatusOK)
	restoredRev := body["revision_id"].(string)
	if restoredRev == rev1 || restoredRev == rev2 {
		t.Fatalf("restore must create a new revision, got %s", restoredRev)
	}
	if got := currentRevisionID(t, ts, wsID, "notes.md"); got != restoredRev {
		t.Fatalf("current revision=%s want restored %s", got, restoredRev)
	}

	// History still lists all revisions (oldest last).
	rec = ts.do(t, http.MethodGet, "/v1/workspaces/"+wsID+"/history?path=notes.md&limit=100", nil, nil)
	body = requireStatus(t, rec, http.StatusOK)
	history := body["data"].([]any)
	if len(history) != 3 {
		t.Fatalf("history len=%d want 3", len(history))
	}

	// Restoring a revision from another path is rejected.
	rec = ts.doJSON(t, http.MethodPost, "/v1/workspaces/"+wsID+"/restore", map[string]string{"path": "other.md", "revision_id": rev1}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("cross-path restore status=%d want 400 body=%s", rec.Code, rec.Body.String())
	}
}

// --- FH-09: concurrent commits ---------------------------------------------------

func TestWorkspaceConcurrentCommitsSQLite(t *testing.T) {
	ts := newWorkspaceServer(t)
	wsID := ts.createWorkspace(t, "concurrent")

	const devices = 6
	const commitsPerDevice = 4
	var wg sync.WaitGroup
	errCh := make(chan error, devices*commitsPerDevice)
	for d := 0; d < devices; d++ {
		for c := 0; c < commitsPerDevice; c++ {
			wg.Add(1)
			go func(d, c int) {
				defer wg.Done()
				name := fmt.Sprintf("d%d-c%d.txt", d, c)
				content := []byte(fmt.Sprintf("device %d commit %d", d, c))
				var buf bytes.Buffer
				mw := multipart.NewWriter(&buf)
				_ = mw.WriteField("purpose", "user_data")
				fw, err := mw.CreateFormFile("file", name)
				if err != nil {
					errCh <- err
					return
				}
				if _, err := fw.Write(content); err != nil {
					errCh <- err
					return
				}
				_ = mw.Close()
				uploadReq := httptest.NewRequest(http.MethodPost, "/v1/assets?on_duplicate=allow", &buf)
				uploadReq.Header.Set("Authorization", "Bearer test-key")
				uploadReq.Header.Set("Content-Type", mw.FormDataContentType())
				uploadRec := httptest.NewRecorder()
				ts.router.ServeHTTP(uploadRec, uploadReq)
				if uploadRec.Code != http.StatusOK {
					errCh <- fmt.Errorf("upload failed: %d %s", uploadRec.Code, uploadRec.Body.String())
					return
				}
				var uploadOut map[string]any
				if err := json.Unmarshal(uploadRec.Body.Bytes(), &uploadOut); err != nil {
					errCh <- err
					return
				}
				assetID := uploadOut["id"].(string)

				commitBody, _ := json.Marshal(map[string]any{
					"device_id": fmt.Sprintf("device-%d", d),
					"operations": []commitOp{
						{"kind": "put", "path": "concurrent/" + name, "asset_id": assetID},
					},
				})
				req := httptest.NewRequest(http.MethodPost, "/v1/workspaces/"+wsID+"/commits", bytes.NewReader(commitBody))
				req.Header.Set("Authorization", "Bearer test-key")
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Idempotency-Key", fmt.Sprintf("d%d-c%d", d, c))
				rec := httptest.NewRecorder()
				ts.router.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					errCh <- fmt.Errorf("commit failed: %d %s", rec.Code, rec.Body.String())
					return
				}
			}(d, c)
		}
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent commit: %v", err)
	}

	// Workspace sequence equals the total number of committed operations.
	rec := ts.do(t, http.MethodGet, "/v1/workspaces/"+wsID, nil, nil)
	body := requireStatus(t, rec, http.StatusOK)
	total := devices * commitsPerDevice
	if seq := int64(body["sequence"].(float64)); seq != int64(total) {
		t.Fatalf("workspace sequence=%d want %d", seq, total)
	}

	// All sequences are unique, contiguous and monotonic.
	sequences := map[int64]bool{}
	var after int64
	for {
		rec := ts.do(t, http.MethodGet, fmt.Sprintf("/v1/workspaces/%s/changes?after=%d&limit=500", wsID, after), nil, nil)
		body := requireStatus(t, rec, http.StatusOK)
		items := body["data"].([]any)
		var last int64
		for _, item := range items {
			seq := int64(item.(map[string]any)["sequence"].(float64))
			if sequences[seq] {
				t.Fatalf("duplicate sequence %d", seq)
			}
			if seq <= last || seq <= after {
				t.Fatalf("non-monotonic sequence %d after %d", seq, after)
			}
			sequences[seq] = true
			last = seq
		}
		next := int64(body["next_sequence"].(float64))
		if next == after || len(items) == 0 {
			break
		}
		after = next
	}
	if len(sequences) != total {
		t.Fatalf("distinct sequences=%d want %d", len(sequences), total)
	}

	// Every committed path is present in the tree.
	rec = ts.do(t, http.MethodGet, "/v1/workspaces/"+wsID+"/tree?prefix=concurrent/&limit=500", nil, nil)
	body = requireStatus(t, rec, http.StatusOK)
	if entries := body["data"].([]any); len(entries) != total {
		t.Fatalf("tree entries=%d want %d", len(entries), total)
	}
}

// --- FH-11: asset reference protection -------------------------------------------

func TestWorkspaceAssetReferenceProtection(t *testing.T) {
	ts := newWorkspaceServer(t)
	wsID := ts.createWorkspace(t, "refs")

	assetID := ts.uploadAsset(t, "protected.txt", []byte("protected content"))
	rec := ts.commit(t, wsID, "req-ref", "device-1", "s", "", []commitOp{
		{"kind": "put", "path": "protected.txt", "asset_id": assetID},
	})
	requireStatus(t, rec, http.StatusOK)

	// Single delete is rejected with 409.
	rec = ts.do(t, http.MethodDelete, "/v1/assets/"+assetID, nil, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("single delete status=%d want 409 body=%s", rec.Code, rec.Body.String())
	}

	// Bulk delete reports the referenced item as not deleted.
	rec = ts.doJSON(t, http.MethodPost, "/v1/assets/bulk-delete", map[string]any{"ids": []string{assetID}}, nil)
	body := requireStatus(t, rec, http.StatusOK)
	results := body["data"].([]any)
	item := results[0].(map[string]any)
	if item["deleted"] != false || !strings.Contains(fmt.Sprint(item["error"]), "asset_referenced") {
		t.Fatalf("bulk delete item = %#v want deleted=false asset_referenced", item)
	}

	// Content is still downloadable.
	rec = ts.do(t, http.MethodGet, "/v1/assets/"+assetID+"/content", nil, nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "protected content" {
		t.Fatalf("content after protected delete status=%d body=%q", rec.Code, rec.Body.String())
	}

	// An unreferenced asset still deletes normally.
	orphan := ts.uploadAsset(t, "orphan.txt", []byte("orphan"))
	rec = ts.do(t, http.MethodDelete, "/v1/assets/"+orphan, nil, nil)
	requireStatus(t, rec, http.StatusOK)
}

// --- SEC-03: hard limits -------------------------------------------------------------

func TestWorkspaceLimits(t *testing.T) {
	ts := newWorkspaceServer(t)
	wsID := ts.createWorkspace(t, "limits")
	asset := ts.uploadAsset(t, "l.txt", []byte("limit content"))

	// Missing Idempotency-Key.
	rec := ts.doJSON(t, http.MethodPost, "/v1/workspaces/"+wsID+"/commits", map[string]any{
		"device_id": "d", "operations": []commitOp{{"kind": "put", "path": "a", "asset_id": asset}},
	}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing idempotency key status=%d want 400", rec.Code)
	}

	// Too many operations (>1000).
	ops := make([]commitOp, 0, 1001)
	for i := 0; i < 1001; i++ {
		ops = append(ops, commitOp{"kind": "put", "path": fmt.Sprintf("f%04d.txt", i), "asset_id": asset})
	}
	rec = ts.commit(t, wsID, "req-many", "device-1", "s", "", ops)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("ops limit status=%d want 422 body=%s", rec.Code, rec.Body.String())
	}

	// Overlong note.
	rec = ts.commit(t, wsID, "req-note", "device-1", "s", strings.Repeat("n", 2000), []commitOp{
		{"kind": "put", "path": "a.txt", "asset_id": asset},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("note limit status=%d want 422", rec.Code)
	}

	// Overlong path.
	rec = ts.commit(t, wsID, "req-path", "device-1", "s", "", []commitOp{
		{"kind": "put", "path": strings.Repeat("p", 300) + ".txt", "asset_id": asset},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("segment limit status=%d want 422", rec.Code)
	}

	// Dangerous paths are rejected (SEC-01 server side).
	for _, bad := range []string{"../etc/passwd", "/abs.txt", "a/./b", ".git/config", ".env", "nested/.saker-sync/state.json"} {
		rec = ts.commit(t, wsID, "req-bad-"+bad, "device-1", "s", "", []commitOp{
			{"kind": "put", "path": bad, "asset_id": asset},
		})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("path %q status=%d want 422 body=%s", bad, rec.Code, rec.Body.String())
		}
	}

	// Oversized commit body (>2MiB) yields 413.
	bigNote := strings.Repeat("x", 3*1024*1024)
	rec = ts.commit(t, wsID, "req-big", "device-1", "s", bigNote, []commitOp{
		{"kind": "put", "path": "a.txt", "asset_id": asset},
	})
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("body limit status=%d want 413", rec.Code)
	}

	// Pagination limit clamps to 500 (no amplification).
	rec = ts.do(t, http.MethodGet, "/v1/workspaces/"+wsID+"/changes?limit=100000", nil, nil)
	requireStatus(t, rec, http.StatusOK)
}

// --- SEC-04 / SEC-05: shares ----------------------------------------------------------

func TestWorkspaceSharesAndPublicAccess(t *testing.T) {
	ts := newWorkspaceServer(t)
	wsID := ts.createWorkspace(t, "shares")
	asset := ts.uploadAsset(t, "shared.txt", []byte("shared content"))
	rec := ts.commit(t, wsID, "req-share", "device-1", "s", "", []commitOp{
		{"kind": "put", "path": "shared.txt", "asset_id": asset},
	})
	requireStatus(t, rec, http.StatusOK)

	// Create: token returned exactly once.
	rec = ts.doJSON(t, http.MethodPost, "/v1/workspaces/"+wsID+"/shares", map[string]string{"path": "shared.txt"}, nil)
	created := requireStatus(t, rec, http.StatusOK)
	token, _ := created["token"].(string)
	if len(token) != 43 {
		t.Fatalf("share token len=%d want 43 (32 bytes base64url)", len(token))
	}
	shareID := created["id"].(string)

	// List: only hint, never the token.
	rec = ts.do(t, http.MethodGet, "/v1/workspaces/"+wsID+"/shares", nil, nil)
	body := requireStatus(t, rec, http.StatusOK)
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), token) {
		t.Fatal("share list leaks the full token")
	}
	items := body["data"].([]any)
	if len(items) != 1 {
		t.Fatalf("shares len=%d want 1", len(items))
	}

	// Public access: anonymous, safe headers.
	req := httptest.NewRequest(http.MethodGet, "/s/"+token, nil)
	rec = httptest.NewRecorder()
	ts.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("public share status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "shared content" {
		t.Fatalf("public share body=%q", rec.Body.String())
	}
	if csp := rec.Header().Get("Content-Security-Policy"); csp != "sandbox" {
		t.Fatalf("CSP=%q want sandbox", csp)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing nosniff")
	}
	if strings.Contains(rec.Header().Get("Set-Cookie"), "=") {
		t.Fatal("public share sets cookies")
	}
	if disp := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(disp, "attachment") {
		t.Fatalf("Content-Disposition=%q want attachment by default", disp)
	}

	// HTML content must never be served inline.
	htmlAsset := ts.uploadAsset(t, "page.html", []byte("<script>alert(1)</script>"))
	rec = ts.commit(t, wsID, "req-share-html", "device-1", "s", "", []commitOp{
		{"kind": "put", "path": "page.html", "asset_id": htmlAsset},
	})
	requireStatus(t, rec, http.StatusOK)
	rec = ts.doJSON(t, http.MethodPost, "/v1/workspaces/"+wsID+"/shares", map[string]string{"path": "page.html"}, nil)
	htmlShare := requireStatus(t, rec, http.StatusOK)
	htmlToken := htmlShare["token"].(string)
	req = httptest.NewRequest(http.MethodGet, "/s/"+htmlToken+"?inline=1", nil)
	rec = httptest.NewRecorder()
	ts.router.ServeHTTP(rec, req)
	if disp := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(disp, "attachment") {
		t.Fatalf("HTML inline disposition=%q want attachment", disp)
	}

	// Revoke by share ID: immediate.
	rec = ts.do(t, http.MethodDelete, "/v1/workspaces/"+wsID+"/shares/"+shareID, nil, nil)
	requireStatus(t, rec, http.StatusOK)
	req = httptest.NewRequest(http.MethodGet, "/s/"+token, nil)
	rec = httptest.NewRecorder()
	ts.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("revoked share status=%d want 404", rec.Code)
	}

	// Invalid tokens are indistinguishable (404).
	req = httptest.NewRequest(http.MethodGet, "/s/"+strings.Repeat("A", 43), nil)
	rec = httptest.NewRecorder()
	ts.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("invalid token status=%d want 404", rec.Code)
	}
}

// --- Read events ------------------------------------------------------------------

func TestWorkspaceReadEventsAndStats(t *testing.T) {
	ts := newWorkspaceServer(t)
	wsID := ts.createWorkspace(t, "reads")
	asset := ts.uploadAsset(t, "r.txt", []byte("read me"))
	rec := ts.commit(t, wsID, "req-read", "device-1", "s", "", []commitOp{
		{"kind": "put", "path": "r.txt", "asset_id": asset},
	})
	requireStatus(t, rec, http.StatusOK)

	events := map[string]any{"events": []map[string]any{
		{"path": "r.txt", "kind": "agent", "session_id": "sess-1", "device_id": "device-1", "count": 3},
		{"path": "r.txt", "kind": "human", "session_id": "sess-2", "device_id": "device-1", "count": 1},
	}}
	rec = ts.doJSON(t, http.MethodPost, "/v1/workspaces/"+wsID+"/read-events", events, nil)
	requireStatus(t, rec, http.StatusOK)

	rec = ts.do(t, http.MethodGet, "/v1/workspaces/"+wsID+"/read-stats?days=7", nil, nil)
	body := requireStatus(t, rec, http.StatusOK)
	data := body["data"].([]any)
	// Stats aggregate by (path, day); both events share path and day.
	if len(data) != 1 {
		t.Fatalf("stats rows=%d want 1", len(data))
	}
	row := data[0].(map[string]any)
	if row["count"].(float64) != 4 {
		t.Fatalf("stats count=%v want 4", row["count"])
	}
	// Stats must not expose actor identity.
	if _, hasActor := row["actor_id"]; hasActor {
		t.Fatal("read stats expose actor identity")
	}

	// Invalid kind rejected.
	rec = ts.doJSON(t, http.MethodPost, "/v1/workspaces/"+wsID+"/read-events", map[string]any{
		"events": []map[string]any{{"path": "r.txt", "kind": "robot", "count": 1}},
	}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid kind status=%d want 400", rec.Code)
	}

	// Batch limit (SEC-03).
	tooMany := make([]map[string]any, 0, 1001)
	for i := 0; i < 1001; i++ {
		tooMany = append(tooMany, map[string]any{"path": "r.txt", "kind": "agent", "count": 1})
	}
	rec = ts.doJSON(t, http.MethodPost, "/v1/workspaces/"+wsID+"/read-events", map[string]any{"events": tooMany}, nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("read event batch limit status=%d want 422", rec.Code)
	}
}

// --- SEC-06: log hygiene -------------------------------------------------------------

func TestWorkspaceLogsDoNotLeakSecrets(t *testing.T) {
	var buf bytes.Buffer
	original := slog.Default()
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(original) })

	ts := newWorkspaceServer(t)
	wsID := ts.createWorkspace(t, "logcheck")
	asset := ts.uploadAsset(t, "s.txt", []byte("secret content"))
	rec := ts.commit(t, wsID, "req-secret-key", "device-9", "session-secret", "", []commitOp{
		{"kind": "put", "path": "s.txt", "asset_id": asset},
	})
	requireStatus(t, rec, http.StatusOK)
	rec = ts.doJSON(t, http.MethodPost, "/v1/workspaces/"+wsID+"/shares", map[string]string{"path": "s.txt"}, nil)
	created := requireStatus(t, rec, http.StatusOK)
	token := created["token"].(string)
	req := httptest.NewRequest(http.MethodGet, "/s/"+token, nil)
	rec = httptest.NewRecorder()
	ts.router.ServeHTTP(rec, req)

	logged := buf.String()
	for _, secret := range []string{token, "secret content", "test-key"} {
		if strings.Contains(logged, secret) {
			t.Fatalf("log output leaks secret %q", secret)
		}
	}
}

// --- Asset preview (server-rendered office → PDF) ------------------------------------

func TestPreviewNonOfficeReturns404(t *testing.T) {
	ts := newWorkspaceServer(t)
	assetID := ts.uploadAsset(t, "note.txt", []byte("plain text, not previewable"))
	rec := ts.do(t, http.MethodGet, "/v1/assets/"+assetID+"/preview", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("preview of non-office asset status=%d want 404 body=%s", rec.Code, rec.Body.String())
	}
}

func TestPreviewMissingAssetReturns404(t *testing.T) {
	ts := newWorkspaceServer(t)
	rec := ts.do(t, http.MethodGet, "/v1/assets/asset-does-not-exist/preview", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("preview of missing asset status=%d want 404", rec.Code)
	}
}

// --- Regression guard: unconfigured deployments ----------------------------------------

func TestWorkspacesDisabledLeavesLegacyBehavior(t *testing.T) {
	cfg := config.Defaults()
	cfg.APIKeyAuthEnabled = true
	cfg.APIKeys = []string{"test-key"}
	cfg.WebEnabled = false
	// Workspaces.Enabled stays false.
	ts := newWorkspaceServerWithConfig(t, cfg)
	rec := ts.do(t, http.MethodGet, "/v1/workspaces", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unmounted workspaces status=%d want 404", rec.Code)
	}
	rec = ts.do(t, http.MethodGet, "/healthz", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz status=%d", rec.Code)
	}
}
