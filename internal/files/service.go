package files

import (
	"database/sql"
	"fmt"
	"os"
	"path"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"lanfile/internal/apperr"
	"lanfile/internal/audit"
	"lanfile/internal/model"
	"lanfile/internal/settings"
	"lanfile/internal/storage"
)

type Actor struct {
	ID          string
	DisplayName string
	Role        string
	IsOwner     bool
	IP          string
}

func (a Actor) IsAdmin() bool { return a.Role == model.RoleAdmin }
func (a Actor) CanAccessAdmin() bool {
	return a.Role == model.RoleAdmin || a.Role == model.RoleOperator
}
func (a Actor) CanEdit() bool {
	return a.Role == model.RoleAdmin || a.Role == model.RoleOperator || a.Role == model.RoleEditor
}
func (a Actor) CanUploadSecure() bool {
	return a.Role == model.RoleAdmin || a.Role == model.RoleOperator
}
func (a Actor) EffectiveRank() int {
	if a.IsOwner {
		return 4
	}
	return model.RoleRank(a.Role)
}

type Service struct {
	DB       *sql.DB
	Store    *storage.Store
	Audit    *audit.Service
	Settings *settings.Service
	mu       sync.Mutex
}

type Node struct {
	ID                  string `json:"id"`
	ParentID            string `json:"parent_id"`
	Name                string `json:"name"`
	Kind                string `json:"kind"`
	CurrentVersionID    string `json:"current_version_id,omitempty"`
	Revision            int    `json:"revision"`
	Size                int64  `json:"size"`
	SHA256              string `json:"sha256,omitempty"`
	VersionNo           int    `json:"version_no"`
	CreatedAt           string `json:"created_at"`
	CreatedBy           string `json:"created_by"`
	UpdatedAt           string `json:"updated_at"`
	UpdatedBy           string `json:"updated_by"`
	DeletedAt           string `json:"deleted_at,omitempty"`
	DeletedBy           string `json:"deleted_by,omitempty"`
	DeleteBatch         string `json:"delete_batch,omitempty"`
	Path                string `json:"path,omitempty"`
	Space               string `json:"space"`
	VisibilityMode      string `json:"visibility_mode,omitempty"`
	IsZoneRoot          bool   `json:"is_zone_root,omitempty"`
	CanManage           bool   `json:"can_manage,omitempty"`
	CanDelete           bool   `json:"can_delete,omitempty"`
	CanManageVisibility bool   `json:"can_manage_visibility,omitempty"`
	CreatedByID         string `json:"-"`
	SecureOwnerRole     string `json:"-"`
}

type Visibility struct {
	Mode  string   `json:"mode"`
	Roles []string `json:"roles"`
	Users []string `json:"users"`
}

type VisibilityUser struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

type VisibilityInfo struct {
	Mode  string           `json:"mode"`
	Roles []string         `json:"roles"`
	Users []VisibilityUser `json:"users"`
}

type VisibilityOptions struct {
	Roles []string         `json:"roles"`
	Users []VisibilityUser `json:"users"`
}

type Version struct {
	ID            string `json:"id"`
	NodeID        string `json:"node_id"`
	VersionNo     int    `json:"version_no"`
	ObjectID      string `json:"object_id"`
	Size          int64  `json:"size"`
	SHA256        string `json:"sha256"`
	Note          string `json:"note"`
	CreatedAt     string `json:"created_at"`
	CreatedBy     string `json:"created_by"`
	CreatedByName string `json:"created_by_name"`
	IsCurrent     bool   `json:"is_current"`
}

type Breadcrumb struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Space string `json:"space"`
}

type ArchiveEntry struct {
	NodeID   string
	Path     string
	Kind     string
	ObjectID string
	Size     int64
}

type UploadResult struct {
	Node    Node    `json:"node"`
	Version Version `json:"version"`
}

type Stats struct {
	Files       int   `json:"files"`
	Folders     int   `json:"folders"`
	Versions    int   `json:"versions"`
	ObjectBytes int64 `json:"object_bytes"`
	FreeSpace   int64 `json:"free_space"`
}

func New(db *sql.DB, store *storage.Store, auditSvc *audit.Service, settingsSvc *settings.Service) *Service {
	return &Service{DB: db, Store: store, Audit: auditSvc, Settings: settingsSvc}
}

const nodeColumns = `n.id, n.parent_id, n.name, n.kind, n.current_version_id, n.revision,
	COALESCE(v.size, 0), COALESCE(v.sha256, ''), COALESCE(v.version_no, 0),
	n.created_at, n.created_by, n.updated_at, n.updated_by,
	COALESCE(n.deleted_at, ''), COALESCE(n.deleted_by, ''), COALESCE(n.delete_batch, ''),
	n.space, n.created_by_id, n.secure_owner_role, n.visibility_mode`

type scanner interface {
	Scan(dest ...any) error
}

func scanNode(row scanner) (Node, error) {
	var n Node
	err := row.Scan(&n.ID, &n.ParentID, &n.Name, &n.Kind, &n.CurrentVersionID, &n.Revision,
		&n.Size, &n.SHA256, &n.VersionNo, &n.CreatedAt, &n.CreatedBy, &n.UpdatedAt, &n.UpdatedBy,
		&n.DeletedAt, &n.DeletedBy, &n.DeleteBatch, &n.Space, &n.CreatedByID, &n.SecureOwnerRole,
		&n.VisibilityMode)
	n.IsZoneRoot = n.Space == model.SpaceSecure && n.ParentID == "" && n.Kind == model.KindFolder
	return n, err
}

type aclSet struct {
	roles map[string]bool
	users map[string]bool
}

func (n Node) IsSecure() bool { return n.Space == model.SpaceSecure }

func (s *Service) aclSets(nodeIDs []string) (map[string]aclSet, error) {
	out := map[string]aclSet{}
	if len(nodeIDs) == 0 {
		return out, nil
	}
	args := make([]any, len(nodeIDs))
	for i, id := range nodeIDs {
		args[i] = id
	}
	rows, err := s.DB.Query(`SELECT node_id, principal_type, principal_id FROM node_acl WHERE node_id IN (`+
		strings.TrimSuffix(strings.Repeat("?,", len(nodeIDs)), ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var nodeID, kind, principal string
		if err := rows.Scan(&nodeID, &kind, &principal); err != nil {
			return nil, err
		}
		set := out[nodeID]
		if kind == "role" {
			if set.roles == nil {
				set.roles = map[string]bool{}
			}
			set.roles[principal] = true
		} else {
			if set.users == nil {
				set.users = map[string]bool{}
			}
			set.users[principal] = true
		}
		out[nodeID] = set
	}
	return out, rows.Err()
}

func canViewNodeWithACL(n Node, actor Actor, acl aclSet) bool {
	if !n.IsSecure() {
		return true
	}
	if n.Kind == model.KindFolder {
		return true
	}
	if actor.IsOwner || n.CreatedByID == actor.ID {
		return true
	}
	if n.VisibilityMode == "custom" {
		return acl.roles[actor.Role] || acl.users[actor.ID]
	}
	ownerRank := model.RoleRank(n.SecureOwnerRole)
	return ownerRank >= 0 && actor.EffectiveRank() > ownerRank
}

func canManageNode(n Node, actor Actor) bool {
	if !n.IsSecure() {
		return actor.CanEdit()
	}
	if n.IsZoneRoot {
		return false
	}
	if n.Kind == model.KindFolder {
		return actor.IsOwner
	}
	return actor.CanUploadSecure() && (actor.IsOwner || n.CreatedByID == actor.ID)
}

func canDeleteNode(n Node, actor Actor, acl aclSet) bool {
	if !n.IsSecure() {
		return actor.CanEdit()
	}
	if n.IsZoneRoot {
		return false
	}
	if n.Kind == model.KindFolder {
		return actor.IsOwner
	}
	return canViewNodeWithACL(n, actor, acl)
}

func (s *Service) applyAccess(n *Node, actor Actor, acl aclSet) {
	n.IsZoneRoot = n.Space == model.SpaceSecure && n.ParentID == "" && n.Kind == model.KindFolder
	n.CanManage = canManageNode(*n, actor)
	n.CanDelete = canDeleteNode(*n, actor, acl)
	n.CanManageVisibility = n.IsSecure() && n.Kind == model.KindFile && n.VisibilityMode != "" &&
		actor.CanUploadSecure() && (actor.IsOwner || n.CreatedByID == actor.ID)
}

func (s *Service) CanViewNode(n Node, actor Actor) (bool, error) {
	if !n.IsSecure() || n.Kind == model.KindFolder {
		return true, nil
	}
	sets, err := s.aclSets([]string{n.ID})
	if err != nil {
		return false, err
	}
	return canViewNodeWithACL(n, actor, sets[n.ID]), nil
}

func (s *Service) GetActiveFor(id string, actor Actor) (Node, error) {
	n, err := s.GetActive(id)
	if err != nil {
		return Node{}, err
	}
	sets, err := s.aclSets([]string{id})
	if err != nil {
		return Node{}, err
	}
	if !canViewNodeWithACL(n, actor, sets[id]) {
		return Node{}, apperr.NotFound("文件或目录不存在")
	}
	s.applyAccess(&n, actor, sets[id])
	return n, nil
}

func (s *Service) canUploadInto(n Node, actor Actor) bool {
	if n.IsSecure() {
		return n.Kind == model.KindFolder && actor.CanUploadSecure()
	}
	return n.Kind == model.KindFolder && actor.CanEdit()
}

func ValidateName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return apperr.BadRequest("INVALID_NAME", "名称不能为空")
	}
	if name == "." || name == ".." {
		return apperr.BadRequest("INVALID_NAME", "名称无效")
	}
	if strings.HasSuffix(name, ".") {
		return apperr.BadRequest("INVALID_NAME", "名称不能以句点结尾")
	}
	if utf8.RuneCountInString(name) > 200 {
		return apperr.BadRequest("INVALID_NAME", "名称不能超过 200 个字符")
	}
	for _, r := range name {
		if r < 32 || strings.ContainsRune(`/\:*?"<>|`, r) {
			return apperr.BadRequest("INVALID_NAME", `名称不能包含 \ / : * ? " < > | 或控制字符`)
		}
	}
	return nil
}

