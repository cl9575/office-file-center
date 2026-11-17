package api_test

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lanfile/internal/api"
	"lanfile/internal/audit"
	"lanfile/internal/auth"
	"lanfile/internal/backup"
	"lanfile/internal/certsetup"
	"lanfile/internal/config"
	"lanfile/internal/db"
	"lanfile/internal/files"
	"lanfile/internal/model"
	"lanfile/internal/settings"
	"lanfile/internal/storage"
)

type env struct {
	t     *testing.T
	ts    *httptest.Server
	app   *api.Server
	dir   string
	admin *http.Client
	db    *sql.DB
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{DataDir: dir, Addr: ":0", HTTP: true, Version: "test"}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	store := storage.New(cfg.ObjectsDir(), cfg.TempDir())
	auditSvc := &audit.Service{DB: database}
	settingsSvc := &settings.Service{DB: database}
	authSvc := auth.New(database)
	filesSvc := files.New(database, store, auditSvc, settingsSvc)
	backupSvc := backup.New(database, cfg.DBPath(), cfg.DataDir, store, settingsSvc, auditSvc)
	app := api.New(cfg, database, authSvc, filesSvc, auditSvc, settingsSvc, backupSvc,
		certsetup.Info{}, false, "", "", "test")
	ts := httptest.NewServer(app.Handler())
	jar, _ := cookiejar.New(nil)
	e := &env{t: t, ts: ts, app: app, dir: dir, admin: &http.Client{Jar: jar}, db: database}
	t.Cleanup(func() {
		ts.Close()
		database.Close()
	})
	return e
}

func newClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

func (e *env) req(client *http.Client, method, path string, payload any) (int, []byte) {
	e.t.Helper()
	var reader io.Reader
	if payload != nil {
		body, err := json.Marshal(payload)
		if err != nil {
			e.t.Fatal(err)
		}
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, e.ts.URL+path, reader)
	if err != nil {
		e.t.Fatal(err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		e.t.Fatal(err)
	}
	return res.StatusCode, body
}

func (e *env) setupAdmin() {
	e.t.Helper()
	code, body := e.req(e.admin, "POST", "/api/setup/admin", map[string]string{
		"username": "admin", "display_name": "管理员", "password": "admin-pass-123",
	})
	if code != http.StatusCreated {
		e.t.Fatalf("setup failed: %d %s", code, body)
	}
}

func decode[T any](t *testing.T, body []byte) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return value
}

type errorPayload struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (e *env) createFolder(client *http.Client, parent, name string) files.Node {
	e.t.Helper()
	code, body := e.req(client, "POST", "/api/nodes", map[string]string{"parent_id": parent, "name": name})
	if code != http.StatusCreated {
		e.t.Fatalf("create folder failed: %d %s", code, body)
	}
	return decode[files.Node](e.t, body)
}

type uploadResponse struct {
	UploadID  string        `json:"upload_id"`
	Node      files.Node    `json:"node"`
	Version   files.Version `json:"version"`
	Duplicate bool          `json:"duplicate"`
}

func (e *env) uploadRaw(client *http.Client, uploadID, name, content, parent, nodeID, base string) (int, []byte) {
	e.t.Helper()
	q := url.Values{}
	q.Set("upload_id", uploadID)
	q.Set("name", name)
	q.Set("note", "test note")
	if parent != "" {
		q.Set("parent", parent)
	}
	if nodeID != "" {
		q.Set("node", nodeID)
	}
	if base != "" {
		q.Set("base", base)
	}
	req, err := http.NewRequest("PUT", e.ts.URL+"/api/upload?"+q.Encode(), strings.NewReader(content))
	if err != nil {
		e.t.Fatal(err)
	}
	req.ContentLength = int64(len(content))
	res, err := client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		e.t.Fatal(err)
	}
	return res.StatusCode, body
}

func (e *env) upload(client *http.Client, name, content, parent, nodeID, base string) (int, []byte) {
	return e.uploadRaw(client, model.NewID(), name, content, parent, nodeID, base)
}

func (e *env) download(client *http.Client, nodeID, versionID string) (int, []byte) {
	e.t.Helper()
	path := "/api/nodes/" + nodeID + "/download"
	if versionID != "" {
		path += "?version=" + url.QueryEscape(versionID)
	}
	code, body := e.req(client, "GET", path, nil)
	return code, body
}

