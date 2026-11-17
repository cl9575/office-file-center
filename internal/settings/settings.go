package settings

import (
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"lanfile/internal/apperr"
)

const (
	KeySiteName       = "site_name"
	KeyMaxFileSize    = "max_file_size_bytes"
	KeyFreeReserve    = "free_space_reserve_bytes"
	KeyRecycleDays    = "recycle_retention_days"
	KeyBackupEnabled  = "backup_enabled"
	KeyBackupTime     = "backup_time"
	KeyBackupKeep     = "backup_keep"
	KeyBackupDir      = "backup_dir"
)

var backupTimePattern = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

type Service struct {
	DB *sql.DB
}

type Public struct {
	SiteName            string `json:"site_name"`
	MaxFileSize         int64  `json:"max_file_size"`
	RecycleRetentionDays int   `json:"recycle_retention_days"`
	BackupEnabled       bool   `json:"backup_enabled"`
	BackupTime          string `json:"backup_time"`
	BackupKeep          int    `json:"backup_keep"`
	BackupDir           string `json:"backup_dir"`
}

type Admin struct {
	Public
	FreeSpaceReserve int64 `json:"free_space_reserve"`
}

func (s *Service) Get(key, fallback string) string {
	var v string
	if err := s.DB.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v); err != nil {
		return fallback
	}
	return v
}

func (s *Service) GetInt64(key string, fallback int64) int64 {
	v, err := strconv.ParseInt(s.Get(key, ""), 10, 64)
	if err != nil {
		return fallback
	}
	return v
}

func (s *Service) GetBool(key string, fallback bool) bool {
	return strings.EqualFold(s.Get(key, strconv.FormatBool(fallback)), "true")
}

func (s *Service) SiteName() string { return s.Get(KeySiteName, "办公室文件中心") }

func (s *Service) Public() Public {
	return Public{
		SiteName:             s.SiteName(),
		MaxFileSize:          s.GetInt64(KeyMaxFileSize, 2<<30),
		RecycleRetentionDays: int(s.GetInt64(KeyRecycleDays, 30)),
		BackupEnabled:        s.GetBool(KeyBackupEnabled, true),
		BackupTime:           s.Get(KeyBackupTime, "02:30"),
		BackupKeep:           int(s.GetInt64(KeyBackupKeep, 7)),
		BackupDir:            s.Get(KeyBackupDir, ""),
	}
}

func (s *Service) Admin() Admin {
	return Admin{Public: s.Public(), FreeSpaceReserve: s.GetInt64(KeyFreeReserve, 5<<30)}
}

func (s *Service) Update(values map[string]string) error {
	allowed := map[string]bool{
		KeySiteName: true, KeyMaxFileSize: true, KeyFreeReserve: true,
		KeyRecycleDays: true, KeyBackupEnabled: true, KeyBackupTime: true,
		KeyBackupKeep: true, KeyBackupDir: true,
	}
	for key := range values {
		if !allowed[key] {
			return apperr.BadRequest("INVALID_SETTING", "未知设置项："+key)
		}
	}
	if v, ok := values[KeySiteName]; ok {
		v = strings.TrimSpace(v)
		if v == "" || len([]rune(v)) > 40 {
			return apperr.BadRequest("INVALID_SETTING", "平台名称长度应为 1 到 40 个字符")
		}
		values[KeySiteName] = v
	}
	rules := []struct {
		key      string
		min, max int64
	}{
		{KeyMaxFileSize, 1 << 20, 100 << 30},
		{KeyFreeReserve, 0, 500 << 30},
		{KeyRecycleDays, 1, 3650},
		{KeyBackupKeep, 1, 90},
	}
	for _, rule := range rules {
		if v, ok := values[rule.key]; ok {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < rule.min || n > rule.max {
				return apperr.BadRequest("INVALID_SETTING", fmt.Sprintf("设置 %s 超出允许范围", rule.key))
			}
		}
	}
	if v, ok := values[KeyBackupTime]; ok && !backupTimePattern.MatchString(v) {
		return apperr.BadRequest("INVALID_SETTING", "备份时间格式应为 HH:MM")
	}
	if v, ok := values[KeyBackupEnabled]; ok && v != "true" && v != "false" {
		return apperr.BadRequest("INVALID_SETTING", "backup_enabled 必须为 true 或 false")
	}
	if v, ok := values[KeyBackupDir]; ok {
		values[KeyBackupDir] = strings.TrimSpace(v)
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, value := range values {
		if _, err := tx.Exec(`INSERT INTO settings(key, value) VALUES(?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value); err != nil {
			return err
		}
	}
	return tx.Commit()
}