func fileNameExt(name string) string {
	ext := path.Ext(name)
	if ext == name {
		return ""
	}
	return ext
}

func joinPath(parentPath, name string) string {
	if parentPath == "" || parentPath == "/" {
		return "/" + name
	}
	return parentPath + "/" + name
}

func (s *Service) GetActive(id string) (Node, error) {
	row := s.DB.QueryRow(`SELECT `+nodeColumns+` FROM nodes n
		LEFT JOIN versions v ON v.id = n.current_version_id
		WHERE n.id = ? AND n.deleted_at IS NULL`, id)
	n, err := scanNode(row)
	if err == sql.ErrNoRows {
		return Node{}, apperr.NotFound("文件或目录不存在")
	}
	if err != nil {
		return Node{}, err
	}
	n.Path, err = s.pathFromDB(id)
	return n, err
}

func (s *Service) pathFromDB(id string) (string, error) {
	cache := map[string]string{}
	var resolve func(string) (string, error)
	resolve = func(cur string) (string, error) {
		if cur == "" {
			return "/", nil
		}
		if p, ok := cache[cur]; ok {
			return p, nil
		}
		var name, parent string
		if err := s.DB.QueryRow(`SELECT name, parent_id FROM nodes WHERE id = ?`, cur).Scan(&name, &parent); err != nil {
			return "", err
		}
		pp, err := resolve(parent)
		if err != nil {
			return "", err
		}
		p := joinPath(pp, name)
		cache[cur] = p
		return p, nil
	}
	return resolve(id)
}

