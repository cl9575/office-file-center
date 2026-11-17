package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"lanfile/internal/apperr"
	"lanfile/internal/audit"
	"lanfile/internal/auth"
	"lanfile/internal/backup"
	"lanfile/internal/certsetup"
	"lanfile/internal/config"
	"lanfile/internal/files"
	"lanfile/internal/model"
	"lanfile/internal/settings"
	"lanfile/internal/webui"
)

const sessionCookie = "lfp_session"

type Server struct {
	Cfg        *config.Config
	DB         *sql.DB
	Auth       *auth.Service
	Files      *files.Service
	Audit      *audit.Service
	Settings   *settings.Service
	Backup     *backup.Service
	Cert       certsetup.Info
	TLSEnabled bool
	CertPath   string
	KeyPath    string
	Version    string
	StartedAt  time.Time
}

func New(cfg *config.Config, db *sql.DB, authSvc *auth.Service, filesSvc *files.Service,
	auditSvc *audit.Service, settingsSvc *settings.Service, backupSvc *backup.Service,
	cert certsetup.Info, tlsEnabled bool, certPath, keyPath, version string) *Server {
	return &Server{
		Cfg: cfg, DB: db, Auth: authSvc, Files: filesSvc, Audit: auditSvc,
		Settings: settingsSvc, Backup: backupSvc, Cert: cert,
		TLSEnabled: tlsEnabled, CertPath: certPath, KeyPath: keyPath,
		Version: version, StartedAt: time.Now(),
	}
}

type authedHandler func(http.ResponseWriter, *http.Request, model.User)

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/setup/status", s.handleSetupStatus)
	mux.HandleFunc("POST /api/setup/admin", s.handleSetupAdmin)
	mux.HandleFunc("GET /api/cert", s.handleCertDownload)
	mux.HandleFunc("GET /api/server-cert", s.handleServerCertDownload)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)

	mux.HandleFunc("GET /api/auth/me", s.authed(s.handleMe))
	mux.HandleFunc("POST /api/auth/logout", s.authed(s.handleLogout))
	mux.HandleFunc("POST /api/auth/change-password", s.authed(s.handleChangePassword))

	mux.HandleFunc("GET /api/nodes", s.authed(s.handleListNodes))
	mux.HandleFunc("POST /api/nodes", s.authed(s.handleCreateNode))
	mux.HandleFunc("GET /api/nodes/resolve", s.authed(s.handleResolve))
	mux.HandleFunc("GET /api/folders/tree", s.authed(s.handleFolderTree))
	mux.HandleFunc("GET /api/nodes/{id}", s.authed(s.handleNodeDetail))
	mux.HandleFunc("PATCH /api/nodes/{id}", s.authed(s.handlePatchNode))
	mux.HandleFunc("DELETE /api/nodes/{id}", s.authed(s.handleDeleteNode))
	mux.HandleFunc("GET /api/nodes/{id}/breadcrumbs", s.authed(s.handleBreadcrumbs))
	mux.HandleFunc("GET /api/nodes/{id}/versions", s.authed(s.handleVersions))
	mux.HandleFunc("GET /api/nodes/{id}/audit", s.authed(s.handleNodeAudit))
	mux.HandleFunc("GET /api/nodes/{id}/download", s.authed(s.handleDownload))
	mux.HandleFunc("GET /api/nodes/{id}/download-folder", s.authed(s.handleFolderDownload))
	mux.HandleFunc("POST /api/nodes/{id}/restore-version", s.authed(s.handleRestoreVersion))
	mux.HandleFunc("DELETE /api/nodes/{id}/versions/{versionId}", s.authed(s.handlePurgeVersion))

	mux.HandleFunc("PUT /api/upload", s.authed(s.handleUpload))
	mux.HandleFunc("GET /api/upload/{id}", s.authed(s.handleUploadStatus))

	mux.HandleFunc("GET /api/recycle", s.admin(s.handleListRecycle))
	mux.HandleFunc("POST /api/recycle/{batch}/restore", s.admin(s.handleRestoreRecycle))
	mux.HandleFunc("DELETE /api/recycle/{batch}", s.admin(s.handlePurgeRecycle))
	mux.HandleFunc("DELETE /api/recycle", s.admin(s.handlePurgeAllRecycle))

	mux.HandleFunc("GET /api/admin/users", s.admin(s.handleListUsers))
	mux.HandleFunc("POST /api/admin/users", s.admin(s.handleCreateUser))
	mux.HandleFunc("PATCH /api/admin/users/{id}", s.admin(s.handleUpdateUser))
	mux.HandleFunc("DELETE /api/admin/users/{id}", s.admin(s.handleDeleteUser))
	mux.HandleFunc("POST /api/admin/users/{id}/reset-password", s.admin(s.handleResetPassword))
	mux.HandleFunc("GET /api/admin/audit", s.admin(s.handleAuditList))
	mux.HandleFunc("GET /api/admin/audit.csv", s.admin(s.handleAuditExport))
	mux.HandleFunc("GET /api/admin/settings", s.admin(s.handleGetSettings))
	mux.HandleFunc("PUT /api/admin/settings", s.admin(s.handleUpdateSettings))
	mux.HandleFunc("GET /api/admin/system", s.admin(s.handleSystem))
	mux.HandleFunc("GET /api/admin/backups", s.admin(s.handleListBackups))
	mux.HandleFunc("POST /api/admin/backups/run", s.admin(s.handleRunBackup))

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		s.writeErr(w, r, apperr.NotFound("接口不存在"))
	})

	root, _ := fs.Sub(webui.Dist, "dist")
	fileServer := http.FileServer(http.FS(root))
	static := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		clean := path.Clean("/" + r.URL.Path)
		if strings.HasPrefix(clean, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		name := strings.TrimPrefix(clean, "/")
		if name == "" {
			name = "index.html"
		}
		if _, err := fs.Stat(root, name); err != nil {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})

	dispatch := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			mux.ServeHTTP(w, r)
			return
		}
		static.ServeHTTP(w, r)
	})
	return s.securityHeaders(s.csrf(dispatch))
}

