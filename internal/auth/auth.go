package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"lanfile/internal/apperr"
	"lanfile/internal/model"
)

const (
	argonMemory      = 64 * 1024
	argonIterations  = 3
	argonParallelism = 2
	argonKeyLength   = 32
	argonSaltLength  = 16
	sessionLifetime  = 7 * 24 * time.Hour
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,32}$`)

type Service struct {
	DB      *sql.DB
	limiter *limiter
}

func New(db *sql.DB) *Service {
	return &Service{DB: db, limiter: newLimiter()}
}

func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyLength)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory, argonIterations, argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false
	}
	var memory uint32
	var iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func ValidatePassword(password string) error {
	n := len([]rune(password))
	if n < 6 {
		return apperr.BadRequest("WEAK_PASSWORD", "密码至少需要 6 个字符")
	}
	if n > 128 {
		return apperr.BadRequest("WEAK_PASSWORD", "密码不能超过 128 个字符")
	}
	return nil
}

func ValidateUsername(username string) error {
	if !usernamePattern.MatchString(username) {
		return apperr.BadRequest("INVALID_USERNAME", "用户名需为 3 到 32 位字母、数字、下划线、点或短横线")
	}
	return nil
}

func normalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Service) HasUsers() (bool, error) {
	var count int
	if err := s.DB.QueryRow(`SELECT COUNT(1) FROM users`).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Service) CreateInitialAdmin(username, displayName, password string) (model.User, error) {
	username = strings.TrimSpace(username)
	displayName = strings.TrimSpace(displayName)
	if err := ValidateUsername(username); err != nil {
		return model.User{}, err
	}
	if displayName == "" || len([]rune(displayName)) > 40 {
		return model.User{}, apperr.BadRequest("INVALID_DISPLAY_NAME", "显示名称长度应为 1 到 40 个字符")
	}
	if err := ValidatePassword(password); err != nil {
		return model.User{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return model.User{}, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return model.User{}, err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRow(`SELECT COUNT(1) FROM users`).Scan(&count); err != nil {
		return model.User{}, err
	}
	if count > 0 {
		return model.User{}, apperr.Conflict("ALREADY_SETUP", "系统已经完成初始化")
	}
	id := model.NewID()
	now := model.Now()
	if _, err := tx.Exec(`INSERT INTO users
		(id, username, display_name, password_hash, role, is_owner, must_change_password, disabled, created_at, updated_at, created_by)
		VALUES (?, ?, ?, ?, ?, 1, 0, 0, ?, ?, ?)`,
		id, username, displayName, hash, model.RoleAdmin, now, now, id); err != nil {
		return model.User{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.User{}, err
	}
	return model.User{ID: id, Username: username, DisplayName: displayName, Role: model.RoleAdmin, IsOwner: true, CreatedAt: now}, nil
}

type userRecord struct {
	model.User
	PasswordHash string
}

func (s *Service) Login(username, password, ip, userAgent string) (model.User, string, error) {
	username = strings.TrimSpace(username)
	key := normalizeUsername(username) + "|" + ip
	if err := s.limiter.Allow(key); err != nil {
		return model.User{}, "", err
	}
	var rec userRecord
	var mustChange, disabled, owner int
	err := s.DB.QueryRow(`SELECT id, username, display_name, role, password_hash,
		is_owner, must_change_password, disabled, created_at FROM users WHERE username = ? AND deleted_at = ''`, username).
		Scan(&rec.ID, &rec.Username, &rec.DisplayName, &rec.Role, &rec.PasswordHash,
			&owner, &mustChange, &disabled, &rec.CreatedAt)
	if err == sql.ErrNoRows || (err == nil && !VerifyPassword(rec.PasswordHash, password)) {
		s.limiter.Fail(key)
		return model.User{}, "", apperr.Unauthorized("用户名或密码错误")
	}
	if err != nil {
		return model.User{}, "", err
	}
	if disabled == 1 {
		return model.User{}, "", apperr.Forbidden("账号已停用，请联系管理员")
	}
	rec.IsOwner = owner == 1
	rec.MustChangePassword = mustChange == 1
	rec.Disabled = false
	s.limiter.Success(key)

	token, err := randomToken()
	if err != nil {
		return model.User{}, "", err
	}
	now := time.Now().UTC()
	_, err = s.DB.Exec(`INSERT INTO sessions(token_hash, user_id, created_at, expires_at, ip, user_agent)
		VALUES (?, ?, ?, ?, ?, ?)`,
		tokenHash(token), rec.ID,
		now.Format(model.TimeFormat),
		now.Add(sessionLifetime).Format(model.TimeFormat), ip, userAgent)
	if err != nil {
		return model.User{}, "", err
	}
	_, _ = s.DB.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, model.Now())
	return rec.User, token, nil
}

func (s *Service) UserByToken(token string) (model.User, error) {
	if token == "" {
		return model.User{}, apperr.Unauthorized("请先登录")
	}
	var user model.User
	var mustChange, disabled, owner int
	err := s.DB.QueryRow(`SELECT u.id, u.username, u.display_name, u.role, u.must_change_password,
		u.disabled, u.is_owner, u.created_at FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.expires_at > ? AND u.deleted_at = ''`, tokenHash(token), model.Now()).
		Scan(&user.ID, &user.Username, &user.DisplayName, &user.Role, &mustChange, &disabled, &owner, &user.CreatedAt)
	if err == sql.ErrNoRows {
		return model.User{}, apperr.Unauthorized("登录已过期，请重新登录")
	}
	if err != nil {
		return model.User{}, err
	}
	if disabled == 1 {
		return model.User{}, apperr.Forbidden("账号已停用")
	}
	user.IsOwner = owner == 1
	user.MustChangePassword = mustChange == 1
	return user, nil
}

func (s *Service) Logout(token string) {
	if token == "" {
		return
	}
	_, _ = s.DB.Exec(`DELETE FROM sessions WHERE token_hash = ?`, tokenHash(token))
}

func (s *Service) ChangePassword(userID, oldPassword, newPassword string) (string, error) {
	if err := ValidatePassword(newPassword); err != nil {
		return "", err
	}
	var hash string
	if err := s.DB.QueryRow(`SELECT password_hash FROM users WHERE id = ? AND disabled = 0 AND deleted_at = ''`, userID).Scan(&hash); err != nil {
		if err == sql.ErrNoRows {
			return "", apperr.NotFound("账号不存在")
		}
		return "", err
	}
	if !VerifyPassword(hash, oldPassword) {
		return "", apperr.BadRequest("WRONG_PASSWORD", "原密码不正确")
	}
	if VerifyPassword(hash, newPassword) {
		return "", apperr.BadRequest("SAME_PASSWORD", "新密码不能与原密码相同")
	}
	newHash, err := HashPassword(newPassword)
	if err != nil {
		return "", err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE users SET password_hash = ?, must_change_password = 0, updated_at = ? WHERE id = ?`,
		newHash, model.Now(), userID); err != nil {
		return "", err
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		return "", err
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(`INSERT INTO sessions(token_hash, user_id, created_at, expires_at, ip, user_agent)
		VALUES (?, ?, ?, ?, '', '')`, tokenHash(token), userID,
		now.Format(model.TimeFormat), now.Add(sessionLifetime).Format(model.TimeFormat)); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return token, nil
}

