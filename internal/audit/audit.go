package audit

import (
	"database/sql"
	"fmt"
	"strings"

	"lanfile/internal/model"
)

const (
	ResultSuccess = "success"
	ResultFailure = "failure"
	ResultPartial = "partial"
)

type Event struct {
	ActorID    string
	ActorName  string
	Action     string
	TargetType string
	TargetID   string
	TargetName string
	TargetPath string
	Detail     string
	Result     string
	IP         string
	At         string
}

type Row struct {
	Seq        int64  `json:"seq"`
	ID         string `json:"id"`
	ActorID    string `json:"actor_id"`
	ActorName  string `json:"actor_name"`
	Action     string `json:"action"`
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	TargetName string `json:"target_name"`
	TargetPath string `json:"target_path"`
	Detail     string `json:"detail"`
	Result     string `json:"result"`
	IP         string `json:"ip"`
	CreatedAt  string `json:"created_at"`
}

type Filter struct {
	Actor   string
	Action  string
	Result  string
	Query   string
	From    string
	To      string
	Limit   int
	Offset  int
}

type Service struct {
	DB *sql.DB
}

func normalize(ev *Event) {
	if ev.At == "" {
		ev.At = model.Now()
	}
	if ev.Result == "" {
		ev.Result = ResultSuccess
	}
	if ev.ActorName == "" {
		ev.ActorName = "系统"
	}
}

func (s *Service) Log(ev Event) error {
	normalize(&ev)
	_, err := s.DB.Exec(`INSERT INTO audit
		(id, actor_id, actor_name, action, target_type, target_id, target_name, target_path, detail, result, ip, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		model.NewID(), ev.ActorID, ev.ActorName, ev.Action, ev.TargetType, ev.TargetID,
		ev.TargetName, ev.TargetPath, ev.Detail, ev.Result, ev.IP, ev.At)
	return err
}

func (s *Service) LogTx(tx *sql.Tx, ev Event) error {
	normalize(&ev)
	_, err := tx.Exec(`INSERT INTO audit
		(id, actor_id, actor_name, action, target_type, target_id, target_name, target_path, detail, result, ip, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		model.NewID(), ev.ActorID, ev.ActorName, ev.Action, ev.TargetType, ev.TargetID,
		ev.TargetName, ev.TargetPath, ev.Detail, ev.Result, ev.IP, ev.At)
	return err
}

func (f Filter) where() (string, []any) {
	var parts []string
	var args []any
	if f.Actor != "" {
		parts = append(parts, "actor_name LIKE ? ESCAPE '\\'")
		args = append(args, "%"+escapeLike(f.Actor)+"%")
	}
	if f.Action != "" {
		parts = append(parts, "action = ?")
		args = append(args, f.Action)
	}
	if f.Result != "" {
		parts = append(parts, "result = ?")
		args = append(args, f.Result)
	}
	if f.Query != "" {
		parts = append(parts, "(target_name LIKE ? ESCAPE '\\' OR target_path LIKE ? ESCAPE '\\' OR detail LIKE ? ESCAPE '\\')")
		q := "%" + escapeLike(f.Query) + "%"
		args = append(args, q, q, q)
	}
	if f.From != "" {
		parts = append(parts, "created_at >= ?")
		args = append(args, f.From)
	}
	if f.To != "" {
		parts = append(parts, "created_at <= ?")
		args = append(args, f.To)
	}
	if len(parts) == 0 {
		return "", args
	}
	return "WHERE " + strings.Join(parts, " AND "), args
}

func escapeLike(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, "%", `\%`)
	v = strings.ReplaceAll(v, "_", `\_`)
	return v
}

func (s *Service) List(f Filter) ([]Row, int, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	where, args := f.where()
	var total int
	if err := s.DB.QueryRow(`SELECT COUNT(1) FROM audit `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	query := fmt.Sprintf(`SELECT seq, id, actor_id, actor_name, action, target_type, target_id,
		target_name, target_path, detail, result, ip, created_at FROM audit %s
		ORDER BY seq DESC LIMIT ? OFFSET ?`, where)
	rows, err := s.DB.Query(query, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]Row, 0, f.Limit)
	for rows.Next() {
		var row Row
		if err := rows.Scan(&row.Seq, &row.ID, &row.ActorID, &row.ActorName, &row.Action,
			&row.TargetType, &row.TargetID, &row.TargetName, &row.TargetPath, &row.Detail,
			&row.Result, &row.IP, &row.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, row)
	}
	return out, total, rows.Err()
}

func (s *Service) All(f Filter, max int) ([]Row, error) {
	if max <= 0 || max > 50000 {
		max = 10000
	}
	where, args := f.where()
	query := fmt.Sprintf(`SELECT seq, id, actor_id, actor_name, action, target_type, target_id,
		target_name, target_path, detail, result, ip, created_at FROM audit %s
		ORDER BY seq DESC LIMIT ?`, where)
	rows, err := s.DB.Query(query, append(args, max)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Row{}
	for rows.Next() {
		var row Row
		if err := rows.Scan(&row.Seq, &row.ID, &row.ActorID, &row.ActorName, &row.Action,
			&row.TargetType, &row.TargetID, &row.TargetName, &row.TargetPath, &row.Detail,
			&row.Result, &row.IP, &row.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Service) ForTarget(targetID string, limit int) ([]Row, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.DB.Query(`SELECT seq, id, actor_id, actor_name, action, target_type, target_id,
		target_name, target_path, detail, result, ip, created_at FROM audit
		WHERE target_id = ? ORDER BY seq DESC LIMIT ?`, targetID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Row{}
	for rows.Next() {
		var row Row
		if err := rows.Scan(&row.Seq, &row.ID, &row.ActorID, &row.ActorName, &row.Action,
			&row.TargetType, &row.TargetID, &row.TargetName, &row.TargetPath, &row.Detail,
			&row.Result, &row.IP, &row.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func CSVSafe(value string) string {
	if value == "" {
		return value
	}
	switch value[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + value
	}
	return value
}