func (s *Service) pathTx(tx *sql.Tx, id string) (string, error) {
	var parts []string
	cur := id
	for i := 0; i < 100 && cur != ""; i++ {
		var name, parent string
		if err := tx.QueryRow(`SELECT name, parent_id FROM nodes WHERE id = ?`, cur).Scan(&name, &parent); err != nil {
			return "", err
		}
		parts = append(parts, name)
		cur = parent
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return "/" + strings.Join(parts, "/"), nil
}

func (s *Service) ensureParentTx(tx *sql.Tx, parentID string) (string, error) {
	if parentID == "" {
		return "/", nil
	}
	var kind string
	var deleted sql.NullString
	err := tx.QueryRow(`SELECT kind, deleted_at FROM nodes WHERE id = ?`, parentID).Scan(&kind, &deleted)
	if err == sql.ErrNoRows || deleted.Valid {
		return "", apperr.NotFound("上级目录不存在")
	}
	if err != nil {
		return "", err
	}
	if kind != model.KindFolder {
		return "", apperr.BadRequest("NOT_FOLDER", "上级项目不是目录")
	}
	path, err := s.pathTx(tx, parentID)
	if err != nil {
		return "", err
	}
	if strings.Count(path, "/") > 64 {
		return "", apperr.BadRequest("TOO_DEEP", "目录层级过深")
	}
	return path, nil
}

func (s *Service) List(parentID, query string, actor Actor) ([]Node, error) {
	query = strings.TrimSpace(query)
	var rows *sql.Rows
	var err error
	if query != "" {
		if utf8.RuneCountInString(query) > 100 {
			query = string([]rune(query)[:100])
		}
		like := "%" + escapeLike(query) + "%"
		rows, err = s.DB.Query(`SELECT `+nodeColumns+` FROM nodes n
			LEFT JOIN versions v ON v.id = n.current_version_id
			WHERE n.deleted_at IS NULL AND n.name LIKE ? ESCAPE '\'
			ORDER BY CASE n.kind WHEN 'folder' THEN 0 ELSE 1 END, n.name COLLATE NOCASE
			LIMIT 400`, like)
	} else {
		if parentID != "" {
			if _, err := s.GetActive(parentID); err != nil {
				return nil, err
			}
		}
		if parentID == "" {
			rows, err = s.DB.Query(`SELECT ` + nodeColumns + ` FROM nodes n
				LEFT JOIN versions v ON v.id = n.current_version_id
				WHERE n.deleted_at IS NULL AND n.space = 'main' AND n.parent_id = ''
				ORDER BY CASE n.kind WHEN 'folder' THEN 0 ELSE 1 END, n.name COLLATE NOCASE`)
		} else {
			rows, err = s.DB.Query(`SELECT `+nodeColumns+` FROM nodes n
				LEFT JOIN versions v ON v.id = n.current_version_id
				WHERE n.deleted_at IS NULL AND n.parent_id = ?
				ORDER BY CASE n.kind WHEN 'folder' THEN 0 ELSE 1 END, n.name COLLATE NOCASE`, parentID)
		}
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Node{}
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var secureFileIDs []string
	for _, n := range list {
		if n.IsSecure() && n.Kind == model.KindFile {
			secureFileIDs = append(secureFileIDs, n.ID)
		}
	}
	sets, err := s.aclSets(secureFileIDs)
	if err != nil {
		return nil, err
	}
	filtered := list[:0]
	for _, n := range list {
		if n.IsSecure() && n.Kind == model.KindFile && !canViewNodeWithACL(n, actor, sets[n.ID]) {
			continue
		}
		s.applyAccess(&n, actor, sets[n.ID])
		filtered = append(filtered, n)
	}
	list = filtered
	if query != "" && len(list) > 200 {
		list = list[:200]
	}
	paths := map[string]string{}
	for i := range list {
		p, err := s.pathForCached(list[i].ID, paths)
		if err != nil {
			return nil, err
		}
		list[i].Path = p
	}
	return list, nil
}

func (s *Service) pathForCached(id string, cache map[string]string) (string, error) {
	if p, ok := cache[id]; ok {
		return p, nil
	}
	ids := []string{}
	cur := id
	var result string
	for {
		if p, ok := cache[cur]; ok {
			result = p
			break
		}
		if cur == "" {
			result = "/"
			break
		}
		var name, parent string
		if err := s.DB.QueryRow(`SELECT name, parent_id FROM nodes WHERE id = ?`, cur).Scan(&name, &parent); err != nil {
			return "", err
		}
		ids = append(ids, cur)
		cache[cur] = name
		cur = parent
	}
	for i := len(ids) - 1; i >= 0; i-- {
		var name, parent string
		if err := s.DB.QueryRow(`SELECT name, parent_id FROM nodes WHERE id = ?`, ids[i]).Scan(&name, &parent); err != nil {
			return "", err
		}
		result = joinPath(result, name)
		cache[ids[i]] = result
	}
	cache[id] = result
	return result, nil
}

func (s *Service) AllFolders() ([]Node, error) {
	rows, err := s.DB.Query(`SELECT id, parent_id, name, space FROM nodes
		WHERE kind = 'folder' AND deleted_at IS NULL ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	paths := map[string]string{}
	var out []Node
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.ID, &n.ParentID, &n.Name, &n.Space); err != nil {
			rows.Close()
			return nil, err
		}
		n.Kind = model.KindFolder
		n.IsZoneRoot = n.Space == model.SpaceSecure && n.ParentID == ""
		out = append(out, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		p, err := s.pathForCached(out[i].ID, paths)
		if err != nil {
			return nil, err
		}
		out[i].Path = p
	}
	if out == nil {
		out = []Node{}
	}
	return out, nil
}

func (s *Service) ArchiveEntries(rootID string, actor Actor) ([]ArchiveEntry, error) {
	rows, err := s.DB.Query(`WITH RECURSIVE tree(id, name, parent_id, kind, current_version_id, space, rel) AS (
		SELECT id, name, parent_id, kind, current_version_id, space, name
		FROM nodes WHERE parent_id = ? AND deleted_at IS NULL
		UNION ALL
		SELECT n.id, n.name, n.parent_id, n.kind, n.current_version_id, n.space, tree.rel || '/' || n.name
		FROM nodes n JOIN tree ON n.parent_id = tree.id
		WHERE n.deleted_at IS NULL
	)
	SELECT tree.id, tree.kind, COALESCE(v.object_id, ''), COALESCE(v.size, 0), tree.rel, tree.space
	FROM tree LEFT JOIN versions v ON v.id = tree.current_version_id
	ORDER BY tree.rel COLLATE NOCASE`, rootID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ArchiveEntry
	var secureFileIDs []string
	for rows.Next() {
		var e ArchiveEntry
		var space string
		if err := rows.Scan(&e.NodeID, &e.Kind, &e.ObjectID, &e.Size, &e.Path, &space); err != nil {
			return nil, err
		}
		if kind := e.Kind; kind == model.KindFile && space == model.SpaceSecure {
			secureFileIDs = append(secureFileIDs, e.NodeID)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(secureFileIDs) == 0 {
		return out, nil
	}
	nodes := map[string]Node{}
	nodeRows, err := s.DB.Query(`SELECT `+nodeColumns+` FROM nodes n
		LEFT JOIN versions v ON v.id = n.current_version_id
		WHERE n.space = 'secure' AND n.kind = 'file' AND n.deleted_at IS NULL AND n.id IN (`+
		strings.TrimSuffix(strings.Repeat("?,", len(secureFileIDs)), ",")+`)`,
		stringArgs(secureFileIDs)...)
	if err != nil {
		return nil, err
	}
	for nodeRows.Next() {
		n, err := scanNode(nodeRows)
		if err != nil {
			nodeRows.Close()
			return nil, err
		}
		nodes[n.ID] = n
	}
	if err := nodeRows.Err(); err != nil {
		nodeRows.Close()
		return nil, err
	}
	nodeRows.Close()
	sets, err := s.aclSets(secureFileIDs)
	if err != nil {
		return nil, err
	}
	filtered := out[:0]
	for _, e := range out {
		if e.Kind == model.KindFile && e.NodeID != "" {
			if n, ok := nodes[e.NodeID]; ok && !canViewNodeWithACL(n, actor, sets[e.NodeID]) {
				continue
			}
		}
		filtered = append(filtered, e)
	}
	return filtered, nil
}

func (s *Service) Breadcrumbs(id string, actor Actor) ([]Breadcrumb, error) {
	if id == "" {
		return []Breadcrumb{{ID: "", Name: "全部项目", Space: model.SpaceMain}}, nil
	}
	n, err := s.GetActiveFor(id, actor)
	if err != nil {
		return nil, err
	}
	var parts []Breadcrumb
	cur := id
	visited := map[string]bool{}
	for cur != "" && !visited[cur] {
		visited[cur] = true
		var name, parent, kind, space string
		var deleted sql.NullString
		err := s.DB.QueryRow(`SELECT name, parent_id, kind, space, deleted_at FROM nodes WHERE id = ?`, cur).
			Scan(&name, &parent, &kind, &space, &deleted)
		if err != nil {
			return nil, err
		}
		if deleted.Valid {
			return nil, apperr.NotFound("目录不存在")
		}
		parts = append(parts, Breadcrumb{ID: cur, Name: name, Space: space})
		cur = parent
	}
	out := []Breadcrumb{}
	if n.Space == model.SpaceMain {
		out = append(out, Breadcrumb{ID: "", Name: "全部项目", Space: model.SpaceMain})
	}
	for i := len(parts) - 1; i >= 0; i-- {
		out = append(out, parts[i])
	}
	return out, nil
}

func (s *Service) CheckNewName(parentID, name string) (Node, bool, error) {
	name = strings.TrimSpace(name)
	if err := ValidateName(name); err != nil {
		return Node{}, false, err
	}
	space := model.SpaceMain
	if parentID != "" {
		parent, err := s.GetActive(parentID)
		if err != nil {
			return Node{}, false, err
		}
		space = parent.Space
	}
	var id string
	err := s.DB.QueryRow(`SELECT id FROM nodes WHERE space = ? AND parent_id = ? AND name = ? AND deleted_at IS NULL`,
		space, parentID, name).Scan(&id)
	if err == sql.ErrNoRows {
		return Node{}, false, nil
	}
	if err != nil {
		return Node{}, false, err
	}
	node, err := s.GetActive(id)
	return node, true, err
}

func (s *Service) CreateFolder(parentID, name string, actor Actor) (Node, error) {
	name = strings.TrimSpace(name)
	if err := ValidateName(name); err != nil {
		return Node{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return Node{}, err
	}
	defer tx.Rollback()
	parentPath, err := s.ensureParentTx(tx, parentID)
	if err != nil {
		return Node{}, err
	}
	space := model.SpaceMain
	if parentID != "" {
		if err := tx.QueryRow(`SELECT space FROM nodes WHERE id = ?`, parentID).Scan(&space); err != nil {
			return Node{}, err
		}
	}
	if space == model.SpaceSecure {
		if !actor.CanUploadSecure() {
			return Node{}, apperr.Forbidden("只有操作员及以上可以维护权限区")
		}
	} else if !actor.CanEdit() {
		return Node{}, apperr.Forbidden("当前账号没有编辑权限")
	}
	if space == model.SpaceMain && parentID == "" && name == "权限区" {
		return Node{}, apperr.Conflict("NAME_RESERVED", "该名称保留给权限区")
	}
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(1) FROM nodes WHERE space = ? AND parent_id = ? AND name = ? AND deleted_at IS NULL`,
		space, parentID, name).Scan(&exists); err != nil {
		return Node{}, err
	}
	if exists > 0 {
		return Node{}, apperr.Conflict("NAME_EXISTS", "同一目录下已存在同名项目")
	}
	id := model.NewID()
	now := model.Now()
	if _, err := tx.Exec(`INSERT INTO nodes(id, parent_id, name, kind, current_version_id, revision,
		created_at, created_by, updated_at, updated_by, space, created_by_id, secure_owner_role, visibility_mode)
		VALUES(?, ?, ?, 'folder', '', 0, ?, ?, ?, ?, ?, ?, ?, 'default')`,
		id, parentID, name, now, actor.DisplayName, now, actor.DisplayName, space, actor.ID, actor.Role); err != nil {
		return Node{}, err
	}
	path := joinPath(parentPath, name)
	if err := s.Audit.LogTx(tx, eventFor(actor, space, "folder_create", "folder", id, name, path, "新建目录")); err != nil {
		return Node{}, err
	}
	if err := tx.Commit(); err != nil {
		return Node{}, err
	}
	return Node{ID: id, ParentID: parentID, Name: name, Kind: model.KindFolder, Revision: 0,
		CreatedAt: now, CreatedBy: actor.DisplayName, UpdatedAt: now, UpdatedBy: actor.DisplayName,
		Path: path, Space: space, CreatedByID: actor.ID, SecureOwnerRole: actor.Role,
		VisibilityMode: "default", CanManage: canManageNode(Node{ParentID: parentID, Kind: model.KindFolder,
			Space: space, CreatedByID: actor.ID}, actor)}, nil
}

func (s *Service) UploadNew(parentID, name, note string, obj storage.Saved, actor Actor, vis Visibility) (UploadResult, error) {
	name = strings.TrimSpace(name)
	if err := ValidateName(name); err != nil {
		return UploadResult{}, err
	}
	note = limitNote(note)
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return UploadResult{}, err
	}
	defer tx.Rollback()
	parentPath, err := s.ensureParentTx(tx, parentID)
	if err != nil {
		return UploadResult{}, err
	}
	space := model.SpaceMain
	if parentID != "" {
		if err := tx.QueryRow(`SELECT space FROM nodes WHERE id = ?`, parentID).Scan(&space); err != nil {
			return UploadResult{}, err
		}
	}
	if space == model.SpaceSecure {
		if !actor.CanUploadSecure() {
			return UploadResult{}, apperr.Forbidden("只有操作员及以上可以上传到权限区")
		}
	} else if !actor.CanEdit() {
		return UploadResult{}, apperr.Forbidden("当前账号没有编辑权限")
	}
	vis, err = normalizeVisibility(tx, actor, space, vis)
	if err != nil {
		return UploadResult{}, err
	}
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(1) FROM nodes WHERE space = ? AND parent_id = ? AND name = ? AND deleted_at IS NULL`,
		space, parentID, name).Scan(&exists); err != nil {
		return UploadResult{}, err
	}
	if exists > 0 {
		return UploadResult{}, apperr.Conflict("NAME_EXISTS", "同一目录下已存在同名项目，请选择替换为新版本或改名")
	}
	nodeID := model.NewID()
	versionID := model.NewID()
	now := model.Now()
	path := joinPath(parentPath, name)
	if _, err := tx.Exec(`INSERT INTO nodes(id, parent_id, name, kind, current_version_id, revision,
		created_at, created_by, updated_at, updated_by, space, created_by_id, secure_owner_role, visibility_mode)
		VALUES(?, ?, ?, 'file', ?, 1, ?, ?, ?, ?, ?, ?, ?, ?)`,
		nodeID, parentID, name, versionID, now, actor.DisplayName, now, actor.DisplayName,
		space, actor.ID, actor.Role, vis.Mode); err != nil {
		if isUniqueError(err) {
			return UploadResult{}, apperr.Conflict("NAME_EXISTS", "同一目录下已存在同名项目")
		}
		return UploadResult{}, err
	}
	if _, err := tx.Exec(`INSERT INTO objects(id, size, sha256, created_at) VALUES(?, ?, ?, ?)`,
		obj.ID, obj.Size, obj.SHA256, now); err != nil {
		return UploadResult{}, err
	}
	if _, err := tx.Exec(`INSERT INTO versions(id, node_id, version_no, object_id, size, sha256, note,
		created_at, created_by) VALUES(?, ?, 1, ?, ?, ?, ?, ?, ?)`,
		versionID, nodeID, obj.ID, obj.Size, obj.SHA256, note, now, actor.ID); err != nil {
		return UploadResult{}, err
	}
	if err := saveVisibilityTx(tx, nodeID, actor, vis); err != nil {
		return UploadResult{}, err
	}
	if err := s.Audit.LogTx(tx, eventFor(actor, space, "upload_new", "file", nodeID, name, path,
		fmt.Sprintf("初始版本，%d 字节，SHA256 %s", obj.Size, obj.SHA256))); err != nil {
		return UploadResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return UploadResult{}, err
	}
	node := Node{ID: nodeID, ParentID: parentID, Name: name, Kind: model.KindFile,
		CurrentVersionID: versionID, Revision: 1, Size: obj.Size, SHA256: obj.SHA256,
		VersionNo: 1, CreatedAt: now, CreatedBy: actor.DisplayName, UpdatedAt: now,
		UpdatedBy: actor.DisplayName, Path: path, Space: space, VisibilityMode: vis.Mode,
		CreatedByID: actor.ID, SecureOwnerRole: actor.Role}
	s.applyAccess(&node, actor, aclSet{})
	return UploadResult{
		Node: node,
		Version: Version{ID: versionID, NodeID: nodeID, VersionNo: 1, ObjectID: obj.ID,
			Size: obj.Size, SHA256: obj.SHA256, Note: note, CreatedAt: now, CreatedBy: actor.ID,
			CreatedByName: actor.DisplayName, IsCurrent: true},
	}, nil
}

func (s *Service) UploadVersion(nodeID, baseVersionID, note string, obj storage.Saved, actor Actor) (UploadResult, error) {
	note = limitNote(note)
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return UploadResult{}, err
	}
	defer tx.Rollback()
	n, err := s.getNodeTx(tx, nodeID)
	if err != nil {
		return UploadResult{}, err
	}
	if n.Kind != model.KindFile {
		return UploadResult{}, apperr.BadRequest("NOT_FILE", "只有文件可以上传新版本")
	}
	if !canManageNode(n, actor) {
		return UploadResult{}, apperr.Forbidden("没有权限更新该文件")
	}
	if baseVersionID == "" || baseVersionID != n.CurrentVersionID {
		return UploadResult{}, apperr.Conflict("VERSION_CONFLICT", "文件已被他人更新，请刷新版本信息后重试")
	}
	nextNo := n.Revision + 1
	versionID := model.NewID()
	now := model.Now()
	if _, err := tx.Exec(`INSERT INTO objects(id, size, sha256, created_at) VALUES(?, ?, ?, ?)`,
		obj.ID, obj.Size, obj.SHA256, now); err != nil {
		return UploadResult{}, err
	}
	if _, err := tx.Exec(`INSERT INTO versions(id, node_id, version_no, object_id, size, sha256, note,
		created_at, created_by) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		versionID, nodeID, nextNo, obj.ID, obj.Size, obj.SHA256, note, now, actor.ID); err != nil {
		return UploadResult{}, err
	}
	if _, err := tx.Exec(`UPDATE nodes SET current_version_id = ?, revision = ?, updated_at = ?, updated_by = ?
		WHERE id = ? AND deleted_at IS NULL`, versionID, nextNo, now, actor.DisplayName, nodeID); err != nil {
		return UploadResult{}, err
	}
	detail := fmt.Sprintf("版本 V%d → V%d，%d 字节，SHA256 %s", nextNo-1, nextNo, obj.Size, obj.SHA256)
	if err := s.Audit.LogTx(tx, eventFor(actor, n.Space, "upload_version", "file", nodeID, n.Name, n.Path, detail)); err != nil {
		return UploadResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return UploadResult{}, err
	}
	n.CurrentVersionID = versionID
	n.Revision = nextNo
	n.Size = obj.Size
	n.SHA256 = obj.SHA256
	n.VersionNo = nextNo
	n.UpdatedAt = now
	n.UpdatedBy = actor.DisplayName
	s.applyAccess(&n, actor, aclSet{})
	return UploadResult{
		Node: n,
		Version: Version{ID: versionID, NodeID: nodeID, VersionNo: nextNo, ObjectID: obj.ID,
			Size: obj.Size, SHA256: obj.SHA256, Note: note, CreatedAt: now, CreatedBy: actor.ID,
			CreatedByName: actor.DisplayName, IsCurrent: true},
	}, nil
}

