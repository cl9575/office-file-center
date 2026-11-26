package api

import (
	"bytes"
	"encoding/csv"
	"encoding/pem"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lanfile/internal/apperr"
	"lanfile/internal/audit"
	"lanfile/internal/backup"
	"lanfile/internal/certsetup"
	"lanfile/internal/model"
	"lanfile/internal/settings"
)

func (s *Server) handleListRecycle(w http.ResponseWriter, r *http.Request, user model.User) {
	items, err := s.Files.ListRecycle(actorOf(user, r))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleRestoreRecycle(w http.ResponseWriter, r *http.Request, user model.User) {
	count, err := s.Files.RestoreBatch(r.PathValue("batch"), actorOf(user, r))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"restored": count})
}

func (s *Server) handlePurgeRecycle(w http.ResponseWriter, r *http.Request, user model.User) {
	count, err := s.Files.PurgeBatch(r.PathValue("batch"), actorOf(user, r))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"purged": count})
}

func (s *Server) handlePurgeAllRecycle(w http.ResponseWriter, r *http.Request, user model.User) {
	count, err := s.Files.PurgeAll(actorOf(user, r))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"purged": count})
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request, user model.User) {
	users, err := s.Auth.ListUsers()
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"items": users})
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request, user model.User) {
	var in struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
		Role        string `json:"role"`
	}
	if !s.decodeJSON(w, r, &in) {
		return
	}
	created, err := s.Auth.CreateUser(user.Actor(), in.Username, in.DisplayName, in.Password, in.Role)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	_ = s.Audit.Log(audit.Event{
		ActorID: user.ID, ActorName: user.DisplayName, Action: "user_create", TargetType: "user",
		TargetID: created.ID, TargetName: created.Username,
		Detail: "创建账号，角色：" + model.RoleName(created.Role), Result: audit.ResultSuccess, IP: requestIP(r),
	})
	s.writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request, user model.User) {
	var in struct {
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
		Disabled    bool   `json:"disabled"`
	}
	if !s.decodeJSON(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	updated, err := s.Auth.UpdateUser(user.Actor(), id, in.DisplayName, in.Role, in.Disabled)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	detail := fmt.Sprintf("角色：%s，状态：%s", model.RoleName(in.Role), map[bool]string{true: "停用", false: "启用"}[in.Disabled])
	_ = s.Audit.Log(audit.Event{
		ActorID: user.ID, ActorName: user.DisplayName, Action: "user_update", TargetType: "user",
		TargetID: id, TargetName: updated.DisplayName, Detail: detail, Result: audit.ResultSuccess, IP: requestIP(r),
	})
	s.writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request, user model.User) {
	id := r.PathValue("id")
	deleted, err := s.Auth.DeleteUser(user.Actor(), id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	_ = s.Audit.Log(audit.Event{
		ActorID: user.ID, ActorName: user.DisplayName, Action: "user_delete", TargetType: "user",
		TargetID: id, TargetName: deleted.Username,
		Detail: "删除账号，角色：" + model.RoleName(deleted.Role), Result: audit.ResultSuccess, IP: requestIP(r),
	})
	s.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request, user model.User) {
	var in struct {
		Password string `json:"password"`
	}
	if !s.decodeJSON(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	if err := s.Auth.ResetPassword(user.Actor(), id, in.Password); err != nil {
		s.writeErr(w, r, err)
		return
	}
	_ = s.Audit.Log(audit.Event{
		ActorID: user.ID, ActorName: user.DisplayName, Action: "user_reset_password", TargetType: "user",
		TargetID: id, Detail: "重置密码，要求下次登录修改", Result: audit.ResultSuccess, IP: requestIP(r),
	})
	s.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func normalizeAuditTime(value string, end bool) string {
	value = strings.TrimSpace(value)
	if len(value) == 10 {
		if end {
			return value + "T23:59:59.999Z"
		}
		return value + "T00:00:00.000Z"
	}
	return value
}

func auditFilter(r *http.Request) audit.Filter {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(q.Get("page_size"))
	if size < 1 || size > 200 {
		size = 50
	}
	return audit.Filter{
		Actor:  q.Get("actor"),
		Action: q.Get("action"),
		Result: q.Get("result"),
		Query:  q.Get("q"),
		From:   normalizeAuditTime(q.Get("from"), false),
		To:     normalizeAuditTime(q.Get("to"), true),
		Limit:  size,
		Offset: (page - 1) * size,
	}
}

func (s *Server) handleAuditList(w http.ResponseWriter, r *http.Request, user model.User) {
	f := auditFilter(r)
	f.HideSecure = !user.IsOwner
	rows, total, err := s.Audit.List(f)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"items": rows, "total": total, "page": f.Offset/f.Limit + 1, "page_size": f.Limit,
	})
}

var actionLabels = map[string]string{
	"login_success": "登录成功", "login_failed": "登录失败", "logout": "退出登录",
	"password_change": "修改密码", "setup_admin": "创建初始管理员",
	"user_create": "创建账号", "user_update": "修改账号", "user_reset_password": "重置密码", "user_delete": "删除账号",
	"folder_create": "新建目录", "upload_new": "上传文件", "upload_version": "上传新版本",
	"upload_failed": "上传失败", "rename": "重命名", "move": "移动", "delete": "移入回收站",
	"recycle_restore": "恢复回收站", "recycle_purge": "永久删除", "recycle_purge_all": "清空回收站",
	"version_restore": "恢复历史版本", "version_purge": "清理历史版本",
	"download": "下载", "settings_update": "修改设置",
	"backup_success": "备份成功", "backup_failed": "备份失败", "backup_manual": "手动备份",
}

func actionLabel(action string) string {
	if label, ok := actionLabels[action]; ok {
		return label
	}
	return action
}

func (s *Server) handleAuditExport(w http.ResponseWriter, r *http.Request, user model.User) {
	f := auditFilter(r)
	f.HideSecure = !user.IsOwner
	rows, err := s.Audit.All(f, 10000)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	var buf bytes.Buffer
	buf.WriteString("\xEF\xBB\xBF")
	cw := csv.NewWriter(&buf)
	cw.UseCRLF = true
	_ = cw.Write([]string{"时间", "操作人", "操作", "对象类型", "对象名称", "路径", "详情", "结果", "来源 IP"})
	for _, row := range rows {
		_ = cw.Write([]string{
			row.CreatedAt, audit.CSVSafe(row.ActorName), audit.CSVSafe(actionLabel(row.Action)),
			row.TargetType, audit.CSVSafe(row.TargetName), audit.CSVSafe(row.TargetPath),
			audit.CSVSafe(row.Detail), row.Result, row.IP,
		})
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="audit-%s.csv"`, time.Now().Format("20060102")))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request, user model.User) {
	s.writeJSON(w, http.StatusOK, s.Settings.Admin())
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request, user model.User) {
	var in struct {
		SiteName             *string `json:"site_name"`
		MaxFileSize          *int64  `json:"max_file_size"`
		FreeSpaceReserve     *int64  `json:"free_space_reserve"`
		RecycleRetentionDays *int    `json:"recycle_retention_days"`
		BackupEnabled        *bool   `json:"backup_enabled"`
		BackupTime           *string `json:"backup_time"`
		BackupKeep           *int    `json:"backup_keep"`
		BackupDir            *string `json:"backup_dir"`
	}
	if !s.decodeJSON(w, r, &in) {
		return
	}
	values := map[string]string{}
	if in.SiteName != nil {
		values[settings.KeySiteName] = *in.SiteName
	}
	if in.MaxFileSize != nil {
		values[settings.KeyMaxFileSize] = strconv.FormatInt(*in.MaxFileSize, 10)
	}
	if in.FreeSpaceReserve != nil {
		values[settings.KeyFreeReserve] = strconv.FormatInt(*in.FreeSpaceReserve, 10)
	}
	if in.RecycleRetentionDays != nil {
		values[settings.KeyRecycleDays] = strconv.Itoa(*in.RecycleRetentionDays)
	}
	if in.BackupEnabled != nil {
		values[settings.KeyBackupEnabled] = strconv.FormatBool(*in.BackupEnabled)
	}
	if in.BackupTime != nil {
		values[settings.KeyBackupTime] = *in.BackupTime
	}
	if in.BackupKeep != nil {
		values[settings.KeyBackupKeep] = strconv.Itoa(*in.BackupKeep)
	}
	if in.BackupDir != nil {
		dir := strings.TrimSpace(*in.BackupDir)
		if dir != "" {
			abs, err := filepath.Abs(dir)
			if err != nil {
				s.writeErr(w, r, apperr.BadRequest("INVALID_BACKUP_DIR", "备份目录路径无效"))
				return
			}
			dataAbs, _ := filepath.Abs(s.Cfg.DataDir)
			if rel, err := filepath.Rel(dataAbs, abs); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				s.writeErr(w, r, apperr.BadRequest("INVALID_BACKUP_DIR", "备份目录不能位于数据目录内"))
				return
			}
			dir = abs
		}
		values[settings.KeyBackupDir] = dir
	}
	if len(values) == 0 {
		s.writeErr(w, r, apperr.BadRequest("NO_CHANGE", "没有提供设置项"))
		return
	}
	if err := s.Settings.Update(values); err != nil {
		s.writeErr(w, r, err)
		return
	}
	var changed []string
	for key := range values {
		changed = append(changed, key)
	}
	_ = s.Audit.Log(audit.Event{
		ActorID: user.ID, ActorName: user.DisplayName, Action: "settings_update", TargetType: "system",
		TargetName: "系统设置", Detail: "修改：" + strings.Join(changed, ", "),
		Result: audit.ResultSuccess, IP: requestIP(r),
	})
	s.writeJSON(w, http.StatusOK, s.Settings.Admin())
}

type systemResponse struct {
	Stats          any            `json:"stats"`
	Users          int            `json:"users"`
	ActiveSessions int            `json:"active_sessions"`
	BackupRunning  bool           `json:"backup_running"`
	LastBackup     *backup.Run    `json:"last_backup"`
	RecentBackups  []backup.Run   `json:"recent_backups"`
	Cert           certsetup.Info `json:"cert"`
	TLSEnabled     bool           `json:"tls_enabled"`
	DataDir        string         `json:"data_dir"`
	Version        string         `json:"version"`
	StartedAt      string         `json:"started_at"`
}

func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request, user model.User) {
	stats, err := s.Files.Stats()
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	users, _ := s.Auth.CountUsers()
	var sessions int
	_ = s.DB.QueryRow(`SELECT COUNT(1) FROM sessions WHERE expires_at > ?`, model.Now()).Scan(&sessions)
	last, _ := s.Backup.LastSuccess()
	recent, _ := s.Backup.ListRuns(10)
	if recent == nil {
		recent = []backup.Run{}
	}
	s.writeJSON(w, http.StatusOK, systemResponse{
		Stats: stats, Users: users, ActiveSessions: sessions,
		BackupRunning: s.Backup.Running(), LastBackup: last, RecentBackups: recent,
		Cert: s.Cert, TLSEnabled: s.TLSEnabled, DataDir: s.Cfg.DataDir,
		Version: s.Version, StartedAt: s.StartedAt.UTC().Format(model.TimeFormat),
	})
}

func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request, user model.User) {
	runs, err := s.Backup.ListRuns(30)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	last, _ := s.Backup.LastSuccess()
	s.writeJSON(w, http.StatusOK, map[string]any{
		"items": runs, "running": s.Backup.Running(), "last_success": last,
	})
}

func (s *Server) handleRunBackup(w http.ResponseWriter, r *http.Request, user model.User) {
	if s.Backup.Running() {
		s.writeErr(w, r, apperr.Conflict("BACKUP_RUNNING", "备份正在进行中"))
		return
	}
	if s.Settings.Get(settings.KeyBackupDir, "") == "" {
		s.writeErr(w, r, apperr.BadRequest("NO_BACKUP_DIR", "请先在系统设置中配置备份目录"))
		return
	}
	_ = s.Audit.Log(audit.Event{
		ActorID: user.ID, ActorName: user.DisplayName, Action: "backup_manual", TargetType: "system",
		TargetName: "手动备份", Detail: "管理员触发", Result: audit.ResultSuccess, IP: requestIP(r),
	})
	go func() {
		if _, err := s.Backup.RunNow("manual"); err != nil {
			log.Printf("手动备份失败：%v", err)
		}
	}()
	s.writeJSON(w, http.StatusAccepted, map[string]string{"message": "备份任务已开始，可在本页查看进度"})
}

func (s *Server) handleCertDownload(w http.ResponseWriter, r *http.Request) {
	if s.Cert.CertFile == "" {
		s.writeErr(w, r, apperr.NotFound("当前未启用 HTTPS"))
		return
	}
	body, err := os.ReadFile(s.Cert.CertFile)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if block, _ := pem.Decode(body); block != nil {
		body = block.Bytes
	}
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="office-files-cert.cer"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (s *Server) handleServerCertDownload(w http.ResponseWriter, r *http.Request) {
	if s.CertPath == "" {
		s.writeErr(w, r, apperr.NotFound("当前未启用 HTTPS"))
		return
	}
	body, err := os.ReadFile(s.CertPath)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if block, _ := pem.Decode(body); block != nil {
		body = block.Bytes
	}
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="office-files-server.cer"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