func TestFileVersionLifecycle(t *testing.T) {
	e := newEnv(t)
	e.setupAdmin()
	folder := e.createFolder(e.admin, "", "合同")

	code, body := e.upload(e.admin, "报价.txt", "version-one", folder.ID, "", "")
	if code != http.StatusOK {
		t.Fatalf("upload v1: %d %s", code, body)
	}
	v1 := decode[uploadResponse](t, body)
	if v1.Node.VersionNo != 1 || v1.Node.Size != int64(len("version-one")) {
		t.Fatalf("unexpected v1: %+v", v1.Node)
	}
	if code, body := e.download(e.admin, v1.Node.ID, ""); code != 200 || string(body) != "version-one" {
		t.Fatalf("download v1 failed: %d %q", code, body)
	}

	code, body = e.upload(e.admin, "报价.txt", "version-two", folder.ID, v1.Node.ID, v1.Node.CurrentVersionID)
	if code != 200 {
		t.Fatalf("upload v2: %d %s", code, body)
	}
	v2 := decode[uploadResponse](t, body)
	if v2.Node.VersionNo != 2 {
		t.Fatalf("expected version 2, got %+v", v2.Node)
	}
	if code, body := e.download(e.admin, v2.Node.ID, v1.Version.ID); code != 200 || string(body) != "version-one" {
		t.Fatalf("download old version failed: %d %q", code, body)
	}

	code, body = e.upload(e.admin, "报价.txt", "stale", folder.ID, v1.Node.ID, v1.Node.CurrentVersionID)
	if code != http.StatusConflict {
		t.Fatalf("expected stale conflict, got %d %s", code, body)
	}
	if got := decode[errorPayload](t, body).Error.Code; got != "VERSION_CONFLICT" {
		t.Fatalf("expected VERSION_CONFLICT, got %s", got)
	}

	code, body = e.req(e.admin, "POST", "/api/nodes/"+v2.Node.ID+"/restore-version", map[string]string{
		"version_id": v1.Version.ID, "base_version_id": v2.Node.CurrentVersionID,
	})
	if code != 200 {
		t.Fatalf("restore: %d %s", code, body)
	}
	restored := decode[uploadResponse](t, body)
	if restored.Node.VersionNo != 3 || restored.Node.CurrentVersionID == "" {
		t.Fatalf("unexpected restored node: %+v", restored.Node)
	}
	if code, body := e.download(e.admin, restored.Node.ID, ""); code != 200 || string(body) != "version-one" {
		t.Fatalf("restored content mismatch: %d %q", code, body)
	}

	code, body = e.req(e.admin, "PATCH", "/api/nodes/"+restored.Node.ID, map[string]string{"name": "报价-最终.txt"})
	if code != 200 {
		t.Fatalf("rename: %d %s", code, body)
	}
	renamed := decode[files.Node](t, body)
	if renamed.Name != "报价-最终.txt" {
		t.Fatalf("rename failed: %+v", renamed)
	}

	code, body = e.req(e.admin, "PATCH", "/api/nodes/"+restored.Node.ID, map[string]string{"parent_id": ""})
	if code != 200 {
		t.Fatalf("move: %d %s", code, body)
	}
	moved := decode[files.Node](t, body)
	if moved.ParentID != "" || moved.Path != "/报价-最终.txt" {
		t.Fatalf("move failed: %+v", moved)
	}

	code, body = e.req(e.admin, "GET", "/api/nodes?q="+url.QueryEscape("最终"), nil)
	if code != 200 {
		t.Fatalf("search: %d %s", code, body)
	}
	search := decode[struct {
		Items []files.Node `json:"items"`
	}](t, body)
	if len(search.Items) != 1 || search.Items[0].ID != restored.Node.ID {
		t.Fatalf("search mismatch: %+v", search.Items)
	}

	code, body = e.req(e.admin, "GET", "/api/nodes/"+restored.Node.ID+"/versions", nil)
	if code != 200 {
		t.Fatalf("versions: %d %s", code, body)
	}
	versions := decode[struct {
		Items []files.Version `json:"items"`
	}](t, body)
	if len(versions.Items) != 3 || !versions.Items[0].IsCurrent {
		t.Fatalf("version list mismatch: %+v", versions.Items)
	}

	code, body = e.req(e.admin, "GET", "/api/nodes/"+restored.Node.ID+"/audit", nil)
	if code != 200 {
		t.Fatalf("node audit: %d %s", code, body)
	}
	nodeAudit := decode[struct {
		Items []audit.Row `json:"items"`
	}](t, body)
	if len(nodeAudit.Items) < 3 {
		t.Fatalf("expected audit entries, got %d", len(nodeAudit.Items))
	}

	code, body = e.req(e.admin, "GET", "/api/admin/audit?page=1&page_size=20", nil)
	if code != 200 {
		t.Fatalf("admin audit: %d %s", code, body)
	}
	adminAudit := decode[struct {
		Items []audit.Row `json:"items"`
		Total int         `json:"total"`
	}](t, body)
	if adminAudit.Total == 0 || len(adminAudit.Items) == 0 {
		t.Fatalf("admin audit empty")
	}

	code, body = e.req(e.admin, "GET", "/api/admin/audit.csv", nil)
	if code != 200 {
		t.Fatalf("audit csv: %d %s", code, body)
	}
	if !bytes.HasPrefix(body, []byte{0xEF, 0xBB, 0xBF}) || !bytes.Contains(body, []byte("操作人")) {
		t.Fatalf("audit csv missing BOM or header")
	}
}