func (s *Service) getNodeTx(tx *sql.Tx, id string) (Node, error) {
	row := tx.QueryRow(`SELECT `+nodeColumns+` FROM nodes n
		LEFT JOIN versions v ON v.id = n.current_version_id
		WHERE n.id = ? AND n.deleted_at IS NULL`, id)
	n, err := scanNode(row)
	if err == sql.ErrNoRows {
		return Node{}, apperr.NotFound("文件或目录不存在")
	}
	if err != nil {
		return Node{}, err
	}
	n.Path, err = s.pathTx(tx, id)
	return n, err
}

func (s *Service) Rename(nodeID, newName string, actor Actor) (Node, error) {
	newName = strings.TrimSpace(newName)
	if err := ValidateName(newName); err != nil {
		return Node{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return Node{}, err
	}
	defer tx.Rollback()
	n, err := s.getNodeTx(tx, nodeID)
	if err != nil {
		return Node{}, err
	}
	if n.IsZoneRoot {
		return Node{}, apperr.Forbidden("权限区名称不能修改")
	}
	if !canManageNode(n, actor) {
		return Node{}, apperr.Forbidden("没有权限修改该名称")
	}
	if n.Name == newName {
		s.applyAccess(&n, actor, aclSet{})
		return n, nil
	}
	if n.Kind == model.KindFile && fileNameExt(n.Name) != fileNameExt(newName) {
		return Node{}, apperr.BadRequest("EXTENSION_IMMUTABLE", "重命名不能修改文件后缀")
	}
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(1) FROM nodes WHERE space = ? AND parent_id = ? AND name = ? AND deleted_at IS NULL AND id <> ?`,
		n.Space, n.ParentID, newName, nodeID).Scan(&exists); err != nil {
		return Node{}, err
	}
	if exists > 0 {
		return Node{}, apperr.Conflict("NAME_EXISTS", "同一目录下已存在同名项目")
	}
	now := model.Now()
	if _, err := tx.Exec(`UPDATE nodes SET name = ?, updated_at = ?, updated_by = ? WHERE id = ?`,
		newName, now, actor.DisplayName, nodeID); err != nil {
		return Node{}, err
	}
	newPath := joinPath(parentPathOf(n.Path), newName)
	if err := s.Audit.LogTx(tx, eventFor(actor, n.Space, "rename", n.Kind, nodeID, newName, newPath,
		"原名："+n.Name)); err != nil {
		return Node{}, err
	}
	if err := tx.Commit(); err != nil {
		return Node{}, err
	}
	n.Name = newName
	n.Path = newPath
	n.UpdatedAt = now
	n.UpdatedBy = actor.DisplayName
	s.applyAccess(&n, actor, aclSet{})
	return n, nil
}

func parentPathOf(path string) string {
	idx := strings.LastIndex(path, "/")
	if idx <= 0 {
		return "/"
	}
	return path[:idx]
}

func (s *Service) Move(nodeID, newParentID string, actor Actor) (Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return Node{}, err
	}
	defer tx.Rollback()
	n, err := s.getNodeTx(tx, nodeID)
	if err != nil {
		return Node{}, err
	}
	if n.IsZoneRoot {
		return Node{}, apperr.Forbidden("权限区不能移动")
	}
	if !canManageNode(n, actor) {
		return Node{}, apperr.Forbidden("没有权限移动该项目")
	}
	newParentPath, err := s.ensureParentTx(tx, newParentID)
	if err != nil {
		return Node{}, err
	}
	targetSpace := model.SpaceMain
	if newParentID != "" {
		if err := tx.QueryRow(`SELECT space FROM nodes WHERE id = ?`, newParentID).Scan(&targetSpace); err != nil {
			return Node{}, err
		}
	}
	if targetSpace != n.Space {
		return Node{}, apperr.BadRequest("CROSS_SPACE_MOVE", "不能移动全部项目和权限区之间")
	}
	if newParentID == nodeID {
		return Node{}, apperr.BadRequest("INVALID_MOVE", "不能将目录移动到自身")
	}
	cur := newParentID
	for i := 0; i < 100 && cur != ""; i++ {
		if cur == nodeID {
			return Node{}, apperr.BadRequest("INVALID_MOVE", "不能将目录移动到自己的子目录")
		}
		if err := tx.QueryRow(`SELECT parent_id FROM nodes WHERE id = ?`, cur).Scan(&cur); err != nil {
			return Node{}, err
		}
	}
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(1) FROM nodes WHERE space = ? AND parent_id = ? AND name = ? AND deleted_at IS NULL AND id <> ?`,
		n.Space, newParentID, n.Name, nodeID).Scan(&exists); err != nil {
		return Node{}, err
	}
	if exists > 0 {
		return Node{}, apperr.Conflict("NAME_EXISTS", "目标目录已有同名项目")
	}
	now := model.Now()
	if _, err := tx.Exec(`UPDATE nodes SET parent_id = ?, updated_at = ?, updated_by = ? WHERE id = ?`,
		newParentID, now, actor.DisplayName, nodeID); err != nil {
		return Node{}, err
	}
	newPath := joinPath(newParentPath, n.Name)
	if err := s.Audit.LogTx(tx, eventFor(actor, n.Space, "move", n.Kind, nodeID, n.Name, newPath,
		"原位置："+n.Path)); err != nil {
		return Node{}, err
	}
	if err := tx.Commit(); err != nil {
		return Node{}, err
	}
	n.ParentID = newParentID
	n.Path = newPath
	n.UpdatedAt = now
	n.UpdatedBy = actor.DisplayName
	s.applyAccess(&n, actor, aclSet{})
	return n, nil
}

