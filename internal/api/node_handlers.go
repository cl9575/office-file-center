package api

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"lanfile/internal/apperr"
	"lanfile/internal/audit"
	"lanfile/internal/files"
	"lanfile/internal/model"
	"lanfile/internal/settings"
	"lanfile/internal/storage"
)

var uploadIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

type uploadTask struct {
	ID        string
	UserID    string
	Status    string
	NodeID    string
	VersionID string
	Error     string
}

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request, user model.User) {
	parent := r.URL.Query().Get("parent")
	q := r.URL.Query().Get("q")
	items, err := s.Files.List(parent, q)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleNodeDetail(w http.ResponseWriter, r *http.Request, user model.User) {
	node, err := s.Files.GetActive(r.PathValue("id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, node)
}

func (s *Server) handleBreadcrumbs(w http.ResponseWriter, r *http.Request, user model.User) {
	crumbs, err := s.Files.Breadcrumbs(r.PathValue("id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"items": crumbs})
}

func (s *Server) handleCreateNode(w http.ResponseWriter, r *http.Request, user model.User) {
	var in struct {
		ParentID string `json:"parent_id"`
		Name     string `json:"name"`
	}
	if !s.decodeJSON(w, r, &in) {
		return
	}
	node, err := s.Files.CreateFolder(in.ParentID, in.Name, actorOf(user, r))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, node)
}

func (s *Server) handlePatchNode(w http.ResponseWriter, r *http.Request, user model.User) {
	var in struct {
		Name     *string `json:"name"`
		ParentID *string `json:"parent_id"`
	}
	if !s.decodeJSON(w, r, &in) {
		return
	}
	if in.Name == nil && in.ParentID == nil {
		s.writeErr(w, r, apperr.BadRequest("NO_CHANGE", "没有提供要修改的内容"))
		return
	}
	id := r.PathValue("id")
	node, err := s.Files.GetActive(id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if in.Name != nil {
		node, err = s.Files.Rename(id, *in.Name, actorOf(user, r))
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
	}
	if in.ParentID != nil {
		node, err = s.Files.Move(id, *in.ParentID, actorOf(user, r))
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
	}
	s.writeJSON(w, http.StatusOK, node)
}

func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request, user model.User) {
	count, err := s.Files.SoftDelete(r.PathValue("id"), actorOf(user, r))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"deleted": count})
}

func (s *Server) handleVersions(w http.ResponseWriter, r *http.Request, user model.User) {
	versions, err := s.Files.ListVersions(r.PathValue("id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"items": versions})
}

func (s *Server) handleNodeAudit(w http.ResponseWriter, r *http.Request, user model.User) {
	rows, err := s.Files.NodeAudit(r.PathValue("id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (s *Server) handleFolderTree(w http.ResponseWriter, r *http.Request, user model.User) {
	folders, err := s.Files.AllFolders()
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"items": folders})
}

func (s *Server) handleResolve(w http.ResponseWriter, r *http.Request, user model.User) {
	parent := r.URL.Query().Get("parent")
	name := r.URL.Query().Get("name")
	node, exists, err := s.Files.CheckNewName(parent, name)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"exists": exists, "node": node})
}

func (s *Server) handleRestoreVersion(w http.ResponseWriter, r *http.Request, user model.User) {
	var in struct {
		VersionID     string `json:"version_id"`
		BaseVersionID string `json:"base_version_id"`
	}
	if !s.decodeJSON(w, r, &in) {
		return
	}
	result, err := s.Files.RestoreVersion(r.PathValue("id"), in.VersionID, in.BaseVersionID, actorOf(user, r))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, result)
}

func (s *Server) handlePurgeVersion(w http.ResponseWriter, r *http.Request, user model.User) {
	if err := s.Files.PurgeVersion(r.PathValue("id"), r.PathValue("versionId"), actorOf(user, r)); err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request, user model.User) {
	node, version, err := s.Files.ResolveVersion(r.PathValue("id"), r.URL.Query().Get("version"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	f, err := s.Files.Store.Open(version.ObjectID)
	if err != nil {
		_ = s.Audit.Log(audit.Event{
			ActorID: user.ID, ActorName: user.DisplayName, Action: "download", TargetType: "file",
			TargetID: node.ID, TargetName: node.Name, TargetPath: node.Path,
			Detail: fmt.Sprintf("V%d 内容缺失", version.VersionNo), Result: audit.ResultFailure, IP: requestIP(r),
		})
		s.writeErr(w, r, apperr.Internal("文件内容缺失，请联系管理员检查存储或备份"))
		return
	}
	defer f.Close()
	modTime, err := time.Parse(model.TimeFormat, version.CreatedAt)
	if err != nil {
		modTime = time.Now()
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", attachmentHeader(node.Name))
	w.Header().Set("ETag", `"`+version.SHA256+`"`)
	w.Header().Set("Cache-Control", "private, max-age=0, must-revalidate")
	cw := &countingWriter{ResponseWriter: w}
	http.ServeContent(cw, r, node.Name, modTime, f)
	_ = s.Audit.Log(audit.Event{
		ActorID: user.ID, ActorName: user.DisplayName, Action: "download", TargetType: "file",
		TargetID: node.ID, TargetName: node.Name, TargetPath: node.Path,
		Detail: fmt.Sprintf("下载 V%d，声明 %d 字节，实际发送 %d 字节", version.VersionNo, version.Size, cw.n),
		Result: audit.ResultSuccess, IP: requestIP(r),
	})
}

func (s *Server) handleFolderDownload(w http.ResponseWriter, r *http.Request, user model.User) {
	root, err := s.Files.GetActive(r.PathValue("id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if root.Kind != model.KindFolder {
		s.writeErr(w, r, apperr.BadRequest("NOT_FOLDER", "只能下载文件夹"))
		return
	}
	entries, err := s.Files.ArchiveEntries(root.ID)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", attachmentHeader(root.Name+".zip"))
	w.Header().Set("Cache-Control", "no-store")
	cw := &countingWriter{ResponseWriter: w}
	zw := zip.NewWriter(cw)
	rootHeader := &zip.FileHeader{Name: root.Name + "/", Method: zip.Store}
	rootHeader.SetMode(os.ModeDir | 0o755)
	if _, err := zw.CreateHeader(rootHeader); err != nil {
		log.Printf("创建文件夹压缩包失败：%v", err)
		return
	}
	var files int
	var total int64
	fail := func(detail string) {
		_ = s.Audit.Log(audit.Event{
			ActorID: user.ID, ActorName: user.DisplayName, Action: "download_folder", TargetType: "folder",
			TargetID: root.ID, TargetName: root.Name, TargetPath: root.Path,
			Detail: detail, Result: audit.ResultFailure, IP: requestIP(r),
		})
	}
	for _, entry := range entries {
		name := root.Name + "/" + entry.Path
		if entry.Kind == model.KindFolder {
			h := &zip.FileHeader{Name: name + "/", Method: zip.Store}
			h.SetMode(os.ModeDir | 0o755)
			if _, err := zw.CreateHeader(h); err != nil {
				fail("写入文件夹条目失败：" + err.Error())
				return
			}
			continue
		}
		f, err := s.Files.Store.Open(entry.ObjectID)
		if err != nil {
			log.Printf("文件夹下载缺少对象 %s：%v", entry.ObjectID, err)
			fail("文件内容缺失：" + entry.Path)
			return
		}
		info, statErr := f.Stat()
		if statErr != nil {
			f.Close()
			fail("读取文件信息失败：" + entry.Path)
			return
		}
		h, err := zip.FileInfoHeader(info)
		if err != nil {
			f.Close()
			fail("创建文件条目失败：" + entry.Path)
			return
		}
		h.Name = name
		h.Method = zip.Deflate
		fw, err := zw.CreateHeader(h)
		if err == nil {
			_, err = io.Copy(fw, f)
		}
		f.Close()
		if err != nil {
			log.Printf("写入压缩包失败：%v", err)
			fail("写入文件失败：" + entry.Path)
			return
		}
		files++
		total += entry.Size
	}
	if err := zw.Close(); err != nil {
		log.Printf("结束文件夹压缩包失败：%v", err)
		fail("结束压缩包失败：" + err.Error())
		return
	}
	_ = s.Audit.Log(audit.Event{
		ActorID: user.ID, ActorName: user.DisplayName, Action: "download_folder", TargetType: "folder",
		TargetID: root.ID, TargetName: root.Name, TargetPath: root.Path,
		Detail: fmt.Sprintf("下载文件夹 ZIP，%d 个文件，原文 %d 字节，发送 %d 字节", files, total, cw.n),
		Result: audit.ResultSuccess, IP: requestIP(r),
	})
}

type countingWriter struct {
	http.ResponseWriter
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.ResponseWriter.Write(p)
	c.n += int64(n)
	return n, err
}

func attachmentHeader(name string) string {
	var fallback strings.Builder
	for _, r := range name {
		if r < 128 && r != '"' && r != '\\' && r >= 32 {
			fallback.WriteRune(r)
		}
	}
	if fallback.Len() == 0 {
		fallback.WriteString("download")
	}
	return fmt.Sprintf("attachment; filename=\"%s\"; filename*=UTF-8''%s", fallback.String(), url.PathEscape(name))
}

func actorOf(user model.User, r *http.Request) files.Actor {
	return files.Actor{ID: user.ID, DisplayName: user.DisplayName, Role: user.Role, IP: requestIP(r)}
}

type uploadConflict struct {
	Message  string
	Existing *files.Node
}

func (s *Server) writeUploadConflict(w http.ResponseWriter, r *http.Request, message string, existing *files.Node) {
	s.writeJSON(w, http.StatusConflict, map[string]any{
		"error":    map[string]string{"code": "NAME_EXISTS", "message": message},
		"existing": existing,
	})
}

func (s *Server) beginUploadTask(id, userID string) (*uploadTask, error) {
	now := model.Now()
	_, err := s.DB.Exec(`INSERT INTO upload_tasks(id, user_id, status, created_at, updated_at)
		VALUES(?, ?, 'pending', ?, ?)`, id, userID, now, now)
	if err == nil {
		return &uploadTask{ID: id, UserID: userID, Status: "pending"}, nil
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unique") {
		return nil, err
	}
	var t uploadTask
	err = s.DB.QueryRow(`SELECT id, user_id, status, node_id, version_id, error
		FROM upload_tasks WHERE id = ?`, id).Scan(&t.ID, &t.UserID, &t.Status, &t.NodeID, &t.VersionID, &t.Error)
	if err != nil {
		return nil, err
	}
	if t.UserID != userID {
		return nil, apperr.Conflict("UPLOAD_ID_CONFLICT", "上传任务编号已被使用")
	}
	if t.Status == "completed" {
		return &t, nil
	}
	if t.Status == "pending" {
		return nil, apperr.Conflict("UPLOAD_IN_PROGRESS", "该上传任务正在处理中，请稍后查询结果")
	}
	_, err = s.DB.Exec(`UPDATE upload_tasks SET status = 'pending', error = '', node_id = '', version_id = '',
		updated_at = ? WHERE id = ?`, now, id)
	if err != nil {
		return nil, err
	}
	return &uploadTask{ID: id, UserID: userID, Status: "pending"}, nil
}

func (s *Server) markTaskFailed(id string, err error) {
	_, _ = s.DB.Exec(`UPDATE upload_tasks SET status = 'failed', error = ?, updated_at = ? WHERE id = ?`,
		err.Error(), model.Now(), id)
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request, user model.User) {
	if !user.CanEdit() {
		s.writeErr(w, r, apperr.Forbidden("当前账号只有查看权限，不能上传"))
		return
	}
	q := r.URL.Query()
	uploadID := q.Get("upload_id")
	if !uploadIDPattern.MatchString(uploadID) {
		s.writeErr(w, r, apperr.BadRequest("INVALID_UPLOAD_ID", "上传任务编号无效"))
		return
	}
	name := q.Get("name")
	note := q.Get("note")
	nodeID := q.Get("node")
	base := q.Get("base")
	parent := q.Get("parent")

	task, err := s.beginUploadTask(uploadID, user.ID)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if task.Status == "completed" {
		s.writeJSON(w, http.StatusOK, map[string]any{
			"upload_id": task.ID, "node_id": task.NodeID, "version_id": task.VersionID, "duplicate": true,
		})
		return
	}

	maxSize := s.Settings.GetInt64(settings.KeyMaxFileSize, 2<<30)
	if maxSize < 1<<20 {
		maxSize = 2 << 30
	}
	if r.ContentLength > maxSize {
		s.markTaskFailed(uploadID, apperr.TooLarge("文件超过单个文件大小限制"))
		s.writeErr(w, r, apperr.TooLarge(fmt.Sprintf("文件超过 %s 的大小限制", humanSize(maxSize))))
		return
	}
	reserve := s.Settings.GetInt64(settings.KeyFreeReserve, 5<<30)
	if r.ContentLength > 0 {
		if free, err := s.Files.Store.FreeSpace(); err == nil && free-r.ContentLength < reserve {
			s.markTaskFailed(uploadID, errors.New("磁盘可用空间不足"))
			s.writeErr(w, r, apperr.New(507, "NO_SPACE", "服务器磁盘空间不足，暂时无法上传"))
			return
		}
	}

	if nodeID != "" {
		target, err := s.Files.GetActive(nodeID)
		if err != nil {
			s.markTaskFailed(uploadID, err)
			s.writeErr(w, r, err)
			return
		}
		if target.Kind != model.KindFile {
			s.markTaskFailed(uploadID, errors.New("目标不是文件"))
			s.writeErr(w, r, apperr.BadRequest("NOT_FILE", "目标不是文件"))
			return
		}
		if base == "" || base != target.CurrentVersionID {
			s.markTaskFailed(uploadID, errors.New("版本冲突"))
			s.writeJSON(w, http.StatusConflict, map[string]any{
				"error":    map[string]string{"code": "VERSION_CONFLICT", "message": "文件已被他人更新，请刷新后重试"},
				"existing": target,
			})
			return
		}
	} else {
		existing, exists, err := s.Files.CheckNewName(parent, name)
		if err != nil {
			s.markTaskFailed(uploadID, err)
			s.writeErr(w, r, err)
			return
		}
		if exists {
			s.markTaskFailed(uploadID, apperr.Conflict("NAME_EXISTS", "同名项目已存在"))
			s.writeUploadConflict(w, r, "同一目录下已存在同名项目，请选择替换为新版本或改名", &existing)
			return
		}
	}

	obj, err := s.Files.Store.Save(r.Body, maxSize)
	if err != nil {
		if errors.Is(err, storage.ErrTooLarge) {
			s.markTaskFailed(uploadID, err)
			s.writeErr(w, r, apperr.TooLarge(fmt.Sprintf("文件超过 %s 的大小限制", humanSize(maxSize))))
			return
		}
		s.markTaskFailed(uploadID, err)
		_ = s.Audit.Log(audit.Event{
			ActorID: user.ID, ActorName: user.DisplayName, Action: "upload_failed", TargetType: "file",
			TargetName: name, Detail: "接收文件失败：" + err.Error(), Result: audit.ResultFailure, IP: requestIP(r),
		})
		s.writeErr(w, r, apperr.BadRequest("UPLOAD_ABORTED", "上传中断，请重试"))
		return
	}
	if free, err := s.Files.Store.FreeSpace(); err == nil && free-obj.Size < reserve {
		_ = s.Files.Store.DeleteObject(obj.ID)
		s.markTaskFailed(uploadID, errors.New("磁盘可用空间不足"))
		s.writeErr(w, r, apperr.New(507, "NO_SPACE", "服务器磁盘空间不足，文件未保存"))
		return
	}

	var result files.UploadResult
	if nodeID != "" {
		result, err = s.Files.UploadVersion(nodeID, base, note, obj, actorOf(user, r))
	} else {
		result, err = s.Files.UploadNew(parent, name, note, obj, actorOf(user, r))
	}
	if err != nil {
		_ = s.Files.Store.DeleteObject(obj.ID)
		s.markTaskFailed(uploadID, err)
		_ = s.Audit.Log(audit.Event{
			ActorID: user.ID, ActorName: user.DisplayName, Action: "upload_failed", TargetType: "file",
			TargetName: name, Detail: err.Error(), Result: audit.ResultFailure, IP: requestIP(r),
		})
		s.writeErr(w, r, err)
		return
	}
	_, _ = s.DB.Exec(`UPDATE upload_tasks SET status = 'completed', node_id = ?, version_id = ?, updated_at = ?
		WHERE id = ?`, result.Node.ID, result.Version.ID, model.Now(), uploadID)
	s.writeJSON(w, http.StatusOK, map[string]any{
		"upload_id": uploadID, "node": result.Node, "version": result.Version,
	})
}

func (s *Server) handleUploadStatus(w http.ResponseWriter, r *http.Request, user model.User) {
	id := r.PathValue("id")
	var t uploadTask
	err := s.DB.QueryRow(`SELECT id, user_id, status, node_id, version_id, error FROM upload_tasks WHERE id = ?`, id).
		Scan(&t.ID, &t.UserID, &t.Status, &t.NodeID, &t.VersionID, &t.Error)
	if err != nil {
		s.writeErr(w, r, apperr.NotFound("上传任务不存在"))
		return
	}
	if t.UserID != user.ID && !user.IsAdmin() {
		s.writeErr(w, r, apperr.Forbidden("无权查看该上传任务"))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"upload_id": t.ID, "status": t.Status, "node_id": t.NodeID,
		"version_id": t.VersionID, "error": t.Error,
	})
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}