func TestUploadIdempotencyAndValidation(t *testing.T) {
	e := newEnv(t)
	e.setupAdmin()
	id := model.NewID()
	code, body := e.uploadRaw(e.admin, id, "幂等.txt", "hello", "", "", "")
	if code != 200 {
		t.Fatalf("first upload: %d %s", code, body)
	}
	code, body = e.uploadRaw(e.admin, id, "幂等.txt", "hello", "", "", "")
	if code != 200 {
		t.Fatalf("duplicate upload should return stored result: %d %s", code, body)
	}
	if !decode[uploadResponse](t, body).Duplicate {
		t.Fatalf("expected duplicate result: %s", body)
	}

	code, body = e.upload(e.admin, "../evil.txt", "x", "", "", "")
	if code != http.StatusBadRequest || decode[errorPayload](t, body).Error.Code != "INVALID_NAME" {
		t.Fatalf("path traversal name should fail: %d %s", code, body)
	}

	root := e.createFolder(e.admin, "", "根")
	child := e.createFolder(e.admin, root.ID, "子目录")
	code, body = e.req(e.admin, "PATCH", "/api/nodes/"+root.ID, map[string]string{"parent_id": child.ID})
	if code != http.StatusBadRequest {
		t.Fatalf("moving folder into child should fail: %d %s", code, body)
	}
}

func TestUserRolesAndMustChangePassword(t *testing.T) {
	e := newEnv(t)
	e.setupAdmin()
	code, body := e.req(e.admin, "POST", "/api/admin/users", map[string]string{
		"username": "viewer1", "display_name": "只读同事", "password": "viewer-pass-123", "role": "viewer",
	})
	if code != http.StatusCreated {
		t.Fatalf("create viewer: %d %s", code, body)
	}
	viewerUser := decode[model.User](t, body)

	viewer := newClient()
	code, body = e.req(viewer, "POST", "/api/auth/login", map[string]string{
		"username": "viewer1", "password": "viewer-pass-123",
	})
	if code != 200 {
		t.Fatalf("viewer login: %d %s", code, body)
	}
	login := decode[struct {
		User model.User `json:"user"`
	}](t, body)
	if !login.User.MustChangePassword {
		t.Fatalf("new user must change password")
	}

	code, body = e.req(viewer, "PUT", "/api/upload?upload_id="+model.NewID()+"&name=x.txt", nil)
	if code != http.StatusForbidden {
		t.Fatalf("upload before password change should be blocked, got %d %s", code, body)
	}

	code, body = e.req(viewer, "POST", "/api/auth/change-password", map[string]string{
		"old_password": "viewer-pass-123", "new_password": "viewer-new-pass-123",
	})
	if code != 200 {
		t.Fatalf("change password: %d %s", code, body)
	}

	code, body = e.req(viewer, "GET", "/api/nodes", nil)
	if code != 200 {
		t.Fatalf("viewer list: %d %s", code, body)
	}
	code, body = e.upload(viewer, "禁止.txt", "x", "", "", "")
	if code != http.StatusForbidden {
		t.Fatalf("viewer upload should be forbidden, got %d %s", code, body)
	}
	code, body = e.req(viewer, "POST", "/api/nodes", map[string]string{"parent_id": "", "name": "禁止目录"})
	if code != http.StatusForbidden {
		t.Fatalf("viewer folder create should be forbidden, got %d %s", code, body)
	}
	code, body = e.req(viewer, "GET", "/api/admin/users", nil)
	if code != http.StatusForbidden {
		t.Fatalf("viewer admin api should be forbidden, got %d %s", code, body)
	}

	code, body = e.req(e.admin, "PATCH", "/api/admin/users/"+viewerUser.ID, map[string]any{
		"display_name": "只读同事", "role": "editor", "disabled": false,
	})
	if code != 200 {
		t.Fatalf("promote viewer: %d %s", code, body)
	}
	code, body = e.createFolderAPI(viewer, "", "编辑者可建目录")
	if code != http.StatusCreated {
		t.Fatalf("editor folder create failed: %d %s", code, body)
	}
}

func (e *env) createFolderAPI(client *http.Client, parent, name string) (int, []byte) {
	return e.req(client, "POST", "/api/nodes", map[string]string{"parent_id": parent, "name": name})
}

