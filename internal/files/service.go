package files

import (
	"database/sql"
	"fmt"
	"os"
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
	IP          string
}

func (a Actor) IsAdmin() bool { return a.Role == model.RoleAdmin }
func (a Actor) CanEdit() bool { return a.Role == model.RoleAdmin || a.Role == model.RoleEditor }

type Service struct {
	DB       *sql.DB
	Store    *storage.Store
	Audit    *audit.Service
	Settings *settings.Service
	mu       sync.Mutex
}

type Node struct {
	ID               string `json:"id"`
	ParentID         string `json:"parent_id"`
	Name             string `json:"name"`
	Kind             string `json:"kind"`
	CurrentVersionID string `json:"current_version_id,omitempty"`
	Revision         int    `json:"revision"`
	Size             int64  `json:"size"`
	SHA256           string `json:"sha256,omitempty"`
	VersionNo        int    `json:"version_no"`
	CreatedAt        string `json:"created_at"`
	CreatedBy        string `json:"created_by"`
	UpdatedAt        string `json:"updated_at"`
	UpdatedBy        string `json:"updated_by"`
	DeletedAt        string `json:"deleted_at,omitempty"`
	DeletedBy        string `json:"deleted_by,omitempty"`
	DeleteBatch      string `json:"delete_batch,omitempty"`
	Path             string `json:"path,omitempty"`
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
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ArchiveEntry struct {
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
	COALESCE(n.deleted_at, ''), COALESCE(n.deleted_by, ''), COALESCE(n.delete_batch, '')`

type scanner interface {
	Scan(dest ...any) error
}

func scanNode(row scanner) (Node, error) {
	var n Node
	err := row.Scan(&n.ID, &n.ParentID, &n.Name, &n.Kind, &n.CurrentVersionID, &n.Revision,
		&n.Size, &n.SHA256, &n.VersionNo, &n.CreatedAt, &n.CreatedBy, &n.UpdatedAt, &n.UpdatedBy,
		&n.DeletedAt, &n.DeletedBy, &n.DeleteBatch)
	return n, err
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

func (s *Service) List(parentID, query string) ([]Node, error) {
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
			LIMIT 200`, like)
	} else {
		if parentID != "" {
			if _, err := s.GetActive(parentID); err != nil {
				return nil, err
			}
		}
		rows, err = s.DB.Query(`SELECT `+nodeColumns+` FROM nodes n
			LEFT JOIN versions v ON v.id = n.current_version_id
			WHERE n.deleted_at IS NULL AND n.parent_id = ?
			ORDER BY CASE n.kind WHEN 'folder' THEN 0 ELSE 1 END, n.name COLLATE NOCASE`, parentID)
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
	rows, err := s.DB.Query(`SELECT id, parent_id, name FROM nodes
		WHERE kind = 'folder' AND deleted_at IS NULL ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	paths := map[string]string{}
	var out []Node
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.ID, &n.ParentID, &n.Name); err != nil {
			rows.Close()
			return nil, err
		}
		n.Kind = model.KindFolder
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

func (s *Service) ArchiveEntries(rootID string) ([]ArchiveEntry, error) {
	rows, err := s.DB.Query(`WITH RECURSIVE tree(id, name, parent_id, kind, current_version_id, rel) AS (
		SELECT id, name, parent_id, kind, current_version_id, name
		FROM nodes WHERE parent_id = ? AND deleted_at IS NULL
		UNION ALL
		SELECT n.id, n.name, n.parent_id, n.kind, n.current_version_id, tree.rel || '/' || n.name
		FROM nodes n JOIN tree ON n.parent_id = tree.id
		WHERE n.deleted_at IS NULL
	)
	SELECT tree.kind, COALESCE(v.object_id, ''), COALESCE(v.size, 0), tree.rel
	FROM tree LEFT JOIN versions v ON v.id = tree.current_version_id
	ORDER BY tree.rel COLLATE NOCASE`, rootID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ArchiveEntry{}
	for rows.Next() {
		var e ArchiveEntry
		if err := rows.Scan(&e.Kind, &e.ObjectID, &e.Size, &e.Path); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Service) Breadcrumbs(id string) ([]Breadcrumb, error) {
	out := []Breadcrumb{{ID: "", Name: "全部项目"}}
	if id == "" {
		return out, nil
	}
	var parts []Breadcrumb
	cur := id
	visited := map[string]bool{}
	for cur != "" && !visited[cur] {
		visited[cur] = true
		var name, parent, kind string
		var deleted sql.NullString
		err := s.DB.QueryRow(`SELECT name, parent_id, kind, deleted_at FROM nodes WHERE id = ?`, cur).
			Scan(&name, &parent, &kind, &deleted)
		if err != nil {
			return nil, err
		}
		if deleted.Valid {
			return nil, apperr.NotFound("目录不存在")
		}
		parts = append(parts, Breadcrumb{ID: cur, Name: name})
		cur = parent
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
	if parentID != "" {
		if _, err := s.GetActive(parentID); err != nil {
			return Node{}, false, err
		}
	}
	var id string
	err := s.DB.QueryRow(`SELECT id FROM nodes WHERE parent_id = ? AND name = ? AND deleted_at IS NULL`,
		parentID, name).Scan(&id)
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
	if !actor.CanEdit() {
		return Node{}, apperr.Forbidden("当前账号没有编辑权限")
	}
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
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(1) FROM nodes WHERE parent_id = ? AND name = ? AND deleted_at IS NULL`,
		parentID, name).Scan(&exists); err != nil {
		return Node{}, err
	}
	if exists > 0 {
		return Node{}, apperr.Conflict("NAME_EXISTS", "同一目录下已存在同名项目")
	}
	id := model.NewID()
	now := model.Now()
	if _, err := tx.Exec(`INSERT INTO nodes(id, parent_id, name, kind, current_version_id, revision,
		created_at, created_by, updated_at, updated_by) VALUES(?, ?, ?, 'folder', '', 0, ?, ?, ?, ?)`,
		id, parentID, name, now, actor.DisplayName, now, actor.DisplayName); err != nil {
		return Node{}, err
	}
	path := joinPath(parentPath, name)
	if err := s.Audit.LogTx(tx, event(actor, "folder_create", "folder", id, name, path, "新建目录")); err != nil {
		return Node{}, err
	}
	if err := tx.Commit(); err != nil {
		return Node{}, err
	}
	return Node{ID: id, ParentID: parentID, Name: name, Kind: model.KindFolder,
		CreatedAt: now, CreatedBy: actor.DisplayName, UpdatedAt: now, UpdatedBy: actor.DisplayName, Path: path}, nil
}