func (s *Service) SoftDelete(nodeID string, actor Actor) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n, err := s.getNodeTx(tx, nodeID)
	if err != nil {
		return 0, err
	}
	var acl aclSet
	if n.IsSecure() && n.Kind == model.KindFile {
		sets, err := s.aclSets([]string{n.ID})
		if err != nil {
			return 0, err
		}
		acl = sets[n.ID]
		if !canViewNodeWithACL(n, actor, acl) {
			return 0, apperr.NotFound("文件或目录不存在")
		}
	}
	if !canDeleteNode(n, actor, acl) {
		if n.IsSecure() {
			return 0, apperr.Forbidden("权限区文件夹只能由主机账号删除")
		}
		return 0, apperr.Forbidden("当前账号没有编辑权限")
	}
	batch := model.NewID()
	now := model.Now()
	res, err := tx.Exec(`WITH RECURSIVE sub(id) AS (
			SELECT id FROM nodes WHERE id = ? AND deleted_at IS NULL
			UNION ALL
			SELECT c.id FROM nodes c JOIN sub s ON c.parent_id = s.id WHERE c.deleted_at IS NULL
		)
		UPDATE nodes SET deleted_at = ?, deleted_by = ?, delete_batch = ?
		WHERE id IN (SELECT id FROM sub)`, nodeID, now, actor.DisplayName, batch)
	if err != nil {
		return 0, err
	}
	count, _ := res.RowsAffected()
	if err := s.Audit.LogTx(tx, eventFor(actor, n.Space, "delete", n.Kind, nodeID, n.Name, n.Path,
		fmt.Sprintf("移入回收站，共 %d 项", count))); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(count), nil
}

func (s *Service) ListRecycle(actor Actor) ([]Node, error) {
	rows, err := s.DB.Query(`SELECT `+nodeColumns+` FROM nodes n
		LEFT JOIN versions v ON v.id = n.current_version_id
		WHERE n.deleted_at IS NOT NULL
		AND (n.space = 'main' OR ? = 1)
		AND NOT EXISTS (
			SELECT 1 FROM nodes p WHERE p.id = n.parent_id
			AND p.delete_batch = n.delete_batch AND p.deleted_at IS NOT NULL
		)
		ORDER BY n.deleted_at DESC`, boolInt(actor.IsOwner))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Node{}
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	paths := map[string]string{}
	for i := range list {
		p, err := s.pathForCached(list[i].ID, paths)
		if err != nil {
			return nil, err
		}
		list[i].Path = p
	}
	return list, nil
}