func TestOwnerDeletesUserKeepsHistory(t *testing.T) {
	e := newEnv(t)
	e.setupAdmin()

	code, body := e.req(e.admin, "GET", "/api/admin/users", nil)
	if code != http.StatusOK {
		t.Fatalf("list users: %d %s", code, body)
	}
	list := decode[struct {
		Items []model.User `json:"items"`
	}](t, body)
	owner := list.Items[0]

	code, body = e.req(e.admin, "POST", "/api/admin/users", map[string]string{
		"username": "deletee", "display_name": "待删同事", "password": "deletee-pass-123", "role": "editor",
	})
	if code != http.StatusCreated {
		t.Fatalf("create editor: %d %s", code, body)
	}
	editorUser := decode[model.User](t, body)
	editor := newClient()
	code, body = e.req(editor, "POST", "/api/auth/login", map[string]string{
		"username": "deletee", "password": "deletee-pass-123",
	})
	if code != http.StatusOK {
		t.Fatalf("editor login: %d %s", code, body)
	}
	code, body = e.req(editor, "POST", "/api/auth/change-password", map[string]string{
		"old_password": "deletee-pass-123", "new_password": "deletee-new-pass-123",
	})
	if code != http.StatusOK {
		t.Fatalf("editor change password: %d %s", code, body)
	}
	code, body = e.upload(editor, "历史文件.txt", "history-content", "", "", "")
	if code != http.StatusOK {
		t.Fatalf("editor upload: %d %s", code, body)
	}
	uploaded := decode[uploadResponse](t, body)

	code, body = e.req(e.admin, "POST", "/api/admin/users", map[string]string{
		"username": "admin2", "display_name": "第二管理员", "password": "admin2-pass-123", "role": "admin",
	})
	if code != http.StatusCreated {
		t.Fatalf("create second admin: %d %s", code, body)
	}
	admin2 := newClient()
	code, body = e.req(admin2, "POST", "/api/auth/login", map[string]string{
		"username": "admin2", "password": "admin2-pass-123",
	})
	if code != http.StatusOK {
		t.Fatalf("second admin login: %d %s", code, body)
	}
	code, body = e.req(admin2, "POST", "/api/auth/change-password", map[string]string{
		"old_password": "admin2-pass-123", "new_password": "admin2-new-pass-123",
	})
	if code != http.StatusOK {
		t.Fatalf("second admin change password: %d %s", code, body)
	}

	code, body = e.req(admin2, "DELETE", "/api/admin/users/"+editorUser.ID, nil)
	if code != http.StatusForbidden {
		t.Fatalf("non-owner deleting user should be forbidden: %d %s", code, body)
	}
	code, body = e.req(admin2, "DELETE", "/api/admin/users/"+owner.ID, nil)
	if code != http.StatusForbidden {
		t.Fatalf("deleting owner should be forbidden: %d %s", code, body)
	}
	code, body = e.req(e.admin, "DELETE", "/api/admin/users/"+owner.ID, nil)
	if code != http.StatusForbidden {
		t.Fatalf("owner deleting self should be forbidden: %d %s", code, body)
	}

	code, body = e.req(e.admin, "DELETE", "/api/admin/users/"+editorUser.ID, nil)
	if code != http.StatusOK {
		t.Fatalf("owner deleting user: %d %s", code, body)
	}
	code, body = e.req(e.admin, "GET", "/api/admin/users", nil)
	after := decode[struct {
		Items []model.User `json:"items"`
	}](t, body)
	for _, item := range after.Items {
		if item.ID == editorUser.ID {
			t.Fatalf("deleted user still listed: %+v", item)
		}
	}
	deletedLogin := newClient()
	code, body = e.req(deletedLogin, "POST", "/api/auth/login", map[string]string{
		"username": "deletee", "password": "deletee-new-pass-123",
	})
	if code != http.StatusUnauthorized {
		t.Fatalf("deleted user should not log in: %d %s", code, body)
	}

	code, body = e.req(e.admin, "GET", "/api/nodes/"+uploaded.Node.ID+"/versions", nil)
	versions := decode[struct {
		Items []files.Version `json:"items"`
	}](t, body)
	if code != http.StatusOK || len(versions.Items) != 1 || versions.Items[0].CreatedByName != "待删同事" {
		t.Fatalf("deleted user's upload history lost: %d %+v", code, versions.Items)
	}

	code, body = e.req(e.admin, "GET", "/api/admin/audit?action=user_delete", nil)
	auditPage := decode[struct {
		Total int         `json:"total"`
		Items []audit.Row `json:"items"`
	}](t, body)
	if code != http.StatusOK || auditPage.Total != 1 || len(auditPage.Items) != 1 {
		t.Fatalf("delete user audit missing: %d %+v", code, auditPage)
	}

	code, body = e.req(e.admin, "POST", "/api/admin/users", map[string]string{
		"username": "deletee", "display_name": "新同事", "password": "deletee-pass-456", "role": "viewer",
	})
	if code != http.StatusCreated {
		t.Fatalf("reusing deleted username should work: %d %s", code, body)
	}
}