func (s *Service) UploadNew(parentID, name, note string, obj storage.Saved, actor Actor) (UploadResult, error) {
	if !actor.CanEdit() {
		return UploadResult{}, apperr.Forbidden("当前账号没有编辑权限")
	}
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
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(1) FROM nodes WHERE parent_id = ? AND name = ? AND deleted_at IS NULL`,
		parentID, name).Scan(&exists); err != nil {
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
		created_at, created_by, updated_at, updated_by) VALUES(?, ?, ?, 'file', ?, 1, ?, ?, ?, ?)`,
		nodeID, parentID, name, versionID, now, actor.DisplayName, now, actor.DisplayName); err != nil {
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
	if err := s.Audit.LogTx(tx, event(actor, "upload_new", "file", nodeID, name, path,
		fmt.Sprintf("初始版本，%d 字节，SHA256 %s", obj.Size, obj.SHA256))); err != nil {
		return UploadResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return UploadResult{}, err
	}
	return UploadResult{
		Node: Node{ID: nodeID, ParentID: parentID, Name: name, Kind: model.KindFile,
			CurrentVersionID: versionID, Revision: 1, Size: obj.Size, SHA256: obj.SHA256,
			VersionNo: 1, CreatedAt: now, CreatedBy: actor.DisplayName, UpdatedAt: now,
			UpdatedBy: actor.DisplayName, Path: path},
		Version: Version{ID: versionID, NodeID: nodeID, VersionNo: 1, ObjectID: obj.ID,
			Size: obj.Size, SHA256: obj.SHA256, Note: note, CreatedAt: now, CreatedBy: actor.ID,
			CreatedByName: actor.DisplayName, IsCurrent: true},
	}, nil
}