func (s *Service) RestoreBatch(batch string, actor Actor) (int, error) {
	if !actor.CanAccessAdmin() {
		return 0, apperr.Forbidden("只有管理员或操作员可以恢复回收站")
	}
	if batch == "" {
		return 0, apperr.BadRequest("INVALID_BATCH", "回收站批次无效")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id, parent_id, name, space FROM nodes WHERE delete_batch = ? AND deleted_at IS NOT NULL`, batch)
	if err != nil {
		return 0, err
	}
	type item struct {
		id, parent, name, space string
	}
	var items []item
	inBatch := map[string]bool{}
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.parent, &it.name, &it.space); err != nil {
			rows.Close()
			return 0, err
		}
		if it.space == model.SpaceSecure && !actor.IsOwner {
			rows.Close()
			return 0, apperr.Forbidden("权限区回收站只能由主机账号恢复")
		}
		items = append(items, it)
		inBatch[it.id] = true
	}
	rows.Close()
	if len(items) == 0 {
		return 0, apperr.NotFound("回收站项目不存在")
	}
	var roots []item
	for _, it := range items {
		if !inBatch[it.parent] {
			roots = append(roots, it)
		}
	}
	for _, root := range roots {
		if root.parent != "" {
			var kind string
			var deleted sql.NullString
			err := tx.QueryRow(`SELECT kind, deleted_at FROM nodes WHERE id = ?`, root.parent).Scan(&kind, &deleted)
			if err == sql.ErrNoRows || deleted.Valid || kind != model.KindFolder {
				return 0, apperr.Conflict("PARENT_MISSING", "原上级目录不存在，请先恢复上级目录")
			}
			if err != nil {
				return 0, err
			}
		}
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(1) FROM nodes WHERE parent_id = ? AND name = ? AND deleted_at IS NULL`,
			root.parent, root.name).Scan(&exists); err != nil {
			return 0, err
		}
		if exists > 0 {
			return 0, apperr.Conflict("NAME_EXISTS", "原位置已有同名项目，无法恢复："+root.name)
		}
	}
	now := model.Now()
	if _, err := tx.Exec(`UPDATE nodes SET deleted_at = NULL, deleted_by = NULL, delete_batch = NULL,
		updated_at = ?, updated_by = ? WHERE delete_batch = ?`, now, actor.DisplayName, batch); err != nil {
		return 0, err
	}
	for _, root := range roots {
		path, _ := s.pathTx(tx, root.id)
		if err := s.Audit.LogTx(tx, eventFor(actor, root.space, "recycle_restore", "file", root.id, root.name, path,
			fmt.Sprintf("从回收站恢复，共 %d 项", len(items)))); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(items), nil
}

func (s *Service) PurgeBatch(batch string, actor Actor) (int, error) {
	if !actor.CanAccessAdmin() {
		return 0, apperr.Forbidden("只有管理员或操作员可以永久清理")
	}
	if batch == "" {
		return 0, apperr.BadRequest("INVALID_BATCH", "回收站批次无效")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Store.Guard.Lock()
	defer s.Store.Guard.Unlock()

	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var rootID, rootName, rootSpace string
	err = tx.QueryRow(`SELECT id, name, space FROM nodes WHERE delete_batch = ? AND deleted_at IS NOT NULL LIMIT 1`, batch).
		Scan(&rootID, &rootName, &rootSpace)
	if err == sql.ErrNoRows {
		return 0, apperr.NotFound("回收站项目不存在")
	}
	if err != nil {
		return 0, err
	}
	if rootSpace == model.SpaceSecure && !actor.IsOwner {
		return 0, apperr.Forbidden("权限区回收站只能由主机账号清理")
	}
	path, _ := s.pathTx(tx, rootID)
	var count int
	if err := tx.QueryRow(`SELECT COUNT(1) FROM nodes WHERE delete_batch = ?`, batch).Scan(&count); err != nil {
		return 0, err
	}
	rows, err := tx.Query(`SELECT DISTINCT object_id FROM versions WHERE node_id IN
		(SELECT id FROM nodes WHERE delete_batch = ?)`, batch)
	if err != nil {
		return 0, err
	}
	var objectIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		objectIDs = append(objectIDs, id)
	}
	rows.Close()
	if _, err := tx.Exec(`DELETE FROM versions WHERE node_id IN (SELECT id FROM nodes WHERE delete_batch = ?)`, batch); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM nodes WHERE delete_batch = ?`, batch); err != nil {
		return 0, err
	}
	var removeObjects []string
	for _, objectID := range objectIDs {
		var refs int
		if err := tx.QueryRow(`SELECT COUNT(1) FROM versions WHERE object_id = ?`, objectID).Scan(&refs); err != nil {
			return 0, err
		}
		if refs == 0 {
			if _, err := tx.Exec(`DELETE FROM objects WHERE id = ?`, objectID); err != nil {
				return 0, err
			}
			removeObjects = append(removeObjects, objectID)
		}
	}
	if err := s.Audit.LogTx(tx, eventFor(actor, rootSpace, "recycle_purge", "file", rootID, rootName, path,
		fmt.Sprintf("永久删除，共 %d 项", count))); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	for _, objectID := range removeObjects {
		_ = s.Store.DeleteObject(objectID)
	}
	return count, nil
}

func (s *Service) PurgeAll(actor Actor) (int, error) {
	if !actor.CanAccessAdmin() {
		return 0, apperr.Forbidden("只有管理员或操作员可以永久清理")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Store.Guard.Lock()
	defer s.Store.Guard.Unlock()

	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	scope := `deleted_at IS NOT NULL`
	if !actor.IsOwner {
		scope += ` AND space = 'main'`
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(1) FROM nodes WHERE ` + scope).Scan(&count); err != nil {
		return 0, err
	}
	if count == 0 {
		return 0, nil
	}
	rows, err := tx.Query(`SELECT DISTINCT object_id FROM versions WHERE node_id IN
		(SELECT id FROM nodes WHERE ` + scope + `)`)
	if err != nil {
		return 0, err
	}
	var objectIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		objectIDs = append(objectIDs, id)
	}
	rows.Close()
	if _, err := tx.Exec(`DELETE FROM versions WHERE node_id IN (SELECT id FROM nodes WHERE ` + scope + `)`); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM nodes WHERE ` + scope); err != nil {
		return 0, err
	}
	var removeObjects []string
	for _, objectID := range objectIDs {
		var refs int
		if err := tx.QueryRow(`SELECT COUNT(1) FROM versions WHERE object_id = ?`, objectID).Scan(&refs); err != nil {
			return 0, err
		}
		if refs == 0 {
			if _, err := tx.Exec(`DELETE FROM objects WHERE id = ?`, objectID); err != nil {
				return 0, err
			}
			removeObjects = append(removeObjects, objectID)
		}
	}
	if err := s.Audit.LogTx(tx, eventFor(actor, model.SpaceMain, "recycle_purge_all", "recycle", "", "回收站", "/",
		fmt.Sprintf("清空回收站，共 %d 项", count))); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	for _, objectID := range removeObjects {
		_ = s.Store.DeleteObject(objectID)
	}
	return count, nil
}

func (s *Service) ListVersions(nodeID string, actor Actor) ([]Version, error) {
	n, err := s.GetActiveFor(nodeID, actor)
	if err != nil {
		return nil, err
	}
	currentID := n.CurrentVersionID
	rows, err := s.DB.Query(`SELECT v.id, v.node_id, v.version_no, v.object_id, v.size, v.sha256, v.note,
		v.created_at, v.created_by, COALESCE(u.display_name, v.created_by)
		FROM versions v LEFT JOIN users u ON u.id = v.created_by
		WHERE v.node_id = ? ORDER BY v.version_no DESC`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Version{}
	for rows.Next() {
		var ver Version
		if err := rows.Scan(&ver.ID, &ver.NodeID, &ver.VersionNo, &ver.ObjectID, &ver.Size,
			&ver.SHA256, &ver.Note, &ver.CreatedAt, &ver.CreatedBy, &ver.CreatedByName); err != nil {
			return nil, err
		}
		ver.IsCurrent = ver.ID == currentID
		out = append(out, ver)
	}
	return out, rows.Err()
}

func (s *Service) RestoreVersion(nodeID, versionID, baseVersionID string, actor Actor) (UploadResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return UploadResult{}, err
	}
	defer tx.Rollback()
	n, err := s.getNodeTx(tx, nodeID)
	if err != nil {
		return UploadResult{}, err
	}
	if n.Kind != model.KindFile {
		return UploadResult{}, apperr.BadRequest("NOT_FILE", "只有文件可以恢复历史版本")
	}
	if !canManageNode(n, actor) {
		return UploadResult{}, apperr.Forbidden("没有权限恢复该文件的历史版本")
	}
	if baseVersionID == "" || baseVersionID != n.CurrentVersionID {
		return UploadResult{}, apperr.Conflict("VERSION_CONFLICT", "文件已被他人更新，请刷新后重试")
	}
	var src Version
	err = tx.QueryRow(`SELECT id, node_id, version_no, object_id, size, sha256, note, created_at, created_by
		FROM versions WHERE id = ? AND node_id = ?`, versionID, nodeID).
		Scan(&src.ID, &src.NodeID, &src.VersionNo, &src.ObjectID, &src.Size, &src.SHA256, &src.Note, &src.CreatedAt, &src.CreatedBy)
	if err == sql.ErrNoRows {
		return UploadResult{}, apperr.NotFound("历史版本不存在")
	}
	if err != nil {
		return UploadResult{}, err
	}
	if !s.Store.Exists(src.ObjectID) {
		return UploadResult{}, apperr.Internal("历史版本文件缺失，请检查存储或从备份恢复")
	}
	nextNo := n.Revision + 1
	newID := model.NewID()
	now := model.Now()
	note := fmt.Sprintf("恢复自版本 V%d", src.VersionNo)
	if _, err := tx.Exec(`INSERT INTO versions(id, node_id, version_no, object_id, size, sha256, note,
		created_at, created_by) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		newID, nodeID, nextNo, src.ObjectID, src.Size, src.SHA256, note, now, actor.ID); err != nil {
		return UploadResult{}, err
	}
	if _, err := tx.Exec(`UPDATE nodes SET current_version_id = ?, revision = ?, updated_at = ?, updated_by = ?
		WHERE id = ? AND deleted_at IS NULL`, newID, nextNo, now, actor.DisplayName, nodeID); err != nil {
		return UploadResult{}, err
	}
	if err := s.Audit.LogTx(tx, eventFor(actor, n.Space, "version_restore", "file", nodeID, n.Name, n.Path,
		fmt.Sprintf("将 V%d 恢复为最新版本 V%d", src.VersionNo, nextNo))); err != nil {
		return UploadResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return UploadResult{}, err
	}
	n.CurrentVersionID = newID
	n.Revision = nextNo
	n.Size = src.Size
	n.SHA256 = src.SHA256
	n.VersionNo = nextNo
	n.UpdatedAt = now
	n.UpdatedBy = actor.DisplayName
	s.applyAccess(&n, actor, aclSet{})
	return UploadResult{
		Node: n,
		Version: Version{ID: newID, NodeID: nodeID, VersionNo: nextNo, ObjectID: src.ObjectID,
			Size: src.Size, SHA256: src.SHA256, Note: note, CreatedAt: now, CreatedBy: actor.ID,
			CreatedByName: actor.DisplayName, IsCurrent: true},
	}, nil
}