func TestOwnerAccountProtection(t *testing.T) {
	e := newEnv(t)
	e.setupAdmin()

	code, body := e.req(e.admin, "GET", "/api/admin/users", nil)
	if code != http.StatusOK {
		t.Fatalf("list users: %d %s", code, body)
	}
	list := decode[struct {
		Items []model.User `json:"items"`
	}](t, body)
	if len(list.Items) != 1 || !list.Items[0].IsOwner {
		t.Fatalf("initial admin should be owner: %+v", list.Items)
	}
	owner := list.Items[0]

	code, body = e.req(e.admin, "POST", "/api/admin/users", map[string]string{
		"username": "admin2", "display_name": "第二管理员", "password": "admin2-pass-123", "role": "admin",
	})
	if code != http.StatusCreated {
		t.Fatalf("create second admin: %d %s", code, body)
	}
	admin2 := newClient()
	code, body = e.req(admin2, "POST", "/api/auth/login", map[string]string{
		"username": "admin2", "password": "admin2-pass-123",
	})
	if code != http.StatusOK {
		t.Fatalf("second admin login: %d %s", code, body)
	}
	code, body = e.req(admin2, "POST", "/api/auth/change-password", map[string]string{
		"old_password": "admin2-pass-123", "new_password": "admin2-new-pass-123",
	})
	if code != http.StatusOK {
		t.Fatalf("second admin change password: %d %s", code, body)
	}

	code, body = e.req(admin2, "PATCH", "/api/admin/users/"+owner.ID, map[string]any{
		"display_name": "被篡改", "role": "editor", "disabled": true,
	})
	if code != http.StatusForbidden {
		t.Fatalf("other admin editing owner should be forbidden: %d %s", code, body)
	}
	code, body = e.req(admin2, "POST", "/api/admin/users/"+owner.ID+"/reset-password", map[string]string{
		"password": "hacked-pass-123",
	})
	if code != http.StatusForbidden {
		t.Fatalf("other admin resetting owner password should be forbidden: %d %s", code, body)
	}

	code, body = e.req(e.admin, "PATCH", "/api/admin/users/"+owner.ID, map[string]any{
		"display_name": "管理员", "role": "editor", "disabled": false,
	})
	if code != http.StatusBadRequest {
		t.Fatalf("owner demoting self should fail: %d %s", code, body)
	}
	code, body = e.req(e.admin, "PATCH", "/api/admin/users/"+owner.ID, map[string]any{
		"display_name": "主机管理员", "role": "admin", "disabled": false,
	})
	if code != http.StatusOK {
		t.Fatalf("owner renaming self: %d %s", code, body)
	}
	updated := decode[model.User](t, body)
	if !updated.IsOwner || updated.DisplayName != "主机管理员" {
		t.Fatalf("owner update result: %+v", updated)
	}

	code, body = e.req(e.admin, "POST", "/api/admin/users", map[string]string{
		"username": "editor1", "display_name": "编辑同事", "password": "editor-pass-123", "role": "editor",
	})
	if code != http.StatusCreated {
		t.Fatalf("create editor: %d %s", code, body)
	}
	editor := decode[model.User](t, body)
	code, body = e.req(admin2, "PATCH", "/api/admin/users/"+editor.ID, map[string]any{
		"display_name": "编辑同事二", "role": "editor", "disabled": false,
	})
	if code != http.StatusOK {
		t.Fatalf("second admin updating normal user: %d %s", code, body)
	}
	code, body = e.req(e.admin, "GET", "/api/admin/users", nil)
	if code != http.StatusOK {
		t.Fatalf("list users after update: %d %s", code, body)
	}
	list = decode[struct {
		Items []model.User `json:"items"`
	}](t, body)
	if !list.Items[0].IsOwner || list.Items[0].ID != owner.ID {
		t.Fatalf("owner should remain first in list: %+v", list.Items)
	}
}

