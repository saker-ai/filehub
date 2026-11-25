package api

import (
	"net/http"
	"net/url"
	"testing"
)

func TestWorkspaceExplicitDirectories(t *testing.T) {
	ts := newWorkspaceServer(t)
	id := ts.createWorkspace(t, "directory tree")
	base := "/v1/workspaces/" + id
	empty := base + "/directories?path=" + url.QueryEscape("projects/empty")
	for range 2 {
		body := requireStatus(t, ts.do(t, http.MethodPut, empty, nil, nil), http.StatusOK)
		if body["path"] != "projects/empty" || body["type"] != "directory" {
			t.Fatalf("directory response: %v", body)
		}
	}
	asset := ts.uploadAsset(t, "notes.txt", []byte("directory contents"))
	put := func(requestID, path string, status int) {
		t.Helper()
		requireStatus(t, ts.commit(t, id, requestID, "device", "session", "",
			[]commitOp{{"kind": "put", "path": path, "asset_id": asset}}), status)
	}
	put("add-file", "projects/notes.txt", http.StatusOK)
	put("replace-directory", "projects/empty", http.StatusConflict)
	put("replace-implicit-parent", "projects", http.StatusConflict)
	underFile := base + "/directories?path=" + url.QueryEscape("projects/notes.txt/folder")
	requireStatus(t, ts.do(t, http.MethodPut, underFile, nil, nil), http.StatusConflict)

	tree := base + "/tree?prefix=projects%2F&include_directories=true&limit=1"
	first := requireStatus(t, ts.do(t, http.MethodGet, tree, nil, nil), http.StatusOK)
	directory := first["data"].([]any)[0].(map[string]any)
	if directory["type"] != "directory" || directory["path"] != "projects/empty" || first["has_more"] != true {
		t.Fatalf("directory tree first page: %v", first)
	}
	next := tree + "&cursor=" + url.QueryEscape(first["next_cursor"].(string))
	second := requireStatus(t, ts.do(t, http.MethodGet, next, nil, nil), http.StatusOK)
	file := second["data"].([]any)[0].(map[string]any)
	if file["path"] != "projects/notes.txt" || file["revision"].(map[string]any)["asset_id"] != asset || second["has_more"] != false {
		t.Fatalf("directory tree second page: %v", second)
	}
	legacy := requireStatus(t, ts.do(t, http.MethodGet, base+"/tree", nil, nil), http.StatusOK)
	if len(legacy["data"].([]any)) != 1 {
		t.Fatalf("legacy file tree: %v", legacy)
	}
	requireStatus(t, ts.do(t, http.MethodDelete, base+"/directories?path=projects", nil, nil), http.StatusConflict)
	for range 2 {
		rec := ts.do(t, http.MethodDelete, empty, nil, nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("delete empty directory: %d %s", rec.Code, rec.Body)
		}
	}
	put("reuse-former-directory", "projects/empty", http.StatusOK)
}

func TestWorkspaceDirectoriesUseAuthenticatedTenant(t *testing.T) {
	ts := newJWTWorkspaceServer(t)
	owner := ts.signToken(t, "owner", "owner-agent")
	other := ts.signToken(t, "other", "other-agent")
	created := requireStatus(t, ts.doJWT(t, http.MethodPost, "/v1/workspaces",
		map[string]string{"name": "private directory tree"}, owner), http.StatusOK)
	base := "/v1/workspaces/" + created["id"].(string)
	path := base + "/directories?path=private"
	requireStatus(t, ts.doJWT(t, http.MethodPut, path, nil, owner), http.StatusOK)
	requireStatus(t, ts.doJWT(t, http.MethodPut, path, nil, other), http.StatusNotFound)
	requireStatus(t, ts.doJWT(t, http.MethodDelete, path, nil, other), http.StatusNotFound)
	requireStatus(t, ts.doJWT(t, http.MethodGet, base+"/tree?include_directories=true", nil, other), http.StatusNotFound)
	tree := requireStatus(t, ts.doJWT(t, http.MethodGet, base+"/tree?include_directories=true", nil, owner), http.StatusOK)
	if len(tree["data"].([]any)) != 1 {
		t.Fatalf("directory tree after cross-tenant attempts: %v", tree)
	}
}