func (s *Service) PurgeVersion(nodeID, versionID string, actor Actor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Store.Guard.Lock()
	defer s.Store.Guard.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	n, err := s.getNodeTx(tx, nodeID)
	if err != nil {
		return err
	}
	if n.IsSecure() {
		if !actor.IsOwner {
			return apperr.Forbidden("权限区历史版本只能由主机账号清理")
		}
	} else if !actor.IsAdmin() {
		return apperr.Forbidden("只有管理员可以清理历史版本")
	}
	var objectID string
	var versionNo int
	err = tx.QueryRow(`SELECT object_id, version_no FROM versions WHERE id = ? AND node_id = ?`, versionID, nodeID).
		Scan(&objectID, &versionNo)
	if err == sql.ErrNoRows {
		return apperr.NotFound("历史版本不存在")
	}
	if err != nil {
		return err
	}
	if n.CurrentVersionID == versionID {
		return apperr.Conflict("CURRENT_VERSION", "不能清理当前最新版本")
	}
	if _, err := tx.Exec(`DELETE FROM versions WHERE id = ?`, versionID); err != nil {
		return err
	}
	var refs int
	if err := tx.QueryRow(`SELECT COUNT(1) FROM versions WHERE object_id = ?`, objectID).Scan(&refs); err != nil {
		return err
	}
	removeObject := refs == 0
	if removeObject {
		if _, err := tx.Exec(`DELETE FROM objects WHERE id = ?`, objectID); err != nil {
			return err
		}
	}
	if err := s.Audit.LogTx(tx, eventFor(actor, n.Space, "version_purge", "file", nodeID, n.Name, n.Path,
		fmt.Sprintf("清理历史版本 V%d", versionNo))); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if removeObject {
		_ = s.Store.DeleteObject(objectID)
	}
	return nil
}

func (s *Service) ResolveVersion(nodeID, versionID string, actor Actor) (Node, Version, error) {
	n, err := s.GetActiveFor(nodeID, actor)
	if err != nil {
		return Node{}, Version{}, err
	}
	if n.Kind != model.KindFile {
		return Node{}, Version{}, apperr.BadRequest("NOT_FILE", "该对象不是文件")
	}
	if versionID == "" {
		versionID = n.CurrentVersionID
	}
	var v Version
	var createdBy string
	err = s.DB.QueryRow(`SELECT v.id, v.node_id, v.version_no, v.object_id, v.size, v.sha256, v.note,
		v.created_at, v.created_by, COALESCE(u.display_name, v.created_by)
		FROM versions v LEFT JOIN users u ON u.id = v.created_by
		WHERE v.id = ? AND v.node_id = ?`, versionID, nodeID).
		Scan(&v.ID, &v.NodeID, &v.VersionNo, &v.ObjectID, &v.Size, &v.SHA256, &v.Note,
			&v.CreatedAt, &v.CreatedBy, &createdBy)
	if err == sql.ErrNoRows {
		return Node{}, Version{}, apperr.NotFound("文件版本不存在")
	}
	if err != nil {
		return Node{}, Version{}, err
	}
	v.CreatedByName = createdBy
	v.IsCurrent = versionID == n.CurrentVersionID
	return n, v, nil
}

func (s *Service) NodeAudit(nodeID string, actor Actor) ([]audit.Row, error) {
	if _, err := s.GetActiveFor(nodeID, actor); err != nil {
		return nil, err
	}
	return s.Audit.ForTarget(nodeID, 100)
}

func (s *Service) Stats() (Stats, error) {
	var st Stats
	if err := s.DB.QueryRow(`SELECT COUNT(1) FROM nodes WHERE kind = 'file' AND deleted_at IS NULL`).Scan(&st.Files); err != nil {
		return st, err
	}
	if err := s.DB.QueryRow(`SELECT COUNT(1) FROM nodes WHERE kind = 'folder' AND deleted_at IS NULL`).Scan(&st.Folders); err != nil {
		return st, err
	}
	if err := s.DB.QueryRow(`SELECT COUNT(1), COALESCE(SUM(size), 0) FROM versions`).Scan(&st.Versions, &st.ObjectBytes); err != nil {
		return st, err
	}
	free, err := s.Store.FreeSpace()
	if err == nil {
		st.FreeSpace = free
	}
	return st, nil
}

func (s *Service) GC() (int, error) {
	removedTemp, _ := s.Store.CleanupTemp(24 * time.Hour)
	rows, err := s.DB.Query(`SELECT DISTINCT object_id FROM versions`)
	if err != nil {
		return removedTemp, err
	}
	refs := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return removedTemp, err
		}
		refs[id] = true
	}
	rows.Close()
	ids, err := s.Store.ListObjectIDs()
	if err != nil {
		return removedTemp, err
	}
	removedOrphan := 0
	for _, id := range ids {
		if refs[id] {
			continue
		}
		info, err := os.Stat(s.Store.ObjectPath(id))
		if err != nil || time.Since(info.ModTime()) < 24*time.Hour {
			continue
		}
		s.Store.Guard.Lock()
		var stillRefs int
		_ = s.DB.QueryRow(`SELECT COUNT(1) FROM versions WHERE object_id = ?`, id).Scan(&stillRefs)
		if stillRefs == 0 {
			if err := s.Store.DeleteObject(id); err == nil {
				removedOrphan++
			}
		}
		s.Store.Guard.Unlock()
	}
	return removedTemp + removedOrphan, nil
}