func TestRecycleRestoreAndPurge(t *testing.T) {
	e := newEnv(t)
	e.setupAdmin()
	folder := e.createFolder(e.admin, "", "待删除目录")
	code, body := e.upload(e.admin, "文件.txt", "content", folder.ID, "", "")
	if code != 200 {
		t.Fatalf("upload: %d %s", code, body)
	}
	up := decode[uploadResponse](t, body)

	code, body = e.req(e.admin, "DELETE", "/api/nodes/"+folder.ID, nil)
	if code != 200 {
		t.Fatalf("delete folder: %d %s", code, body)
	}
	if decode[struct {
		Deleted int `json:"deleted"`
	}](t, body).Deleted != 2 {
		t.Fatalf("expected 2 deleted nodes: %s", body)
	}
	code, body = e.req(e.admin, "GET", "/api/recycle", nil)
	if code != 200 {
		t.Fatalf("recycle: %d %s", code, body)
	}
	recycle := decode[struct {
		Items []files.Node `json:"items"`
	}](t, body)
	if len(recycle.Items) != 1 || recycle.Items[0].DeleteBatch == "" {
		t.Fatalf("unexpected recycle list: %+v", recycle.Items)
	}
	batch := recycle.Items[0].DeleteBatch

	code, body = e.req(e.admin, "POST", "/api/recycle/"+batch+"/restore", nil)
	if code != 200 {
		t.Fatalf("restore recycle: %d %s", code, body)
	}
	code, body = e.req(e.admin, "GET", "/api/nodes/"+up.Node.ID+"/download", nil)
	if code != 200 || string(body) != "content" {
		t.Fatalf("restored file download failed: %d %q", code, body)
	}

	code, body = e.req(e.admin, "DELETE", "/api/nodes/"+folder.ID, nil)
	if code != 200 {
		t.Fatalf("delete again: %d %s", code, body)
	}
	code, body = e.req(e.admin, "GET", "/api/recycle", nil)
	recycle = decode[struct {
		Items []files.Node `json:"items"`
	}](t, body)
	batch = recycle.Items[0].DeleteBatch
	code, body = e.req(e.admin, "DELETE", "/api/recycle/"+batch, nil)
	if code != 200 {
		t.Fatalf("purge: %d %s", code, body)
	}
	code, body = e.req(e.admin, "GET", "/api/recycle", nil)
	recycle = decode[struct {
		Items []files.Node `json:"items"`
	}](t, body)
	if len(recycle.Items) != 0 {
		t.Fatalf("recycle should be empty: %+v", recycle.Items)
	}
}

func TestRecyclePurgeAll(t *testing.T) {
	e := newEnv(t)
	e.setupAdmin()

	code, body := e.upload(e.admin, "保留.txt", "shared", "", "", "")
	if code != 200 {
		t.Fatalf("upload kept file: %d %s", code, body)
	}
	kept := decode[uploadResponse](t, body).Node

	folderA := e.createFolder(e.admin, "", "待删A")
	code, body = e.upload(e.admin, "A.txt", "a-content", folderA.ID, "", "")
	if code != 200 {
		t.Fatalf("upload A: %d %s", code, body)
	}
	folderB := e.createFolder(e.admin, "", "待删B")
	code, body = e.upload(e.admin, "B.txt", "shared", folderB.ID, "", "")
	if code != 200 {
		t.Fatalf("upload B: %d %s", code, body)
	}
	for _, folder := range []files.Node{folderA, folderB} {
		code, body = e.req(e.admin, "DELETE", "/api/nodes/"+folder.ID, nil)
		if code != 200 {
			t.Fatalf("delete %s: %d %s", folder.Name, code, body)
		}
	}
	code, body = e.req(e.admin, "GET", "/api/recycle", nil)
	recycle := decode[struct {
		Items []files.Node `json:"items"`
	}](t, body)
	if code != 200 || len(recycle.Items) != 2 {
		t.Fatalf("recycle before purge: %d %+v", code, recycle.Items)
	}

	code, body = e.req(e.admin, "DELETE", "/api/recycle", nil)
	if code != 200 {
		t.Fatalf("purge all: %d %s", code, body)
	}
	if got := decode[struct {
		Purged int `json:"purged"`
	}](t, body).Purged; got != 4 {
		t.Fatalf("expected 4 purged nodes, got %d", got)
	}
	code, body = e.req(e.admin, "GET", "/api/recycle", nil)
	recycle = decode[struct {
		Items []files.Node `json:"items"`
	}](t, body)
	if code != 200 || len(recycle.Items) != 0 {
		t.Fatalf("recycle should be empty: %d %+v", code, recycle.Items)
	}
	code, body = e.req(e.admin, "GET", "/api/admin/audit?action=recycle_purge_all", nil)
	auditPage := decode[struct {
		Total int         `json:"total"`
		Items []audit.Row `json:"items"`
	}](t, body)
	if code != 200 || auditPage.Total != 1 || len(auditPage.Items) != 1 || auditPage.Items[0].Detail != "清空回收站，共 4 项" {
		t.Fatalf("purge all audit missing: %d %+v", code, auditPage)
	}
	code, body = e.req(e.admin, "GET", "/api/nodes", nil)
	nodes := decode[struct {
		Items []files.Node `json:"items"`
	}](t, body)
	if code != 200 || len(nodes.Items) != 1 || nodes.Items[0].ID != kept.ID {
		t.Fatalf("active files should be kept: %d %+v", code, nodes.Items)
	}
	code, body = e.req(e.admin, "GET", "/api/nodes/"+kept.ID+"/download", nil)
	if code != 200 || string(body) != "shared" {
		t.Fatalf("kept file download failed: %d %q", code, body)
	}

	code, body = e.req(e.admin, "DELETE", "/api/recycle", nil)
	if code != 200 || decode[struct {
		Purged int `json:"purged"`
	}](t, body).Purged != 0 {
		t.Fatalf("purging empty recycle should succeed with 0: %d %s", code, body)
	}
}