func (s *Service) ListUsers() ([]model.User, error) {
	rows, err := s.DB.Query(`SELECT id, username, display_name, role, is_owner, must_change_password, disabled, created_at
		FROM users WHERE deleted_at = '' ORDER BY is_owner DESC, CASE role WHEN 'admin' THEN 0 WHEN 'editor' THEN 1 ELSE 2 END, username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.User{}
	for rows.Next() {
		var u model.User
		var owner, mustChange, disabled int
		if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role, &owner, &mustChange, &disabled, &u.CreatedAt); err != nil {
			return nil, err
		}
		u.IsOwner = owner == 1
		u.MustChangePassword = mustChange == 1
		u.Disabled = disabled == 1
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Service) CountUsers() (int, error) {
	var count int
	err := s.DB.QueryRow(`SELECT COUNT(1) FROM users WHERE disabled = 0 AND deleted_at = ''`).Scan(&count)
	return count, err
}

func (s *Service) CreateUser(actor model.Actor, username, displayName, password, role string) (model.User, error) {
	username = strings.TrimSpace(username)
	displayName = strings.TrimSpace(displayName)
	if err := ValidateUsername(username); err != nil {
		return model.User{}, err
	}
	if displayName == "" || len([]rune(displayName)) > 40 {
		return model.User{}, apperr.BadRequest("INVALID_DISPLAY_NAME", "显示名称长度应为 1 到 40 个字符")
	}
	if role != model.RoleAdmin && role != model.RoleEditor && role != model.RoleViewer {
		return model.User{}, apperr.BadRequest("INVALID_ROLE", "角色无效")
	}
	if err := ValidatePassword(password); err != nil {
		return model.User{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return model.User{}, err
	}
	id := model.NewID()
	now := model.Now()
	_, err = s.DB.Exec(`INSERT INTO users
		(id, username, display_name, password_hash, role, is_owner, must_change_password, disabled, created_at, updated_at, created_by)
		VALUES (?, ?, ?, ?, ?, 0, 1, 0, ?, ?, ?)`, id, username, displayName, hash, role, now, now, actor.ID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return model.User{}, apperr.Conflict("USERNAME_EXISTS", "用户名已存在")
		}
		return model.User{}, err
	}
	return model.User{ID: id, Username: username, DisplayName: displayName, Role: role,
		MustChangePassword: true, CreatedAt: now}, nil
}

func (s *Service) UpdateUser(actor model.Actor, id, displayName, role string, disabled bool) (model.User, error) {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" || len([]rune(displayName)) > 40 {
		return model.User{}, apperr.BadRequest("INVALID_DISPLAY_NAME", "显示名称长度应为 1 到 40 个字符")
	}
	if role != model.RoleAdmin && role != model.RoleEditor && role != model.RoleViewer {
		return model.User{}, apperr.BadRequest("INVALID_ROLE", "角色无效")
	}
	if actor.ID == id && disabled {
		return model.User{}, apperr.BadRequest("SELF_DISABLE", "不能停用当前登录的账号")
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return model.User{}, err
	}
	defer tx.Rollback()
	var currentRole string
	var currentDisabled, currentOwner int
	if err := tx.QueryRow(`SELECT role, disabled, is_owner FROM users WHERE id = ? AND deleted_at = ''`, id).Scan(&currentRole, &currentDisabled, &currentOwner); err != nil {
		if err == sql.ErrNoRows {
			return model.User{}, apperr.NotFound("账号不存在")
		}
		return model.User{}, err
	}
	if currentOwner == 1 {
		if actor.ID != id {
			return model.User{}, apperr.Forbidden("主机账号只能由本人维护")
		}
		if role != model.RoleAdmin || disabled {
			return model.User{}, apperr.BadRequest("OWNER_LOCKED", "主机账号必须保持管理员角色且不能停用")
		}
	}
	if currentRole == model.RoleAdmin && (role != model.RoleAdmin || disabled) {
		var admins int
		if err := tx.QueryRow(`SELECT COUNT(1) FROM users WHERE role = 'admin' AND disabled = 0`).Scan(&admins); err != nil {
			return model.User{}, err
		}
		if admins <= 1 {
			return model.User{}, apperr.Conflict("LAST_ADMIN", "必须至少保留一个可用的管理员")
		}
	}
	if _, err := tx.Exec(`UPDATE users SET display_name = ?, role = ?, disabled = ?, updated_at = ? WHERE id = ?`,
		displayName, role, boolInt(disabled), model.Now(), id); err != nil {
		return model.User{}, err
	}
	if disabled {
		if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
			return model.User{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return model.User{}, err
	}
	return model.User{ID: id, DisplayName: displayName, Role: role, IsOwner: currentOwner == 1, Disabled: disabled}, nil
}

func (s *Service) ResetPassword(actor model.Actor, id, newPassword string) error {
	if err := ValidatePassword(newPassword); err != nil {
		return err
	}
	var owner int
	if err := s.DB.QueryRow(`SELECT is_owner FROM users WHERE id = ? AND deleted_at = ''`, id).Scan(&owner); err != nil {
		if err == sql.ErrNoRows {
			return apperr.NotFound("账号不存在")
		}
		return err
	}
	if owner == 1 && actor.ID != id {
		return apperr.Forbidden("主机账号的密码只能由本人重置")
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE users SET password_hash = ?, must_change_password = 1, updated_at = ? WHERE id = ? AND deleted_at = ''`,
		hash, model.Now(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return apperr.NotFound("账号不存在")
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) DeleteUser(actor model.Actor, id string) (model.User, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return model.User{}, err
	}
	defer tx.Rollback()
	var target model.User
	var targetOwner int
	if err := tx.QueryRow(`SELECT id, username, display_name, role, is_owner
		FROM users WHERE id = ? AND deleted_at = ''`, id).
		Scan(&target.ID, &target.Username, &target.DisplayName, &target.Role, &targetOwner); err != nil {
		if err == sql.ErrNoRows {
			return model.User{}, apperr.NotFound("账号不存在")
		}
		return model.User{}, err
	}
	if targetOwner == 1 {
		return model.User{}, apperr.Forbidden("不能删除主机账号")
	}
	if actor.ID == id {
		return model.User{}, apperr.BadRequest("SELF_DELETE", "不能删除当前登录的账号")
	}
	var actorOwner int
	if err := tx.QueryRow(`SELECT is_owner FROM users WHERE id = ? AND deleted_at = ''`, actor.ID).Scan(&actorOwner); err != nil {
		if err == sql.ErrNoRows {
			return model.User{}, apperr.Forbidden("只有主机账号可以删除账号")
		}
		return model.User{}, err
	}
	if actorOwner != 1 {
		return model.User{}, apperr.Forbidden("只有主机账号可以删除账号")
	}
	now := model.Now()
	res, err := tx.Exec(`UPDATE users SET deleted_at = ?, disabled = 1,
		username = username || '#deleted-' || id, updated_at = ? WHERE id = ? AND deleted_at = ''`, now, now, id)
	if err != nil {
		return model.User{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return model.User{}, apperr.NotFound("账号不存在")
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
		return model.User{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.User{}, err
	}
	return target, nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

type attempt struct {
	count        int
	windowStart  time.Time
	blockedUntil time.Time
}

type limiter struct {
	mu sync.Mutex
	m  map[string]*attempt
}

func newLimiter() *limiter {
	return &limiter{m: make(map[string]*attempt)}
}

func (l *limiter) Allow(key string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	a, ok := l.m[key]
	if !ok {
		return nil
	}
	if now.Before(a.blockedUntil) {
		remain := int(a.blockedUntil.Sub(now).Minutes()) + 1
		return apperr.New(429, "TOO_MANY_ATTEMPTS", fmt.Sprintf("尝试次数过多，请 %d 分钟后再试", remain))
	}
	if now.Sub(a.windowStart) > 10*time.Minute {
		delete(l.m, key)
	}
	return nil
}

func (l *limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	a, ok := l.m[key]
	if !ok || now.Sub(a.windowStart) > 10*time.Minute {
		a = &attempt{windowStart: now}
		l.m[key] = a
	}
	a.count++
	if a.count >= 5 {
		a.blockedUntil = now.Add(5 * time.Minute)
	}
}

func (l *limiter) Success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.m, key)
}
