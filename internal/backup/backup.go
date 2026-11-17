package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"

	"lanfile/internal/audit"
	"lanfile/internal/model"
	"lanfile/internal/settings"
	"lanfile/internal/storage"
)

const manifestApp = "lan-file-platform-backup"

type ObjectRef struct {
	ID     string `json:"id"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Manifest struct {
	App         string      `json:"app"`
	CreatedAt   string      `json:"created_at"`
	DBFile      string      `json:"db_file"`
	DBSHA256    string      `json:"db_sha256"`
	Objects     []ObjectRef `json:"objects"`
	MirrorFiles int         `json:"mirror_files"`
	MirrorBytes int64       `json:"mirror_bytes"`
}

type Run struct {
	ID         string `json:"id"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	Status     string `json:"status"`
	Message    string `json:"message"`
	Path       string `json:"path"`
	Bytes      int64  `json:"bytes"`
	Objects    int    `json:"objects"`
}

type Service struct {
	DB       *sql.DB
	DBPath   string
	DataDir  string
	Store    *storage.Store
	Settings *settings.Service
	Audit    *audit.Service

	mu            sync.Mutex
	running       atomic.Bool
	scheduleMu    sync.Mutex
	lastAttemptAt string
}

func New(db *sql.DB, dbPath, dataDir string, store *storage.Store, settingsSvc *settings.Service, auditSvc *audit.Service) *Service {
	return &Service{DB: db, DBPath: dbPath, DataDir: dataDir, Store: store, Settings: settingsSvc, Audit: auditSvc}
}

func (s *Service) RecoverInterrupted() {
	_, _ = s.DB.Exec(`UPDATE backup_runs SET status = 'failed', message = '备份进程中断', finished_at = ?
		WHERE status = 'running'`, model.Now())
}

func (s *Service) Running() bool { return s.running.Load() }