func (s *Server) Run(ctx context.Context) error {
	s.Backup.RecoverInterrupted()
	s.Backup.StartScheduler(ctx)
	go s.maintenance(ctx)

	srv := &http.Server{
		Addr:              s.Cfg.Addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 20 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	errCh := make(chan error, 1)
	go func() {
		var err error
		if s.TLSEnabled {
			log.Printf("HTTPS 服务启动于 %s，证书 %s", s.Cfg.Addr, s.CertPath)
			err = srv.ListenAndServeTLS(s.CertPath, s.KeyPath)
		} else {
			log.Printf("警告：HTTP 明文模式启动于 %s，仅应临时使用", s.Cfg.Addr)
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func (s *Server) maintenance(ctx context.Context) {
	s.runMaintenance()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runMaintenance()
		}
	}
}

func (s *Server) runMaintenance() {
	if removed, err := s.Files.GC(); err != nil {
		log.Printf("存储清理失败: %v", err)
	} else if removed > 0 {
		log.Printf("存储清理完成，移除 %d 个临时或孤立文件", removed)
	}
	_, _ = s.DB.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, model.Now())
	_, _ = s.DB.Exec(`UPDATE upload_tasks SET status = 'failed', error = '上传进程中断', updated_at = ?
		WHERE status = 'pending' AND updated_at < ?`, model.Now(),
		time.Now().AddDate(0, 0, -1).UTC().Format(model.TimeFormat))
	_, _ = s.DB.Exec(`DELETE FROM upload_tasks WHERE status = 'completed' AND updated_at < ?`,
		time.Now().AddDate(0, 0, -90).UTC().Format(model.TimeFormat))
	_, _ = s.DB.Exec(`DELETE FROM upload_tasks WHERE status = 'failed' AND updated_at < ?`,
		time.Now().AddDate(0, 0, -30).UTC().Format(model.TimeFormat))
}

func (s *Server) authed(h authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, err := s.userFromRequest(r)
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		if user.MustChangePassword {
			switch r.URL.Path {
			case "/api/auth/me", "/api/auth/logout", "/api/auth/change-password":
			default:
				s.writeErr(w, r, apperr.New(403, "PASSWORD_CHANGE_REQUIRED", "首次登录或密码重置后，请先修改密码"))
				return
			}
		}
		h(w, r, user)
	}
}

func (s *Server) admin(h authedHandler) http.HandlerFunc {
	return s.authed(func(w http.ResponseWriter, r *http.Request, u model.User) {
		if !u.IsAdmin() {
			s.writeErr(w, r, apperr.Forbidden("只有管理员可以执行此操作"))
			return
		}
		h(w, r, u)
	})
}

func (s *Server) userFromRequest(r *http.Request) (model.User, error) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return model.User{}, apperr.Unauthorized("请先登录")
	}
	return s.Auth.UserByToken(cookie.Value)
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true,
		Secure: r.TLS != nil || s.TLSEnabled, SameSite: http.SameSiteLaxMode,
		MaxAge: 7 * 24 * 3600,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; font-src 'self' data:; connect-src 'self'; "+
				"object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		if s.TLSEnabled {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			if fetchSite := r.Header.Get("Sec-Fetch-Site"); fetchSite == "cross-site" {
				s.writeErr(w, r, apperr.Forbidden("拒绝跨站请求"))
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || !strings.EqualFold(u.Host, r.Host) {
					s.writeErr(w, r, apperr.Forbidden("请求来源无效"))
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		ae = apperr.Internal("服务器内部错误")
		log.Printf("API 错误 %s %s: %v", r.Method, r.URL.Path, err)
	} else if ae.Status >= 500 {
		log.Printf("API 错误 %s %s: %v", r.Method, r.URL.Path, err)
	}
	s.writeJSON(w, ae.Status, map[string]any{
		"error": map[string]string{"code": ae.Code, "message": ae.Message},
	})
}

func (s *Server) decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		s.writeErr(w, r, apperr.BadRequest("INVALID_JSON", "请求内容格式错误"))
		return false
	}
	return true
}

func requestIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func isLoopback(r *http.Request) bool {
	ip := net.ParseIP(requestIP(r))
	return ip != nil && ip.IsLoopback()
}
