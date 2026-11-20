package model

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

const (
	RoleAdmin  = "admin"
	RoleEditor = "editor"
	RoleViewer = "viewer"

	KindFile   = "file"
	KindFolder = "folder"

	TimeFormat = "2006-01-02T15:04:05.000Z"
)

type User struct {
	ID                 string `json:"id"`
	Username           string `json:"username"`
	DisplayName        string `json:"display_name"`
	Role               string `json:"role"`
	IsOwner            bool   `json:"is_owner"`
	MustChangePassword bool   `json:"must_change_password"`
	// 仅登录响应携带，用于提示一次修改初始密码，不持久化。
	SuggestPasswordChange bool   `json:"suggest_password_change,omitempty"`
	Disabled              bool   `json:"disabled"`
	CreatedAt             string `json:"created_at"`
}

type Actor struct {
	ID          string
	DisplayName string
	Role        string
	IsOwner     bool
}

func (u User) Actor() Actor {
	return Actor{ID: u.ID, DisplayName: u.DisplayName, Role: u.Role, IsOwner: u.IsOwner}
}

func (u User) IsAdmin() bool { return u.Role == RoleAdmin }
func (u User) CanEdit() bool { return u.Role == RoleAdmin || u.Role == RoleEditor }

func (a Actor) IsAdmin() bool { return a.Role == RoleAdmin }
func (a Actor) CanEdit() bool { return a.Role == RoleAdmin || a.Role == RoleEditor }

func Now() string {
	return time.Now().UTC().Format(TimeFormat)
}

func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func RoleName(role string) string {
	switch role {
	case RoleAdmin:
		return "管理员"
	case RoleEditor:
		return "编辑者"
	case RoleViewer:
		return "只读者"
	default:
		return role
	}
}