func normalizeVisibility(tx *sql.Tx, actor Actor, space string, vis Visibility) (Visibility, error) {
	if space != model.SpaceSecure {
		return Visibility{Mode: "default"}, nil
	}
	mode := strings.TrimSpace(vis.Mode)
	if mode == "" {
		mode = "default"
	}
	if mode != "default" && mode != "custom" {
		return Visibility{}, apperr.BadRequest("INVALID_VISIBILITY", "可见范围设置无效")
	}
	if mode == "default" {
		return Visibility{Mode: "default"}, nil
	}
	roles := uniqueStrings(vis.Roles)
	users := uniqueStrings(vis.Users)
	for _, role := range roles {
		if !model.ValidRole(role) {
			return Visibility{}, apperr.BadRequest("INVALID_VISIBILITY", "可见角色无效")
		}
	}
	for _, userID := range users {
		var count int
		if err := tx.QueryRow(`SELECT COUNT(1) FROM users WHERE id = ? AND disabled = 0 AND deleted_at = ''`,
			userID).Scan(&count); err != nil {
			return Visibility{}, err
		}
		if count == 0 {
			return Visibility{}, apperr.BadRequest("INVALID_VISIBILITY", "可见用户不存在或已停用")
		}
	}
	if len(roles) == 0 && len(users) == 0 {
		return Visibility{}, apperr.BadRequest("EMPTY_VISIBILITY", "请至少选择一个权限组或用户")
	}
	return Visibility{Mode: mode, Roles: roles, Users: users}, nil
}

func saveVisibilityTx(tx *sql.Tx, nodeID string, actor Actor, vis Visibility) error {
	if _, err := tx.Exec(`DELETE FROM node_acl WHERE node_id = ?`, nodeID); err != nil {
		return err
	}
	if vis.Mode != "custom" {
		return nil
	}
	now := model.Now()
	for _, role := range vis.Roles {
		if _, err := tx.Exec(`INSERT INTO node_acl(node_id, principal_type, principal_id, created_at, created_by)
			VALUES(?, 'role', ?, ?, ?)`, nodeID, role, now, actor.DisplayName); err != nil {
			return err
		}
	}
	for _, userID := range vis.Users {
		if _, err := tx.Exec(`INSERT INTO node_acl(node_id, principal_type, principal_id, created_at, created_by)
			VALUES(?, 'user', ?, ?, ?)`, nodeID, userID, now, actor.DisplayName); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) GetVisibility(nodeID string, actor Actor) (VisibilityInfo, error) {
	n, err := s.GetActive(nodeID)
	if err != nil {
		return VisibilityInfo{}, err
	}
	if !n.IsSecure() || n.Kind != model.KindFile {
		return VisibilityInfo{}, apperr.BadRequest("NOT_SECURE_FILE", "该文件不在权限区")
	}
	if !actor.CanUploadSecure() || (!actor.IsOwner && n.CreatedByID != actor.ID) {
		visible, err := s.CanViewNode(n, actor)
		if err != nil {
			return VisibilityInfo{}, err
		}
		if !visible {
			return VisibilityInfo{}, apperr.NotFound("文件或目录不存在")
		}
		return VisibilityInfo{}, apperr.Forbidden("只有上传者或主机账号可以查看可见范围")
	}
	info := VisibilityInfo{Mode: n.VisibilityMode, Roles: []string{}, Users: []VisibilityUser{}}
	if info.Mode == "" {
		info.Mode = "default"
	}
	rows, err := s.DB.Query(`SELECT principal_type, principal_id FROM node_acl
		WHERE node_id = ? ORDER BY principal_type, principal_id`, nodeID)
	if err != nil {
		return VisibilityInfo{}, err
	}
	var userIDs []string
	for rows.Next() {
		var kind, principal string
		if err := rows.Scan(&kind, &principal); err != nil {
			rows.Close()
			return VisibilityInfo{}, err
		}
		if kind == "role" {
			info.Roles = append(info.Roles, principal)
		} else {
			userIDs = append(userIDs, principal)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return VisibilityInfo{}, err
	}
	for _, userID := range userIDs {
		var user VisibilityUser
		user.ID = userID
		err := s.DB.QueryRow(`SELECT COALESCE(display_name, id), COALESCE(role, '') FROM users WHERE id = ?`, userID).
			Scan(&user.DisplayName, &user.Role)
		if err == sql.ErrNoRows {
			user.DisplayName = userID
		} else if err != nil {
			return VisibilityInfo{}, err
		}
		info.Users = append(info.Users, user)
	}
	return info, nil
}

func (s *Service) SetVisibility(nodeID string, actor Actor, vis Visibility) (VisibilityInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return VisibilityInfo{}, err
	}
	defer tx.Rollback()
	n, err := s.getNodeTx(tx, nodeID)
	if err != nil {
		return VisibilityInfo{}, err
	}
	if !n.IsSecure() || n.Kind != model.KindFile {
		return VisibilityInfo{}, apperr.BadRequest("NOT_SECURE_FILE", "该文件不在权限区")
	}
	if !actor.CanUploadSecure() || (!actor.IsOwner && n.CreatedByID != actor.ID) {
		visible, err := s.CanViewNode(n, actor)
		if err != nil {
			return VisibilityInfo{}, err
		}
		if !visible {
			return VisibilityInfo{}, apperr.NotFound("文件或目录不存在")
		}
		return VisibilityInfo{}, apperr.Forbidden("只有上传者或主机账号可以修改可见范围")
	}
	vis, err = normalizeVisibility(tx, actor, n.Space, vis)
	if err != nil {
		return VisibilityInfo{}, err
	}
	if _, err := tx.Exec(`UPDATE nodes SET visibility_mode = ?, updated_at = ?, updated_by = ? WHERE id = ?`,
		vis.Mode, model.Now(), actor.DisplayName, nodeID); err != nil {
		return VisibilityInfo{}, err
	}
	if err := saveVisibilityTx(tx, nodeID, actor, vis); err != nil {
		return VisibilityInfo{}, err
	}
	detail := "设为默认等级规则"
	if vis.Mode == "custom" {
		detail = fmt.Sprintf("指定可见：%d 个权限组，%d 个用户", len(vis.Roles), len(vis.Users))
	}
	if err := s.Audit.LogTx(tx, eventFor(actor, n.Space, "visibility_update", "file", nodeID, n.Name, n.Path, detail)); err != nil {
		return VisibilityInfo{}, err
	}
	if err := tx.Commit(); err != nil {
		return VisibilityInfo{}, err
	}
	return s.GetVisibility(nodeID, actor)
}

func (s *Service) VisibilityOptions(actor Actor) (VisibilityOptions, error) {
	roles := []string{model.RoleAdmin, model.RoleOperator, model.RoleEditor, model.RoleViewer}
	rows, err := s.DB.Query(`SELECT id, display_name, role FROM users
		WHERE disabled = 0 AND deleted_at = '' AND is_owner = 0 AND id <> ?
		ORDER BY CASE role WHEN 'admin' THEN 0 WHEN 'operator' THEN 1 WHEN 'editor' THEN 2 ELSE 3 END,
			display_name COLLATE NOCASE`, actor.ID)
	if err != nil {
		return VisibilityOptions{}, err
	}
	defer rows.Close()
	users := []VisibilityUser{}
	for rows.Next() {
		var user VisibilityUser
		if err := rows.Scan(&user.ID, &user.DisplayName, &user.Role); err != nil {
			return VisibilityOptions{}, err
		}
		users = append(users, user)
	}
	return VisibilityOptions{Roles: roles, Users: users}, rows.Err()
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func stringArgs(values []string) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}
	return out
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func eventFor(actor Actor, space, action, targetType, id, name, path, detail string) audit.Event {
	nameDisplay := actor.DisplayName
	if nameDisplay == "" {
		nameDisplay = "系统"
	}
	return audit.Event{
		ActorID: actor.ID, ActorName: nameDisplay, Action: action,
		TargetType: targetType, TargetID: id, TargetName: name, TargetPath: path,
		Detail: detail, Result: audit.ResultSuccess, IP: actor.IP,
		Secure: space == model.SpaceSecure,
	}
}

func limitNote(note string) string {
	note = strings.TrimSpace(note)
	r := []rune(note)
	if len(r) > 500 {
		return string(r[:500])
	}
	return note
}

func isUniqueError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "%", `\%`)
	value = strings.ReplaceAll(value, "_", `\_`)
	return value
}
