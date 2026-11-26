package model

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleEditor   = "editor"
	RoleViewer   = "viewer"

	KindFile   = "file"
	KindFolder = "folder"

	SpaceMain   = "main"
	SpaceSecure = "secure"

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
func (u User) CanEdit() bool {
	return u.Role == RoleAdmin || u.Role == RoleOperator || u.Role == RoleEditor
}
func (u User) CanAccessAdmin() bool { return u.Role == RoleAdmin || u.Role == RoleOperator }
func (u User) CanUploadSecure() bool {
	return u.Role == RoleAdmin || u.Role == RoleOperator
}

func (a Actor) IsAdmin() bool { return a.Role == RoleAdmin }
func (a Actor) CanEdit() bool {
	return a.Role == RoleAdmin || a.Role == RoleOperator || a.Role == RoleEditor
}
func (a Actor) CanAccessAdmin() bool { return a.Role == RoleAdmin || a.Role == RoleOperator }
func (a Actor) CanUploadSecure() bool {
	return a.Role == RoleAdmin || a.Role == RoleOperator
}
func (a Actor) RoleRank() int { return RoleRank(a.Role) }
func (a Actor) EffectiveRank() int {
	if a.IsOwner {
		return 4
	}
	return RoleRank(a.Role)
}

func RoleRank(role string) int {
	switch role {
	case RoleAdmin:
		return 3
	case RoleOperator:
		return 2
	case RoleEditor:
		return 1
	case RoleViewer:
		return 0
	default:
		return -1
	}
}

func ValidRole(role string) bool { return RoleRank(role) >= 0 }

func (a Actor) CanAssignRole(role string) bool {
	if !ValidRole(role) {
		return false
	}
	if a.IsOwner {
		return true
	}
	return a.Role == RoleAdmin && RoleRank(role) <= RoleRank(RoleOperator)
}

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
	case RoleOperator:
		return "操作员"
	case RoleEditor:
		return "编辑者"
	case RoleViewer:
		return "只读者"
	default:
		return role
	}
}