func isInside(child, parent string) bool {
	childAbs, err := filepath.Abs(child)
	if err != nil {
		return false
	}
	parentAbs, err := filepath.Abs(parent)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(parentAbs, childAbs)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

func (s *Service) RunNow(trigger string) (Run, error) {
	if !s.running.CompareAndSwap(false, true) {
		return Run{}, errors.New("已有备份任务正在执行")
	}
	defer s.running.Store(false)

	started := model.Now()
	run := Run{ID: model.NewID(), StartedAt: started, Status: "running"}
	if _, err := s.DB.Exec(`INSERT INTO backup_runs(id, started_at, status) VALUES(?, ?, 'running')`,
		run.ID, started); err != nil {
		return run, err
	}
	fail := func(err error) (Run, error) {
		run.Status = "failed"
		run.Message = err.Error()
		run.FinishedAt = model.Now()
		_, _ = s.DB.Exec(`UPDATE backup_runs SET finished_at = ?, status = 'failed', message = ? WHERE id = ?`,
			run.FinishedAt, run.Message, run.ID)
		_ = s.Audit.Log(audit.Event{
			ActorName: "系统", Action: "backup_failed", TargetType: "system",
			TargetName: "自动备份", Detail: err.Error(), Result: audit.ResultFailure,
		})
		log.Printf("备份失败: %v", err)
		return run, err
	}

	backupRoot := strings.TrimSpace(s.Settings.Get(settings.KeyBackupDir, ""))
	if backupRoot == "" {
		return fail(errors.New("尚未配置备份目录"))
	}
	if isInside(backupRoot, s.DataDir) {
		return fail(errors.New("备份目录不能位于数据目录内，否则无法抵御硬盘故障"))
	}
	info, err := os.Stat(backupRoot)
	if err != nil || !info.IsDir() {
		return fail(fmt.Errorf("备份目录不可访问：%s", backupRoot))
	}
	keep := int(s.Settings.GetInt64(settings.KeyBackupKeep, 7))
	if keep < 1 {
		keep = 1
	}

	runDir := filepath.Join(backupRoot, "run-"+time.Now().Format("20060102-150405"))
	for i := 1; ; i++ {
		if _, err := os.Stat(runDir); errors.Is(err, os.ErrNotExist) {
			break
		}
		runDir = filepath.Join(backupRoot, fmt.Sprintf("run-%s-%d", time.Now().Format("20060102-150405"), i))
	}
	if err := os.MkdirAll(filepath.Join(runDir, "objects"), 0o755); err != nil {
		return fail(err)
	}
	run.Path = runDir

	s.Store.Guard.RLock()
	defer s.Store.Guard.RUnlock()

	snapshotTmp := filepath.Join(runDir, "meta.db.tmp")
	escaped := strings.ReplaceAll(filepath.ToSlash(snapshotTmp), "'", "''")
	if _, err := s.DB.Exec(`VACUUM INTO '` + escaped + `'`); err != nil {
		os.RemoveAll(runDir)
		return fail(fmt.Errorf("创建数据库快照失败：%w", err))
	}
	snapshotDB := filepath.Join(runDir, "meta.db")
	if err := os.Rename(snapshotTmp, snapshotDB); err != nil {
		os.RemoveAll(runDir)
		return fail(err)
	}
	dbSHA, err := fileSHA256(snapshotDB)
	if err != nil {
		os.RemoveAll(runDir)
		return fail(err)
	}

	snapshot, err := sql.Open("sqlite", "file:"+filepath.ToSlash(snapshotDB)+"?mode=ro")
	if err != nil {
		os.RemoveAll(runDir)
		return fail(err)
	}
	rows, err := snapshot.Query(`SELECT DISTINCT o.id, o.size, o.sha256 FROM versions v JOIN objects o ON o.id = v.object_id`)
	if err != nil {
		snapshot.Close()
		os.RemoveAll(runDir)
		return fail(err)
	}
	var objects []ObjectRef
	for rows.Next() {
		var ref ObjectRef
		if err := rows.Scan(&ref.ID, &ref.Size, &ref.SHA256); err != nil {
			rows.Close()
			snapshot.Close()
			os.RemoveAll(runDir)
			return fail(err)
		}
		objects = append(objects, ref)
	}
	rows.Close()
	snapshot.Close()
	sort.Slice(objects, func(i, j int) bool { return objects[i].ID < objects[j].ID })

	var totalBytes int64
	for _, ref := range objects {
		dst := filepath.Join(runDir, "objects", ref.ID[:2], ref.ID)
		n, err := copyVerify(s.Store.ObjectPath(ref.ID), dst, ref.Size, ref.SHA256)
		if err != nil {
			os.RemoveAll(runDir)
			return fail(fmt.Errorf("复制对象 %s 失败：%w", ref.ID, err))
		}
		totalBytes += n
	}

	certDir := filepath.Join(s.DataDir, "certs")
	for _, name := range []string{"ca.crt", "ca.key", "server.crt", "server.key"} {
		src := filepath.Join(certDir, name)
		if _, err := os.Stat(src); err == nil {
			dst := filepath.Join(runDir, "certs", name)
			if err := copyFile(src, dst); err != nil {
				os.RemoveAll(runDir)
				return fail(fmt.Errorf("复制 HTTPS 证书失败：%w", err))
			}
		}
	}

	mirrorFiles, mirrorBytes, err := buildMirror(snapshotDB, runDir)
	if err != nil {
		os.RemoveAll(runDir)
		return fail(fmt.Errorf("生成可读副本失败：%w", err))
	}

	manifest := Manifest{
		App: manifestApp, CreatedAt: model.Now(), DBFile: "meta.db",
		DBSHA256: dbSHA, Objects: objects,
		MirrorFiles: mirrorFiles, MirrorBytes: mirrorBytes,
	}
	if err := writeJSON(filepath.Join(runDir, "manifest.json"), manifest); err != nil {
		os.RemoveAll(runDir)
		return fail(err)
	}
	complete := map[string]any{
		"app": manifestApp, "finished_at": model.Now(),
		"objects": len(objects), "bytes": totalBytes + mirrorBytes,
		"object_bytes": totalBytes, "mirror_files": mirrorFiles, "mirror_bytes": mirrorBytes,
	}
	if err := writeJSON(filepath.Join(runDir, "complete.json"), complete); err != nil {
		os.RemoveAll(runDir)
		return fail(err)
	}

	run.Status = "success"
	run.FinishedAt = model.Now()
	run.Bytes = totalBytes + mirrorBytes
	run.Objects = len(objects)
	run.Message = fmt.Sprintf("备份 %d 个对象，共 %d 字节；可读副本 %d 个文件", len(objects), totalBytes, mirrorFiles)
	if _, err := s.DB.Exec(`UPDATE backup_runs SET finished_at = ?, status = 'success', message = ?, path = ?,
		bytes = ?, objects = ? WHERE id = ?`, run.FinishedAt, run.Message, run.Path, run.Bytes, run.Objects, run.ID); err != nil {
		return fail(err)
	}
	_ = s.Audit.Log(audit.Event{
		ActorName: "系统", Action: "backup_success", TargetType: "system", TargetName: "自动备份",
		TargetPath: runDir, Detail: run.Message, Result: audit.ResultSuccess,
	})
	s.prune(backupRoot, keep)
	log.Printf("备份完成: %s (%s)", runDir, run.Message)
	return run, nil
}

func (s *Service) prune(root string, keep int) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	type item struct {
		path string
		mod  time.Time
	}
	var runs []item
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "run-") {
			continue
		}
		dir := filepath.Join(root, e.Name())
		body, err := os.ReadFile(filepath.Join(dir, "complete.json"))
		if err != nil {
			continue
		}
		var marker map[string]any
		if json.Unmarshal(body, &marker) != nil || marker["app"] != manifestApp {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		runs = append(runs, item{path: dir, mod: info.ModTime()})
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].mod.After(runs[j].mod) })
	for i := keep; i < len(runs); i++ {
		_ = os.RemoveAll(runs[i].path)
	}
}

