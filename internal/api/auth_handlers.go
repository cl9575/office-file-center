package api

import (
	"net/http"
	"strings"

	"lanfile/internal/apperr"
	"lanfile/internal/audit"
	"lanfile/internal/model"
)

type authState struct {
	User     model.User `json:"user"`
	Settings any        `json:"settings"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "version": s.Version, "tls": s.TLSEnabled,
	})
}

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	hasUsers, err := s.Auth.HasUsers()
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"needs_setup":      !hasUsers,
		"local":            isLoopback(r),
		"site_name":        s.Settings.SiteName(),
		"version":          s.Version,
		"tls_enabled":      s.TLSEnabled,
		"cert_fingerprint": s.Cert.Fingerprint,
		"cert_not_after":   s.Cert.NotAfter,
	})
}

func (s *Server) handleSetupAdmin(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r) {
		s.writeErr(w, r, apperr.Forbidden("首次初始化只能在这台主机上操作"))
		return
	}
	hasUsers, err := s.Auth.HasUsers()
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if hasUsers {
		s.writeErr(w, r, apperr.Conflict("ALREADY_SETUP", "系统已经完成初始化"))
		return
	}
	var in struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
	}
	if !s.decodeJSON(w, r, &in) {
		return
	}
	user, err := s.Auth.CreateInitialAdmin(in.Username, in.DisplayName, in.Password)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	ip := requestIP(r)
	_, token, err := s.Auth.Login(in.Username, in.Password, ip, r.UserAgent())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.setSessionCookie(w, r, token)
	_ = s.Audit.Log(audit.Event{
		ActorID: user.ID, ActorName: user.DisplayName, Action: "setup_admin",
		TargetType: "system", TargetName: "系统初始化", Detail: "创建初始管理员",
		Result: audit.ResultSuccess, IP: ip,
	})
	s.writeJSON(w, http.StatusCreated, authState{User: user, Settings: s.Settings.Public()})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !s.decodeJSON(w, r, &in) {
		return
	}
	in.Username = strings.TrimSpace(in.Username)
	if len(in.Username) > 100 || len(in.Password) > 256 {
		s.writeErr(w, r, apperr.BadRequest("INVALID_INPUT", "用户名或密码长度无效"))
		return
	}
	ip := requestIP(r)
	user, token, err := s.Auth.Login(in.Username, in.Password, ip, r.UserAgent())
	if err != nil {
		_ = s.Audit.Log(audit.Event{
			ActorName: in.Username, Action: "login_failed", TargetType: "auth",
			TargetName: in.Username, Detail: err.Error(), Result: audit.ResultFailure, IP: ip,
		})
		s.writeErr(w, r, err)
		return
	}
	s.setSessionCookie(w, r, token)
	_ = s.Audit.Log(audit.Event{
		ActorID: user.ID, ActorName: user.DisplayName, Action: "login_success",
		TargetType: "auth", TargetName: user.Username, Detail: "登录成功",
		Result: audit.ResultSuccess, IP: ip,
	})
	s.writeJSON(w, http.StatusOK, authState{User: user, Settings: s.Settings.Public()})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, user model.User) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		s.Auth.Logout(cookie.Value)
	}
	s.clearSessionCookie(w)
	_ = s.Audit.Log(audit.Event{
		ActorID: user.ID, ActorName: user.DisplayName, Action: "logout",
		TargetType: "auth", TargetName: user.Username, Result: audit.ResultSuccess, IP: requestIP(r),
	})
	s.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, user model.User) {
	s.writeJSON(w, http.StatusOK, map[string]any{
		"user": user, "settings": s.Settings.Public(), "version": s.Version,
		"tls_enabled": s.TLSEnabled,
	})
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request, user model.User) {
	var in struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if !s.decodeJSON(w, r, &in) {
		return
	}
	token, err := s.Auth.ChangePassword(user.ID, in.OldPassword, in.NewPassword)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.setSessionCookie(w, r, token)
	_ = s.Audit.Log(audit.Event{
		ActorID: user.ID, ActorName: user.DisplayName, Action: "password_change",
		TargetType: "user", TargetID: user.ID, TargetName: user.Username,
		Detail: "修改自己的登录密码", Result: audit.ResultSuccess, IP: requestIP(r),
	})
	s.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
