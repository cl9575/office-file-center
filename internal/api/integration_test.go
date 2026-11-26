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

	"golang.org/x/text/encoding/simplifiedchinese"

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

func (e *env) uploadSecure(client *http.Client, name, content, parent, mode string, roles, users []string) (int, []byte) {
	e.t.Helper()
	q := url.Values{}
	q.Set("upload_id", model.NewID())
	q.Set("name", name)
	q.Set("note", "secure test")
	if parent != "" {
		q.Set("parent", parent)
	}
	q.Set("visibility_mode", mode)
	if len(roles) > 0 {
		q.Set("visibility_roles", strings.Join(roles, ","))
	}
	if len(users) > 0 {
		q.Set("visibility_users", strings.Join(users, ","))
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

func (e *env) download(client *http.Client, nodeID, versionID string) (int, []byte) {
	e.t.Helper()
	path := "/api/nodes/" + nodeID + "/download"
	if versionID != "" {
		path += "?version=" + url.QueryEscape(versionID)
	}
	code, body := e.req(client, "GET", path, nil)
	return code, body
}

func (e *env) preview(client *http.Client, nodeID, rangeHeader string) (*http.Response, []byte) {
	e.t.Helper()
	req, err := http.NewRequest("GET", e.ts.URL+"/api/nodes/"+nodeID+"/preview", nil)
	if err != nil {
		e.t.Fatal(err)
	}
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
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
	return res, body
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

	code, body = e.req(e.admin, "PATCH", "/api/nodes/"+restored.Node.ID, map[string]string{"name": "报价-最终.pdf"})
	if code != http.StatusBadRequest || decode[errorPayload](t, body).Error.Code != "EXTENSION_IMMUTABLE" {
		t.Fatalf("rename must keep file extension: %d %s", code, body)
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

func TestUserRolesAndFirstLoginSuggestion(t *testing.T) {
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
	if login.User.MustChangePassword {
		t.Fatalf("new user should not be forced to change password")
	}
	if !login.User.SuggestPasswordChange {
		t.Fatalf("new user should receive a password change suggestion")
	}

	code, body = e.req(viewer, "GET", "/api/nodes", nil)
	if code != http.StatusOK {
		t.Fatalf("new user should access the app before changing password: %d %s", code, body)
	}

	second := newClient()
	code, body = e.req(second, "POST", "/api/auth/login", map[string]string{
		"username": "viewer1", "password": "viewer-pass-123",
	})
	if code != http.StatusOK {
		t.Fatalf("viewer second login: %d %s", code, body)
	}
	secondLogin := decode[struct {
		User model.User `json:"user"`
	}](t, body)
	if secondLogin.User.SuggestPasswordChange || secondLogin.User.MustChangePassword {
		t.Fatalf("password suggestion should only appear on first login: %+v", secondLogin.User)
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

func TestAdminResetStillRequiresPasswordChange(t *testing.T) {
	e := newEnv(t)
	e.setupAdmin()

	code, body := e.req(e.admin, "POST", "/api/admin/users", map[string]string{
		"username": "resetuser", "display_name": "重置测试", "password": "reset-pass-123", "role": "editor",
	})
	if code != http.StatusCreated {
		t.Fatalf("create reset user: %d %s", code, body)
	}
	user := decode[model.User](t, body)

	first := newClient()
	code, body = e.req(first, "POST", "/api/auth/login", map[string]string{
		"username": "resetuser", "password": "reset-pass-123",
	})
	if code != http.StatusOK {
		t.Fatalf("reset user first login: %d %s", code, body)
	}

	code, body = e.req(e.admin, "POST", "/api/admin/users/"+user.ID+"/reset-password", map[string]string{
		"password": "reset-next-pass-123",
	})
	if code != http.StatusOK {
		t.Fatalf("admin reset password: %d %s", code, body)
	}

	afterReset := newClient()
	code, body = e.req(afterReset, "POST", "/api/auth/login", map[string]string{
		"username": "resetuser", "password": "reset-next-pass-123",
	})
	if code != http.StatusOK {
		t.Fatalf("login after reset: %d %s", code, body)
	}
	resetLogin := decode[struct {
		User model.User `json:"user"`
	}](t, body)
	if !resetLogin.User.MustChangePassword || resetLogin.User.SuggestPasswordChange {
		t.Fatalf("reset account should be forced to change password without a suggestion: %+v", resetLogin.User)
	}

	code, body = e.req(afterReset, "GET", "/api/nodes", nil)
	if code != http.StatusForbidden || decode[errorPayload](t, body).Error.Code != "PASSWORD_CHANGE_REQUIRED" {
		t.Fatalf("reset account should be blocked until password change: %d %s", code, body)
	}

	code, body = e.req(afterReset, "POST", "/api/auth/change-password", map[string]string{
		"old_password": "reset-next-pass-123", "new_password": "reset-final-pass-123",
	})
	if code != http.StatusOK {
		t.Fatalf("change password after reset: %d %s", code, body)
	}
	code, body = e.req(afterReset, "GET", "/api/nodes", nil)
	if code != http.StatusOK {
		t.Fatalf("reset account should access app after password change: %d %s", code, body)
	}
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

func TestAdminCannotChangeOwnRoleOrManagePeer(t *testing.T) {
	e := newEnv(t)
	e.setupAdmin()

	createUser := func(username, displayName, password, role string) model.User {
		t.Helper()
		code, body := e.req(e.admin, "POST", "/api/admin/users", map[string]string{
			"username": username, "display_name": displayName, "password": password, "role": role,
		})
		if code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", username, code, body)
		}
		return decode[model.User](t, body)
	}

	admin2User := createUser("admin2", "第二管理员", "admin2-pass-123", "admin")
	admin3User := createUser("admin3", "第三管理员", "admin3-pass-123", "admin")
	editorUser := createUser("editor1", "编辑同事", "editor-pass-123", "editor")

	admin2 := newClient()
	code, body := e.req(admin2, "POST", "/api/auth/login", map[string]string{
		"username": "admin2", "password": "admin2-pass-123",
	})
	if code != http.StatusOK {
		t.Fatalf("second admin login: %d %s", code, body)
	}

	code, body = e.req(admin2, "PATCH", "/api/admin/users/"+admin2User.ID, map[string]any{
		"display_name": "第二管理员改名", "role": "admin", "disabled": false,
	})
	if code != http.StatusOK {
		t.Fatalf("admin renaming self: %d %s", code, body)
	}

	code, body = e.req(admin2, "PATCH", "/api/admin/users/"+admin2User.ID, map[string]any{
		"display_name": "第二管理员改名", "role": "editor", "disabled": false,
	})
	if code != http.StatusBadRequest || decode[errorPayload](t, body).Error.Code != "SELF_ROLE" {
		t.Fatalf("admin changing own role should fail: %d %s", code, body)
	}
	code, body = e.req(admin2, "GET", "/api/admin/users", nil)
	if code != http.StatusOK {
		t.Fatalf("admin should keep admin access after rejected self role change: %d %s", code, body)
	}

	code, body = e.req(admin2, "PATCH", "/api/admin/users/"+admin3User.ID, map[string]any{
		"display_name": "被同级改名", "role": "admin", "disabled": false,
	})
	if code != http.StatusForbidden || decode[errorPayload](t, body).Error.Code != "SAME_LEVEL" {
		t.Fatalf("admin editing peer admin should fail: %d %s", code, body)
	}
	code, body = e.req(admin2, "POST", "/api/admin/users/"+admin3User.ID+"/reset-password", map[string]string{
		"password": "peer-reset-123",
	})
	if code != http.StatusForbidden || decode[errorPayload](t, body).Error.Code != "SAME_LEVEL" {
		t.Fatalf("admin resetting peer admin password should fail: %d %s", code, body)
	}

	code, body = e.req(admin2, "PATCH", "/api/admin/users/"+editorUser.ID, map[string]any{
		"display_name": "编辑同事二", "role": "editor", "disabled": false,
	})
	if code != http.StatusOK {
		t.Fatalf("admin updating lower-level user: %d %s", code, body)
	}

	code, body = e.req(e.admin, "PATCH", "/api/admin/users/"+admin3User.ID, map[string]any{
		"display_name": "第三管理员改名", "role": "admin", "disabled": false,
	})
	if code != http.StatusOK {
		t.Fatalf("owner updating peer admin: %d %s", code, body)
	}
	code, body = e.req(e.admin, "POST", "/api/admin/users/"+admin3User.ID+"/reset-password", map[string]string{
		"password": "admin3-next-pass-123",
	})
	if code != http.StatusOK {
		t.Fatalf("owner resetting peer admin password: %d %s", code, body)
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

func TestFilePreview(t *testing.T) {
	e := newEnv(t)
	e.setupAdmin()

	code, body := e.upload(e.admin, "图片.PNG", "image-bytes", "", "", "")
	if code != http.StatusOK {
		t.Fatalf("upload image: %d %s", code, body)
	}
	imageNode := decode[uploadResponse](t, body).Node

	res, content := e.preview(e.admin, imageNode.ID, "")
	if res.StatusCode != http.StatusOK || string(content) != "image-bytes" {
		t.Fatalf("image preview failed: %d %q", res.StatusCode, content)
	}
	if res.Header.Get("Content-Type") != "image/png" || res.Header.Get("X-Content-Type-Options") != "nosniff" ||
		res.Header.Get("Cache-Control") != "private, no-store, max-age=0" ||
		!strings.HasPrefix(res.Header.Get("Content-Disposition"), "inline;") {
		t.Fatalf("image preview headers: %+v", res.Header)
	}
	res, content = e.preview(e.admin, imageNode.ID, "bytes=0-4")
	if res.StatusCode != http.StatusPartialContent || string(content) != "image" ||
		res.Header.Get("Content-Range") != "bytes 0-4/11" {
		t.Fatalf("image range preview failed: %d %q %+v", res.StatusCode, content, res.Header)
	}

	code, body = e.upload(e.admin, "说明.TXT", "第一行\nsecond", "", "", "")
	if code != http.StatusOK {
		t.Fatalf("upload text: %d %s", code, body)
	}
	textNode := decode[uploadResponse](t, body).Node
	res, content = e.preview(e.admin, textNode.ID, "")
	if res.StatusCode != http.StatusOK || string(content) != "第一行\nsecond" ||
		res.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("utf-8 text preview failed: %d %q %+v", res.StatusCode, content, res.Header)
	}

	code, body = e.upload(e.admin, "版本.txt", "version-one", "", "", "")
	if code != http.StatusOK {
		t.Fatalf("upload versioned text: %d %s", code, body)
	}
	versioned := decode[uploadResponse](t, body)
	code, body = e.upload(e.admin, "版本.txt", "version-two", "", versioned.Node.ID, versioned.Node.CurrentVersionID)
	if code != http.StatusOK {
		t.Fatalf("upload current version: %d %s", code, body)
	}
	if res, content := e.preview(e.admin, versioned.Node.ID, ""); res.StatusCode != http.StatusOK || string(content) != "version-two" {
		t.Fatalf("preview should use current version: %d %q", res.StatusCode, content)
	}

	gbk, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("中文日志"))
	if err != nil {
		t.Fatal(err)
	}
	code, body = e.upload(e.admin, "旧编码.log", string(gbk), "", "", "")
	if code != http.StatusOK {
		t.Fatalf("upload gbk text: %d %s", code, body)
	}
	gbkNode := decode[uploadResponse](t, body).Node
	if res, content := e.preview(e.admin, gbkNode.ID, ""); res.StatusCode != http.StatusOK || string(content) != "中文日志" {
		t.Fatalf("gbk text preview failed: %d %q", res.StatusCode, content)
	}

	large := strings.Repeat("A", (1<<20)+10)
	code, body = e.upload(e.admin, "大文本.txt", large, "", "", "")
	if code != http.StatusOK {
		t.Fatalf("upload large text: %d %s", code, body)
	}
	largeNode := decode[uploadResponse](t, body).Node
	res, content = e.preview(e.admin, largeNode.ID, "")
	if res.StatusCode != http.StatusOK || len(content) != 1<<20 || res.Header.Get("X-Preview-Truncated") != "1" {
		t.Fatalf("large text should be truncated: %d body=%d headers=%+v", res.StatusCode, len(content), res.Header)
	}

	for name, wantType := range map[string]string{
		"报告.pdf":  "application/pdf",
		"声音.mp3":  "audio/mpeg",
		"录音.wav":  "audio/wav",
		"视频.mp4":  "video/mp4",
		"网页.webm": "video/webm",
	} {
		code, body := e.upload(e.admin, name, "preview-"+name, "", "", "")
		if code != http.StatusOK {
			t.Fatalf("upload %s: %d %s", name, code, body)
		}
		node := decode[uploadResponse](t, body).Node
		res, content := e.preview(e.admin, node.ID, "")
		if res.StatusCode != http.StatusOK || string(content) != "preview-"+name || res.Header.Get("Content-Type") != wantType {
			t.Fatalf("preview %s failed: %d %q %+v", name, res.StatusCode, content, res.Header)
		}
	}

	for _, name := range []string{"页面.html", "图标.svg"} {
		code, body := e.upload(e.admin, name, "<script>alert(1)</script>", "", "", "")
		if code != http.StatusOK {
			t.Fatalf("upload unsupported %s: %d %s", name, code, body)
		}
		node := decode[uploadResponse](t, body).Node
		if res, content := e.preview(e.admin, node.ID, ""); res.StatusCode != http.StatusUnsupportedMediaType ||
			bytes.Contains(content, []byte("<script>")) {
			t.Fatalf("unsupported preview %s should be rejected: %d %q", name, res.StatusCode, content)
		}
	}

	code, body = e.req(e.admin, "DELETE", "/api/nodes/"+imageNode.ID, nil)
	if code != http.StatusOK {
		t.Fatalf("delete preview image: %d %s", code, body)
	}
	if res, _ := e.preview(e.admin, imageNode.ID, ""); res.StatusCode != http.StatusNotFound {
		t.Fatalf("preview after delete should be 404, got %d", res.StatusCode)
	}
}

func (e *env) createUserAs(actor *http.Client, username, role string) model.User {
	e.t.Helper()
	password := "pass-" + username + "-123"
	code, body := e.req(actor, "POST", "/api/admin/users", map[string]string{
		"username": username, "display_name": username, "password": password, "role": role,
	})
	if code != http.StatusCreated {
		e.t.Fatalf("create %s: %d %s", username, code, body)
	}
	return decode[model.User](e.t, body)
}

func (e *env) loginAs(username string) *http.Client {
	e.t.Helper()
	client := newClient()
	code, body := e.req(client, "POST", "/api/auth/login", map[string]string{
		"username": username, "password": "pass-" + username + "-123",
	})
	if code != http.StatusOK {
		e.t.Fatalf("login %s: %d %s", username, code, body)
	}
	return client
}

func (e *env) listNames(client *http.Client, parent string) map[string]bool {
	e.t.Helper()
	code, body := e.req(client, "GET", "/api/nodes?parent="+url.QueryEscape(parent), nil)
	if code != http.StatusOK {
		e.t.Fatalf("list nodes: %d %s", code, body)
	}
	page := decode[struct {
		Items []files.Node `json:"items"`
	}](e.t, body)
	names := map[string]bool{}
	for _, item := range page.Items {
		names[item.Name] = true
	}
	return names
}

func TestOperatorRoleAndAccountAdministration(t *testing.T) {
	e := newEnv(t)
	e.setupAdmin()
	operator := e.createUserAs(e.admin, "operator1", "operator")
	admin2 := e.createUserAs(e.admin, "admin2", "admin")
	operatorClient := e.loginAs("operator1")
	admin2Client := e.loginAs("admin2")

	code, body := e.req(operatorClient, "GET", "/api/admin/users", nil)
	if code != http.StatusOK {
		t.Fatalf("operator should view users: %d %s", code, body)
	}
	if code, _ := e.req(operatorClient, "POST", "/api/admin/users", map[string]string{
		"username": "blocked", "display_name": "blocked", "password": "blocked-pass-123", "role": "viewer",
	}); code != http.StatusForbidden {
		t.Fatalf("operator create user should be forbidden, got %d", code)
	}
	if code, _ := e.req(operatorClient, "PATCH", "/api/admin/users/"+operator.ID, map[string]any{
		"display_name": "operator1", "role": "operator", "disabled": false,
	}); code != http.StatusForbidden {
		t.Fatalf("operator update user should be forbidden, got %d", code)
	}
	for _, path := range []string{"/api/admin/audit", "/api/recycle", "/api/admin/settings", "/api/admin/system", "/api/admin/backups"} {
		if code, body := e.req(operatorClient, "GET", path, nil); code != http.StatusOK {
			t.Fatalf("operator should access %s: %d %s", path, code, body)
		}
	}
	if code, _ := e.req(operatorClient, "POST", "/api/admin/backups/run", nil); code == http.StatusForbidden {
		t.Fatalf("operator backup route should not be forbidden")
	}
	if code, body := e.req(operatorClient, "PUT", "/api/admin/settings", map[string]any{"recycle_retention_days": 30}); code != http.StatusOK {
		t.Fatalf("operator should save settings: %d %s", code, body)
	}

	code, body = e.upload(e.admin, "操作员恢复.txt", "operator recycle", "", "", "")
	if code != http.StatusOK {
		t.Fatalf("upload recycle sample: %d %s", code, body)
	}
	recycleNode := decode[uploadResponse](t, body).Node
	if code, _ := e.req(e.admin, "DELETE", "/api/nodes/"+recycleNode.ID, nil); code != http.StatusOK {
		t.Fatalf("delete recycle sample: %d", code)
	}
	code, body = e.req(operatorClient, "GET", "/api/recycle", nil)
	recycle := decode[struct {
		Items []files.Node `json:"items"`
	}](t, body)
	var sampleBatch string
	for _, item := range recycle.Items {
		if item.ID == recycleNode.ID {
			sampleBatch = item.DeleteBatch
		}
	}
	if sampleBatch == "" {
		t.Fatalf("operator should see main recycle item: %s", body)
	}
	if code, _ := e.req(operatorClient, "POST", "/api/recycle/"+sampleBatch+"/restore", nil); code != http.StatusOK {
		t.Fatalf("operator should restore main recycle item: %d", code)
	}
	if code, _ := e.req(e.admin, "DELETE", "/api/nodes/"+recycleNode.ID, nil); code != http.StatusOK {
		t.Fatalf("delete recycle sample again: %d", code)
	}
	code, body = e.req(operatorClient, "GET", "/api/recycle", nil)
	recycle = decode[struct {
		Items []files.Node `json:"items"`
	}](t, body)
	sampleBatch = ""
	for _, item := range recycle.Items {
		if item.ID == recycleNode.ID {
			sampleBatch = item.DeleteBatch
		}
	}
	if sampleBatch == "" {
		t.Fatalf("operator should see main recycle item again: %s", body)
	}
	if code, _ := e.req(operatorClient, "DELETE", "/api/recycle/"+sampleBatch, nil); code != http.StatusOK {
		t.Fatalf("operator should purge main recycle item: %d", code)
	}

	code, body = e.req(admin2Client, "POST", "/api/admin/users", map[string]string{
		"username": "operator2", "display_name": "operator2", "password": "pass-operator2-123", "role": "operator",
	})
	if code != http.StatusCreated {
		t.Fatalf("admin should create operator: %d %s", code, body)
	}
	operator2 := decode[model.User](t, body)

	code, body = e.req(admin2Client, "POST", "/api/admin/users", map[string]string{
		"username": "admin3", "display_name": "admin3", "password": "pass-admin3-123", "role": "admin",
	})
	if code != http.StatusForbidden || decode[errorPayload](t, body).Error.Code != "ADMIN_ASSIGN_FORBIDDEN" {
		t.Fatalf("admin should not create admin: %d %s", code, body)
	}
	code, body = e.req(admin2Client, "PATCH", "/api/admin/users/"+operator2.ID, map[string]any{
		"display_name": "operator2", "role": "admin", "disabled": false,
	})
	if code != http.StatusForbidden || decode[errorPayload](t, body).Error.Code != "ADMIN_ASSIGN_FORBIDDEN" {
		t.Fatalf("admin should not promote operator to admin: %d %s", code, body)
	}

	code, body = e.req(e.admin, "PATCH", "/api/admin/users/"+operator2.ID, map[string]any{
		"display_name": "operator2", "role": "admin", "disabled": false,
	})
	if code != http.StatusOK {
		t.Fatalf("owner should promote operator to admin: %d %s", code, body)
	}
	if admin2.ID == "" {
		t.Fatal("admin2 missing")
	}
}

func TestSecureZoneVisibilityAndAccess(t *testing.T) {
	e := newEnv(t)
	e.setupAdmin()
	op1 := e.createUserAs(e.admin, "op1", "operator")
	_ = e.createUserAs(e.admin, "op2", "operator")
	_ = e.createUserAs(e.admin, "ed1", "editor")
	viewer := e.createUserAs(e.admin, "vi1", "viewer")
	_ = e.createUserAs(e.admin, "ad2", "admin")
	op1Client := e.loginAs("op1")
	op2Client := e.loginAs("op2")
	edClient := e.loginAs("ed1")
	viewerClient := e.loginAs("vi1")
	admin2Client := e.loginAs("ad2")

	code, body := e.req(e.admin, "GET", "/api/folders/tree", nil)
	if code != http.StatusOK {
		t.Fatalf("folder tree: %d %s", code, body)
	}
	tree := decode[struct {
		Items []files.Node `json:"items"`
	}](t, body)
	var secureRoot files.Node
	for _, item := range tree.Items {
		if item.IsZoneRoot && item.Space == model.SpaceSecure {
			secureRoot = item
			break
		}
	}
	if secureRoot.ID == "" {
		t.Fatalf("secure root missing: %+v", tree.Items)
	}

	code, body = e.req(op1Client, "POST", "/api/nodes", map[string]string{
		"parent_id": secureRoot.ID, "name": "共享",
	})
	if code != http.StatusCreated {
		t.Fatalf("operator create secure folder: %d %s", code, body)
	}
	folder := decode[files.Node](t, body)

	code, body = e.uploadSecure(op1Client, "默认.txt", "default-content", folder.ID, "default", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("default secure upload: %d %s", code, body)
	}
	defaultUpload := decode[uploadResponse](t, body)

	code, body = e.uploadSecure(op1Client, "指定.txt", "custom-content", folder.ID, "custom",
		[]string{model.RoleEditor}, []string{viewer.ID})
	if code != http.StatusOK {
		t.Fatalf("custom secure upload: %d %s", code, body)
	}
	customUpload := decode[uploadResponse](t, body)

	ownerNames := e.listNames(e.admin, folder.ID)
	if !ownerNames["默认.txt"] || !ownerNames["指定.txt"] {
		t.Fatalf("owner should see secure files: %+v", ownerNames)
	}
	adminNames := e.listNames(admin2Client, folder.ID)
	if !adminNames["默认.txt"] || adminNames["指定.txt"] {
		t.Fatalf("admin2 should only see the default hierarchy file: %+v", adminNames)
	}
	for name, client := range map[string]*http.Client{
		"operator2": op2Client, "editor": edClient, "viewer": viewerClient,
	} {
		names := e.listNames(client, folder.ID)
		if names["默认.txt"] {
			t.Fatalf("%s should not see default secure file: %+v", name, names)
		}
	}
	if !e.listNames(edClient, folder.ID)["指定.txt"] || !e.listNames(viewerClient, folder.ID)["指定.txt"] {
		t.Fatalf("explicit editor/viewer visibility missing")
	}
	if e.listNames(admin2Client, folder.ID)["指定.txt"] || e.listNames(op2Client, folder.ID)["指定.txt"] {
		t.Fatalf("custom file leaked to unselected accounts")
	}

	if code, _ := e.download(admin2Client, customUpload.Node.ID, ""); code != http.StatusNotFound {
		t.Fatalf("hidden secure file download should be 404, got %d", code)
	}
	if code, body := e.download(edClient, customUpload.Node.ID, ""); code != http.StatusOK || string(body) != "custom-content" {
		t.Fatalf("explicit secure download failed: %d %q", code, body)
	}
	if res, body := e.preview(edClient, customUpload.Node.ID, ""); res.StatusCode != http.StatusOK || string(body) != "custom-content" ||
		res.Header.Get("Content-Type") != "text/plain; charset=utf-8" || res.Header.Get("Cache-Control") != "private, no-store, max-age=0" {
		t.Fatalf("explicit secure preview failed: %d %q %+v", res.StatusCode, body, res.Header)
	}
	if res, _ := e.preview(admin2Client, customUpload.Node.ID, ""); res.StatusCode != http.StatusNotFound {
		t.Fatalf("hidden secure preview should be 404, got %d", res.StatusCode)
	}
	if code, _ := e.download(op2Client, defaultUpload.Node.ID, ""); code != http.StatusNotFound {
		t.Fatalf("same-level default file should be hidden, got %d", code)
	}

	code, body = e.req(op1Client, "GET", "/api/nodes/"+customUpload.Node.ID+"/visibility", nil)
	if code != http.StatusOK {
		t.Fatalf("uploader visibility: %d %s", code, body)
	}
	vis := decode[files.VisibilityInfo](t, body)
	if vis.Mode != "custom" || len(vis.Roles) != 1 || vis.Roles[0] != model.RoleEditor || len(vis.Users) != 1 || vis.Users[0].ID != viewer.ID {
		t.Fatalf("unexpected visibility: %+v", vis)
	}
	if code, _ := e.req(edClient, "GET", "/api/nodes/"+customUpload.Node.ID+"/visibility", nil); code != http.StatusForbidden {
		t.Fatalf("editor should not read visibility details, got %d", code)
	}
	if code, _ := e.req(edClient, "PUT", "/api/nodes/"+customUpload.Node.ID+"/visibility", map[string]any{
		"mode": "default", "roles": []string{}, "users": []string{},
	}); code != http.StatusForbidden {
		t.Fatalf("editor should not modify visibility, got %d", code)
	}

	code, body = e.upload(op1Client, "指定.txt", "custom-v2", "", customUpload.Node.ID, customUpload.Node.CurrentVersionID)
	if code != http.StatusOK {
		t.Fatalf("secure new version: %d %s", code, body)
	}
	v2 := decode[uploadResponse](t, body)
	if code, body := e.download(edClient, customUpload.Node.ID, ""); code != http.StatusOK || string(body) != "custom-v2" {
		t.Fatalf("new version should keep visibility: %d %q", code, body)
	}
	if code, _ := e.download(admin2Client, customUpload.Node.ID, ""); code != http.StatusNotFound {
		t.Fatalf("new version leaked to admin: %d", code)
	}
	_ = v2

	code, body = e.req(e.admin, "GET", "/api/nodes/"+folder.ID+"/download-folder", nil)
	if code != http.StatusOK || !bytes.Contains(body, []byte("默认.txt")) || !bytes.Contains(body, []byte("指定.txt")) {
		t.Fatalf("owner folder zip missing files: %d", code)
	}
	code, body = e.req(edClient, "GET", "/api/nodes/"+folder.ID+"/download-folder", nil)
	if code != http.StatusOK || bytes.Contains(body, []byte("默认.txt")) || !bytes.Contains(body, []byte("指定.txt")) {
		t.Fatalf("editor folder zip filtering failed: %d", code)
	}
	code, body = e.req(op2Client, "GET", "/api/nodes/"+folder.ID+"/download-folder", nil)
	if code != http.StatusOK || bytes.Contains(body, []byte(".txt")) {
		t.Fatalf("operator2 folder zip should not contain files: %d", code)
	}

	code, body = e.req(op2Client, "GET", "/api/nodes?q="+url.QueryEscape("指定"), nil)
	search := decode[struct {
		Items []files.Node `json:"items"`
	}](t, body)
	if code != http.StatusOK {
		t.Fatalf("search operator2: %d %s", code, body)
	}
	for _, item := range search.Items {
		if item.ID == customUpload.Node.ID {
			t.Fatalf("hidden custom file appeared in search")
		}
	}
	code, body = e.req(edClient, "GET", "/api/nodes?q="+url.QueryEscape("指定"), nil)
	search = decode[struct {
		Items []files.Node `json:"items"`
	}](t, body)
	found := false
	for _, item := range search.Items {
		if item.ID == customUpload.Node.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("visible custom file missing from search: %s", body)
	}

	code, body = e.req(op1Client, "PUT", "/api/nodes/"+customUpload.Node.ID+"/visibility", map[string]any{
		"mode": "custom", "roles": []string{model.RoleViewer}, "users": []string{},
	})
	if code != http.StatusOK {
		t.Fatalf("update visibility: %d %s", code, body)
	}
	if e.listNames(edClient, folder.ID)["指定.txt"] {
		t.Fatalf("editor should lose access after visibility update")
	}
	if !e.listNames(viewerClient, folder.ID)["指定.txt"] {
		t.Fatalf("viewer should gain access by role group")
	}
	if res, _ := e.preview(edClient, customUpload.Node.ID, ""); res.StatusCode != http.StatusNotFound {
		t.Fatalf("old preview URL should fail after visibility revoke, got %d", res.StatusCode)
	}
	if res, body := e.preview(viewerClient, customUpload.Node.ID, ""); res.StatusCode != http.StatusOK || string(body) != "custom-v2" {
		t.Fatalf("newly visible viewer preview failed: %d %q", res.StatusCode, body)
	}
	code, body = e.req(viewerClient, "GET", "/api/nodes?parent="+url.QueryEscape(folder.ID), nil)
	viewerPage := decode[struct {
		Items []files.Node `json:"items"`
	}](t, body)
	viewerCanDelete := false
	for _, item := range viewerPage.Items {
		if item.ID == customUpload.Node.ID {
			viewerCanDelete = item.CanDelete
		}
	}
	if code != http.StatusOK || !viewerCanDelete {
		t.Fatalf("visible secure file should expose delete permission: %d %+v", code, viewerPage.Items)
	}

	if code, _ := e.req(edClient, "DELETE", "/api/nodes/"+customUpload.Node.ID, nil); code != http.StatusNotFound {
		t.Fatalf("editor secure delete should be hidden, got %d", code)
	}
	if code, _ := e.req(op1Client, "DELETE", "/api/nodes/"+folder.ID, nil); code != http.StatusForbidden {
		t.Fatalf("operator secure folder delete should be host-only, got %d", code)
	}
	if code, _ := e.req(op1Client, "PATCH", "/api/nodes/"+folder.ID, map[string]string{"name": "改名"}); code != http.StatusForbidden {
		t.Fatalf("operator secure folder rename should be forbidden, got %d", code)
	}
	if code, _ := e.req(e.admin, "PATCH", "/api/nodes/"+secureRoot.ID, map[string]string{"name": "改名"}); code != http.StatusForbidden {
		t.Fatalf("secure root rename should be forbidden, got %d", code)
	}
	if code, _ := e.req(e.admin, "DELETE", "/api/nodes/"+secureRoot.ID, nil); code != http.StatusForbidden {
		t.Fatalf("secure root delete should be forbidden, got %d", code)
	}
	code, body = e.req(e.admin, "PATCH", "/api/nodes/"+customUpload.Node.ID, map[string]string{"parent_id": ""})
	if code != http.StatusBadRequest || decode[errorPayload](t, body).Error.Code != "CROSS_SPACE_MOVE" {
		t.Fatalf("cross-space move should fail: %d %s", code, body)
	}
	code, body = e.req(e.admin, "PATCH", "/api/nodes/"+folder.ID, map[string]string{"name": "改为共享二"})
	if code != http.StatusOK {
		t.Fatalf("owner secure folder rename: %d %s", code, body)
	}

	if code, _ := e.req(op1Client, "GET", "/api/nodes/"+customUpload.Node.ID+"/audit", nil); code != http.StatusOK {
		t.Fatalf("uploader should see node audit, got %d", code)
	}
	if code, _ := e.req(edClient, "GET", "/api/nodes/"+customUpload.Node.ID+"/audit", nil); code != http.StatusNotFound {
		t.Fatalf("editor should lose node audit after visibility update, got %d", code)
	}
	code, body = e.req(admin2Client, "GET", "/api/admin/audit?q="+url.QueryEscape("指定"), nil)
	adminAudit := decode[struct {
		Total int `json:"total"`
	}](t, body)
	if code != http.StatusOK || adminAudit.Total != 0 {
		t.Fatalf("admin audit should hide secure entries: %d %+v", code, adminAudit)
	}
	code, body = e.req(op1Client, "GET", "/api/admin/audit?q="+url.QueryEscape("指定"), nil)
	operatorAudit := decode[struct {
		Total int `json:"total"`
	}](t, body)
	if code != http.StatusOK || operatorAudit.Total != 0 {
		t.Fatalf("operator audit should hide secure entries: %d %+v", code, operatorAudit)
	}
	code, body = e.req(e.admin, "GET", "/api/admin/audit?q="+url.QueryEscape("指定"), nil)
	ownerAudit := decode[struct {
		Total int `json:"total"`
	}](t, body)
	if code != http.StatusOK || ownerAudit.Total == 0 {
		t.Fatalf("owner audit should include secure entries: %d %+v", code, ownerAudit)
	}

	code, body = e.req(viewerClient, "DELETE", "/api/nodes/"+customUpload.Node.ID, nil)
	if code != http.StatusOK {
		t.Fatalf("visible user secure delete: %d %s", code, body)
	}
	if res, _ := e.preview(viewerClient, customUpload.Node.ID, ""); res.StatusCode != http.StatusNotFound {
		t.Fatalf("preview should fail after deletion, got %d", res.StatusCode)
	}
	code, body = e.req(admin2Client, "GET", "/api/recycle", nil)
	adminRecycle := decode[struct {
		Items []files.Node `json:"items"`
	}](t, body)
	for _, item := range adminRecycle.Items {
		if item.ID == customUpload.Node.ID {
			t.Fatalf("secure recycle item leaked to admin")
		}
	}
	code, body = e.req(op1Client, "GET", "/api/recycle", nil)
	operatorRecycle := decode[struct {
		Items []files.Node `json:"items"`
	}](t, body)
	for _, item := range operatorRecycle.Items {
		if item.ID == customUpload.Node.ID {
			t.Fatalf("secure recycle item leaked to operator")
		}
	}
	code, body = e.req(e.admin, "GET", "/api/recycle", nil)
	ownerRecycle := decode[struct {
		Items []files.Node `json:"items"`
	}](t, body)
	if code != http.StatusOK {
		t.Fatalf("owner recycle: %d %s", code, body)
	}
	var batch string
	for _, item := range ownerRecycle.Items {
		if item.ID == customUpload.Node.ID {
			batch = item.DeleteBatch
		}
	}
	if batch == "" {
		t.Fatalf("secure file missing from owner recycle: %+v", ownerRecycle.Items)
	}
	if code, _ := e.req(e.admin, "POST", "/api/recycle/"+batch+"/restore", nil); code != http.StatusOK {
		t.Fatalf("owner secure restore failed: %d", code)
	}
	if code, _ := e.req(edClient, "GET", "/api/visibility/options", nil); code != http.StatusForbidden {
		t.Fatalf("editor visibility options should be forbidden, got %d", code)
	}
	if code, _ := e.req(op1Client, "GET", "/api/visibility/options", nil); code != http.StatusOK {
		t.Fatalf("operator visibility options should be allowed, got %d", code)
	}
	_ = op1
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