func (s *Service) ListRuns(limit int) ([]Run, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.DB.Query(`SELECT id, started_at, finished_at, status, message, path, bytes, objects
		FROM backup_runs ORDER BY started_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.StartedAt, &r.FinishedAt, &r.Status, &r.Message, &r.Path, &r.Bytes, &r.Objects); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Service) LastSuccess() (*Run, error) {
	var r Run
	err := s.DB.QueryRow(`SELECT id, started_at, finished_at, status, message, path, bytes, objects
		FROM backup_runs WHERE status = 'success' ORDER BY finished_at DESC LIMIT 1`).
		Scan(&r.ID, &r.StartedAt, &r.FinishedAt, &r.Status, &r.Message, &r.Path, &r.Bytes, &r.Objects)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Service) StartScheduler(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.tick()
			}
		}
	}()
}

func (s *Service) tick() {
	if !s.Settings.GetBool(settings.KeyBackupEnabled, true) || s.running.Load() {
		return
	}
	want := s.Settings.Get(settings.KeyBackupTime, "02:30")
	now := time.Now()
	if now.Format("15:04") != want {
		return
	}
	today := now.Format("2006-01-02")
	s.scheduleMu.Lock()
	if s.lastAttemptAt == today {
		s.scheduleMu.Unlock()
		return
	}
	s.lastAttemptAt = today
	s.scheduleMu.Unlock()
	go func() {
		if _, err := s.RunNow("scheduled"); err != nil {
			log.Printf("计划备份失败: %v", err)
		}
	}()
}

func Restore(backupDir, targetDir string) error {
	body, err := os.ReadFile(filepath.Join(backupDir, "manifest.json"))
	if err != nil {
		return fmt.Errorf("读取备份清单失败：%w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return fmt.Errorf("备份清单无效：%w", err)
	}
	if manifest.App != manifestApp {
		return errors.New("该目录不是本系统生成的完整备份")
	}
	if _, err := os.Stat(filepath.Join(backupDir, "complete.json")); err != nil {
		return errors.New("备份未完成，不能用于恢复")
	}
	entries, err := os.ReadDir(targetDir)
	if err == nil && len(entries) > 0 {
		return errors.New("恢复目标目录必须为空")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Join(targetDir, "objects"), 0o755); err != nil {
		return err
	}
	gotSHA, err := fileSHA256(filepath.Join(backupDir, manifest.DBFile))
	if err != nil {
		return err
	}
	if !strings.EqualFold(gotSHA, manifest.DBSHA256) {
		return errors.New("备份数据库摘要校验失败")
	}
	if err := copyFile(filepath.Join(backupDir, manifest.DBFile), filepath.Join(targetDir, "meta.db")); err != nil {
		return err
	}
	for _, ref := range manifest.Objects {
		if len(ref.ID) < 2 {
			return fmt.Errorf("备份对象 ID 无效：%s", ref.ID)
		}
		src := filepath.Join(backupDir, "objects", ref.ID[:2], ref.ID)
		dst := filepath.Join(targetDir, "objects", ref.ID[:2], ref.ID)
		if _, err := copyVerify(src, dst, ref.Size, ref.SHA256); err != nil {
			return fmt.Errorf("恢复对象 %s 失败：%w", ref.ID, err)
		}
	}
	for _, name := range []string{"ca.crt", "ca.key", "server.crt", "server.key"} {
		src := filepath.Join(backupDir, "certs", name)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dst := filepath.Join(targetDir, "certs", name)
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("恢复 HTTPS 证书失败：%w", err)
		}
	}
	return nil
}

func copyVerify(src, dst string, size int64, wantSHA string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return 0, err
	}
	if info.Size() != size {
		return 0, errors.New("文件大小不一致")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".backup-*")
	if err != nil {
		return 0, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), in)
	if err != nil {
		tmp.Close()
		return 0, err
	}
	if n != size {
		tmp.Close()
		return 0, errors.New("复制过程中文件大小变化")
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), wantSHA) {
		tmp.Close()
		return 0, errors.New("文件摘要校验失败")
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		return 0, err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return 0, err
	}
	return n, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeJSON(path string, v any) error {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

type mirrorItem struct {
	ID       string
	ParentID string
	Name     string
	Kind     string
	ObjectID string
	SHA256   string
	Size     int64
}

func buildMirror(snapshotDB, runDir string) (int, int64, error) {
	snapshot, err := sql.Open("sqlite", "file:"+filepath.ToSlash(snapshotDB)+"?mode=ro")
	if err != nil {
		return 0, 0, err
	}
	defer snapshot.Close()
	rows, err := snapshot.Query(`SELECT n.id, n.parent_id, n.name, n.kind,
		COALESCE(v.object_id, ''), COALESCE(v.size, 0), COALESCE(v.sha256, '')
		FROM nodes n LEFT JOIN versions v ON v.id = n.current_version_id
		WHERE n.deleted_at IS NULL ORDER BY n.name COLLATE NOCASE`)
	if err != nil {
		return 0, 0, err
	}
	var items []mirrorItem
	children := map[string][]int{}
	for rows.Next() {
		var it mirrorItem
		if err := rows.Scan(&it.ID, &it.ParentID, &it.Name, &it.Kind, &it.ObjectID, &it.Size, &it.SHA256); err != nil {
			rows.Close()
			return 0, 0, err
		}
		children[it.ParentID] = append(children[it.ParentID], len(items))
		items = append(items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}

	mirrorRoot := filepath.Join(runDir, "files")
	if err := os.MkdirAll(mirrorRoot, 0o755); err != nil {
		return 0, 0, err
	}
	used := map[string]map[string]bool{}
	var files int
	var bytes int64
	var walk func(parentID, rel string) error
	walk = func(parentID, rel string) error {
		for _, idx := range children[parentID] {
			it := items[idx]
			name := allocateMirrorName(used, rel, it.Name)
			childRel := name
			if rel != "" {
				childRel = rel + "/" + name
			}
			if it.Kind == "folder" {
				if err := os.MkdirAll(filepath.Join(mirrorRoot, filepath.FromSlash(childRel)), 0o755); err != nil {
					return err
				}
				if err := walk(it.ID, childRel); err != nil {
					return err
				}
				continue
			}
			if it.ObjectID == "" || len(it.ObjectID) < 2 {
				continue
			}
			src := filepath.Join(runDir, "objects", it.ObjectID[:2], it.ObjectID)
			dst := filepath.Join(mirrorRoot, filepath.FromSlash(childRel))
			n, err := copyVerify(src, dst, it.Size, it.SHA256)
			if err != nil {
				return fmt.Errorf("复制 %s 失败：%w", childRel, err)
			}
			files++
			bytes += n
		}
		return nil
	}
	if err := walk("", ""); err != nil {
		return 0, 0, err
	}

	readme := `本目录是备份的可读副本，按原文件夹结构和文件名保存“当前版本”，可直接用资源管理器打开。
历史版本仍保存在 objects/ 目录中；完整恢复请使用 LanFileServer.exe -restore。
如个别名称与 Windows 保留名冲突或无法直接使用，副本中的名称会做少量调整。
`
	if err := os.WriteFile(filepath.Join(runDir, "README-可读副本.txt"), []byte(readme), 0o644); err != nil {
		return 0, 0, err
	}
	return files, bytes, nil
}

func allocateMirrorName(used map[string]map[string]bool, dir, raw string) string {
	name := sanitizeMirrorName(raw)
	if used[dir] == nil {
		used[dir] = map[string]bool{}
	}
	key := strings.ToLower(name)
	if !used[dir][key] {
		used[dir][key] = true
		return name
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if !used[dir][strings.ToLower(candidate)] {
			used[dir][strings.ToLower(candidate)] = true
			return candidate
		}
	}
}

func sanitizeMirrorName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			b.WriteRune('_')
		} else {
			b.WriteRune(r)
		}
	}
	out := strings.TrimRight(b.String(), " .")
	if out == "" {
		out = "_"
	}
	if isReservedWindowsName(out) {
		out = "_" + out
	}
	return out
}

func isReservedWindowsName(name string) bool {
	base := name
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	base = strings.ToUpper(strings.TrimRight(base, " ."))
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
		return base[3] >= '0' && base[3] <= '9'
	}
	return false
}