func (s *Service) UploadVersion(nodeID, baseVersionID, note string, obj storage.Saved, actor Actor) (UploadResult, error) {
	if !actor.CanEdit() {
		return UploadResult{}, apperr.Forbidden("当前账号没有编辑权限")
	}
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
	if err := s.Audit.LogTx(tx, event(actor, "upload_version", "file", nodeID, n.Name, n.Path, detail)); err != nil {
		return UploadResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return UploadResult{}, err
	}
	return UploadResult{
		Node: Node{ID: n.ID, ParentID: n.ParentID, Name: n.Name, Kind: n.Kind,
			CurrentVersionID: versionID, Revision: nextNo, Size: obj.Size, SHA256: obj.SHA256,
			VersionNo: nextNo, CreatedAt: n.CreatedAt, CreatedBy: n.CreatedBy,
			UpdatedAt: now, UpdatedBy: actor.DisplayName, Path: n.Path},
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
	if !actor.CanEdit() {
		return Node{}, apperr.Forbidden("当前账号没有编辑权限")
	}
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
	if n.Name == newName {
		return n, nil
	}
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(1) FROM nodes WHERE parent_id = ? AND name = ? AND deleted_at IS NULL AND id <> ?`,
		n.ParentID, newName, nodeID).Scan(&exists); err != nil {
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
	if err := s.Audit.LogTx(tx, event(actor, "rename", n.Kind, nodeID, newName, newPath,
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
	if !actor.CanEdit() {
		return Node{}, apperr.Forbidden("当前账号没有编辑权限")
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
	newParentPath, err := s.ensureParentTx(tx, newParentID)
	if err != nil {
		return Node{}, err
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
	if err := tx.QueryRow(`SELECT COUNT(1) FROM nodes WHERE parent_id = ? AND name = ? AND deleted_at IS NULL AND id <> ?`,
		newParentID, n.Name, nodeID).Scan(&exists); err != nil {
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
	if err := s.Audit.LogTx(tx, event(actor, "move", n.Kind, nodeID, n.Name, newPath,
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
	return n, nil
}

func (s *Service) SoftDelete(nodeID string, actor Actor) (int, error) {
	if !actor.CanEdit() {
		return 0, apperr.Forbidden("当前账号没有编辑权限")
	}
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
	if err := s.Audit.LogTx(tx, event(actor, "delete", n.Kind, nodeID, n.Name, n.Path,
		fmt.Sprintf("移入回收站，共 %d 项", count))); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(count), nil
}

func (s *Service) ListRecycle() ([]Node, error) {
	rows, err := s.DB.Query(`SELECT ` + nodeColumns + ` FROM nodes n
		LEFT JOIN versions v ON v.id = n.current_version_id
		WHERE n.deleted_at IS NOT NULL
		AND NOT EXISTS (
			SELECT 1 FROM nodes p WHERE p.id = n.parent_id
			AND p.delete_batch = n.delete_batch AND p.deleted_at IS NOT NULL
		)
		ORDER BY n.deleted_at DESC`)
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
	if !actor.IsAdmin() {
		return 0, apperr.Forbidden("只有管理员可以恢复回收站")
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
	rows, err := tx.Query(`SELECT id, parent_id, name FROM nodes WHERE delete_batch = ? AND deleted_at IS NOT NULL`, batch)
	if err != nil {
		return 0, err
	}
	type item struct{ id, parent, name string }
	var items []item
	inBatch := map[string]bool{}
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.parent, &it.name); err != nil {
			rows.Close()
			return 0, err
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
		if err := s.Audit.LogTx(tx, event(actor, "recycle_restore", "file", root.id, root.name, path,
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
	if !actor.IsAdmin() {
		return 0, apperr.Forbidden("只有管理员可以永久清理")
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
	var rootID, rootName string
	err = tx.QueryRow(`SELECT id, name FROM nodes WHERE delete_batch = ? AND deleted_at IS NOT NULL LIMIT 1`, batch).
		Scan(&rootID, &rootName)
	if err == sql.ErrNoRows {
		return 0, apperr.NotFound("回收站项目不存在")
	}
	if err != nil {
		return 0, err
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
	if err := s.Audit.LogTx(tx, event(actor, "recycle_purge", "file", rootID, rootName, path,
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
	if !actor.IsAdmin() {
		return 0, apperr.Forbidden("只有管理员可以永久清理")
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
	var count int
	if err := tx.QueryRow(`SELECT COUNT(1) FROM nodes WHERE deleted_at IS NOT NULL`).Scan(&count); err != nil {
		return 0, err
	}
	if count == 0 {
		return 0, nil
	}
	rows, err := tx.Query(`SELECT DISTINCT object_id FROM versions WHERE node_id IN
		(SELECT id FROM nodes WHERE deleted_at IS NOT NULL)`)
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
	if _, err := tx.Exec(`DELETE FROM versions WHERE node_id IN (SELECT id FROM nodes WHERE deleted_at IS NOT NULL)`); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM nodes WHERE deleted_at IS NOT NULL`); err != nil {
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
	if err := s.Audit.LogTx(tx, event(actor, "recycle_purge_all", "recycle", "", "回收站", "/",
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

func (s *Service) ListVersions(nodeID string) ([]Version, error) {
	var currentID string
	if err := s.DB.QueryRow(`SELECT current_version_id FROM nodes WHERE id = ? AND deleted_at IS NULL`, nodeID).Scan(&currentID); err != nil {
		if err == sql.ErrNoRows {
			return nil, apperr.NotFound("文件不存在")
		}
		return nil, err
	}
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
	if !actor.CanEdit() {
		return UploadResult{}, apperr.Forbidden("当前账号没有编辑权限")
	}
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
	if err := s.Audit.LogTx(tx, event(actor, "version_restore", "file", nodeID, n.Name, n.Path,
		fmt.Sprintf("将 V%d 恢复为最新版本 V%d", src.VersionNo, nextNo))); err != nil {
		return UploadResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return UploadResult{}, err
	}
	return UploadResult{
		Node: Node{ID: n.ID, ParentID: n.ParentID, Name: n.Name, Kind: n.Kind,
			CurrentVersionID: newID, Revision: nextNo, Size: src.Size, SHA256: src.SHA256,
			VersionNo: nextNo, CreatedAt: n.CreatedAt, CreatedBy: n.CreatedBy,
			UpdatedAt: now, UpdatedBy: actor.DisplayName, Path: n.Path},
		Version: Version{ID: newID, NodeID: nodeID, VersionNo: nextNo, ObjectID: src.ObjectID,
			Size: src.Size, SHA256: src.SHA256, Note: note, CreatedAt: now, CreatedBy: actor.ID,
			CreatedByName: actor.DisplayName, IsCurrent: true},
	}, nil
}

func (s *Service) PurgeVersion(nodeID, versionID string, actor Actor) error {
	if !actor.IsAdmin() {
		return apperr.Forbidden("只有管理员可以清理历史版本")
	}
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
	if err := s.Audit.LogTx(tx, event(actor, "version_purge", "file", nodeID, n.Name, n.Path,
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

func (s *Service) ResolveVersion(nodeID, versionID string) (Node, Version, error) {
	n, err := s.GetActive(nodeID)
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

func (s *Service) NodeAudit(nodeID string) ([]audit.Row, error) {
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

func event(actor Actor, action, targetType, id, name, path, detail string) audit.Event {
	nameDisplay := actor.DisplayName
	if nameDisplay == "" {
		nameDisplay = "系统"
	}
	return audit.Event{
		ActorID: actor.ID, ActorName: nameDisplay, Action: action,
		TargetType: targetType, TargetID: id, TargetName: name, TargetPath: path,
		Detail: detail, Result: audit.ResultSuccess, IP: actor.IP,
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