func TestBackupAndRestore(t *testing.T) {
	e := newEnv(t)
	e.setupAdmin()
	folder := e.createFolder(e.admin, "", "合同")
	code, body := e.upload(e.admin, "备份文件.txt", "backup-content", folder.ID, "", "")
	if code != 200 {
		t.Fatalf("upload: %d %s", code, body)
	}
	up := decode[uploadResponse](t, body)
	code, body = e.upload(e.admin, "CON.txt", "reserved-name", "", "", "")
	if code != 200 {
		t.Fatalf("upload reserved name: %d %s", code, body)
	}

	certDir := filepath.Join(e.dir, "certs")
	if err := os.MkdirAll(certDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(certDir, "server.crt"), []byte("test-cert"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(certDir, "server.key"), []byte("test-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(certDir, "ca.crt"), []byte("test-ca-cert"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(certDir, "ca.key"), []byte("test-ca-key"), 0o600); err != nil {
		t.Fatal(err)
	}

	backupDir := t.TempDir()
	code, body = e.req(e.admin, "PUT", "/api/admin/settings", map[string]any{
		"backup_enabled": false,
		"backup_dir":     backupDir,
	})
	if code != 200 {
		t.Fatalf("update settings: %d %s", code, body)
	}
	inside := filepath.Join(e.dir, "bad-backup")
	code, body = e.req(e.admin, "PUT", "/api/admin/settings", map[string]any{"backup_dir": inside})
	if code != http.StatusBadRequest {
		t.Fatalf("backup inside data dir should be rejected: %d %s", code, body)
	}

	run, err := e.app.Backup.RunNow("test")
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if run.Status != "success" {
		t.Fatalf("backup not successful: %+v", run)
	}
	if _, err := os.Stat(filepath.Join(run.Path, "complete.json")); err != nil {
		t.Fatalf("missing complete marker: %v", err)
	}
	if _, err := os.Stat(filepath.Join(run.Path, "meta.db")); err != nil {
		t.Fatalf("missing snapshot db: %v", err)
	}
	if body, err := os.ReadFile(filepath.Join(run.Path, "files", "合同", "备份文件.txt")); err != nil || string(body) != "backup-content" {
		t.Fatalf("missing readable mirror file: %v %q", err, body)
	}
	if body, err := os.ReadFile(filepath.Join(run.Path, "files", "_CON.txt")); err != nil || string(body) != "reserved-name" {
		t.Fatalf("reserved-name mirror mismatch: %v %q", err, body)
	}
	var manifest backup.Manifest
	if body, err := os.ReadFile(filepath.Join(run.Path, "manifest.json")); err != nil || json.Unmarshal(body, &manifest) != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if manifest.MirrorFiles != 2 {
		t.Fatalf("unexpected mirror file count: %+v", manifest)
	}

	restoreDir := filepath.Join(t.TempDir(), "restored")
	if err := backup.Restore(run.Path, restoreDir); err != nil {
		t.Fatalf("restore: %v", err)
	}
	restored, err := db.Open(filepath.Join(restoreDir, "meta.db"))
	if err != nil {
		t.Fatalf("open restored db: %v", err)
	}
	defer restored.Close()
	var name string
	if err := restored.QueryRow(`SELECT name FROM nodes WHERE kind = 'file' AND name = '备份文件.txt'`).Scan(&name); err != nil {
		t.Fatalf("query restored node: %v", err)
	}
	if name != "备份文件.txt" {
		t.Fatalf("unexpected restored node: %s", name)
	}
	objPath := filepath.Join(restoreDir, "objects", up.Version.ObjectID[:2], up.Version.ObjectID)
	if body, err := os.ReadFile(objPath); err != nil || string(body) != "backup-content" {
		t.Fatalf("restored object mismatch: %v %q", err, body)
	}
	if body, err := os.ReadFile(filepath.Join(restoreDir, "certs", "server.crt")); err != nil || string(body) != "test-cert" {
		t.Fatalf("restored cert mismatch: %v %q", err, body)
	}
	if body, err := os.ReadFile(filepath.Join(restoreDir, "certs", "ca.crt")); err != nil || string(body) != "test-ca-cert" {
		t.Fatalf("restored ca cert mismatch: %v %q", err, body)
	}
	if body, err := os.ReadFile(filepath.Join(restoreDir, "certs", "ca.key")); err != nil || string(body) != "test-ca-key" {
		t.Fatalf("restored ca key mismatch: %v %q", err, body)
	}

	code, body = e.req(e.admin, "GET", "/api/admin/backups", nil)
	if code != 200 {
		t.Fatalf("list backups: %d %s", code, body)
	}
	backups := decode[struct {
		Items []backup.Run `json:"items"`
	}](t, body)
	if len(backups.Items) == 0 || backups.Items[0].Status != "success" {
		t.Fatalf("backup list mismatch: %+v", backups.Items)
	}
}

func TestFolderZipDownload(t *testing.T) {
	e := newEnv(t)
	e.setupAdmin()
	root := e.createFolder(e.admin, "", "合同")
	sub := e.createFolder(e.admin, root.ID, "子目录")
	e.createFolder(e.admin, root.ID, "空目录")

	code, body := e.upload(e.admin, "说明.txt", "root-content", root.ID, "", "")
	if code != 200 {
		t.Fatalf("upload root file: %d %s", code, body)
	}
	rootFile := decode[uploadResponse](t, body)
	code, body = e.upload(e.admin, "深层.txt", "deep-content", sub.ID, "", "")
	if code != 200 {
		t.Fatalf("upload nested file: %d %s", code, body)
	}

	code, body = e.req(e.admin, "GET", "/api/nodes/"+root.ID+"/download-folder", nil)
	if code != 200 {
		t.Fatalf("folder download: %d %s", code, body)
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	dirs := map[string]bool{}
	files := map[string]string{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			dirs[f.Name] = true
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open zip entry %s: %v", f.Name, err)
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read zip entry %s: %v", f.Name, err)
		}
		files[f.Name] = string(content)
	}
	if !dirs["合同/子目录/"] || !dirs["合同/空目录/"] {
		t.Fatalf("missing directory entries: %+v", dirs)
	}
	if files["合同/说明.txt"] != "root-content" {
		t.Fatalf("root file mismatch: %+v", files)
	}
	if files["合同/子目录/深层.txt"] != "deep-content" {
		t.Fatalf("nested file mismatch: %+v", files)
	}

	code, _ = e.req(e.admin, "GET", "/api/nodes/"+rootFile.Node.ID+"/download-folder", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("file should not be downloadable as folder: %d", code)
	}
}

func TestConcurrentVersionConflict(t *testing.T) {
	e := newEnv(t)
	e.setupAdmin()
	code, body := e.upload(e.admin, "并发.txt", "v1", "", "", "")
	if code != 200 {
		t.Fatalf("upload v1: %d %s", code, body)
	}
	v1 := decode[uploadResponse](t, body)

	results := make(chan int, 2)
	for _, content := range []string{"v2-a", "v2-b"} {
		content := content
		go func() {
			q := url.Values{}
			q.Set("upload_id", model.NewID())
			q.Set("name", "并发.txt")
			q.Set("node", v1.Node.ID)
			q.Set("base", v1.Node.CurrentVersionID)
			req, err := http.NewRequest("PUT", e.ts.URL+"/api/upload?"+q.Encode(), strings.NewReader(content))
			if err != nil {
				results <- 0
				return
			}
			req.ContentLength = int64(len(content))
			res, err := e.admin.Do(req)
			if err != nil {
				results <- 0
				return
			}
			defer res.Body.Close()
			_, _ = io.Copy(io.Discard, res.Body)
			results <- res.StatusCode
		}()
	}
	first, second := <-results, <-results
	success, conflict := 0, 0
	for _, status := range []int{first, second} {
		switch status {
		case http.StatusOK:
			success++
		case http.StatusConflict:
			conflict++
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("expected 1 success and 1 conflict, got %d and %d", success, conflict)
	}

	code, body = e.req(e.admin, "GET", "/api/nodes/"+v1.Node.ID+"/versions", nil)
	if code != 200 {
		t.Fatalf("versions: %d %s", code, body)
	}
	versions := decode[struct {
		Items []files.Version `json:"items"`
	}](t, body)
	if len(versions.Items) != 2 {
		t.Fatalf("expected exactly two versions, got %d", len(versions.Items))
	}
}
