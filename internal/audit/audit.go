// Package audit keeps an append-only forensic trail of every
// authentication/authorization DML: who changed what, to whom, from where.
// Events deliberately survive deletion of actors and targets (no FKs), and
// tenant_id scopes what tenant admins may read back.
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

type Event struct {
	ID         int64          `json:"id"`
	TS         int64          `json:"ts"` // unix seconds
	Time       time.Time      `json:"time"`
	ActorID    *int64         `json:"actor_id,omitempty"`    // nil for unauthenticated attempts
	Actor      string         `json:"actor,omitempty"`       // "tenant/username" snapshot
	TenantID   int64          `json:"tenant_id"`             // subject tenant (0 = platform/unresolved)
	Action     string         `json:"action"`                // perm_set, user_create, login_failed, ...
	TargetType string         `json:"target_type,omitempty"` // user | group | grant | factor | tenant | session
	TargetID   int64          `json:"target_id,omitempty"`
	TargetName string         `json:"target_name,omitempty"`
	Path       string         `json:"path,omitempty"` // for grant actions
	Details    map[string]any `json:"details,omitempty"`
	IP         string         `json:"ip,omitempty"`
}

type Store struct{ DB *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{DB: db} }

// Record appends one event. details is serialized as JSON; secrets (api
// keys, passwords) must never be placed in it by callers.
func (s *Store) Record(ctx context.Context, ev Event) error {
	if ev.TS == 0 {
		ev.TS = time.Now().Unix()
	}
	details := ""
	if ev.Details != nil {
		b, err := json.Marshal(ev.Details)
		if err != nil {
			return err
		}
		details = string(b)
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO events (ts, actor_id, actor, tenant_id, action, target_type, target_id, target_name, path, details, ip)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		ev.TS, ev.ActorID, ev.Actor, ev.TenantID, ev.Action,
		ev.TargetType, ev.TargetID, ev.TargetName, ev.Path, details, ev.IP)
	return err
}

// Filter narrows List; zero values mean "any". Structured filters
// (Action/TargetType/Target/Since) are ANDed with the free-text Q search.
type Filter struct {
	Limit      int
	Offset     int
	Action     string
	TargetType string
	Target     string // exact target_name match
	Since      int64  // unix seconds lower bound
	Q          string // case-insensitive substring
	Col        string // restrict Q: actor | action | target | path; "" = all
	Sort       string // ts | actor | action | target | path (default ts)
	Dir        string // asc | desc (default desc: newest first)
}

// List returns one page of events plus the total matching count.
// Default order is newest-first; tenantID 0 means every tenant (super
// admin view); otherwise results are restricted to that tenant's scope.
func (s *Store) List(ctx context.Context, tenantID int64, f Filter) ([]Event, int, error) {
	if f.Limit <= 0 {
		f.Limit = 100
	}
	if f.Limit > 1000 {
		f.Limit = 1000
	}
	var conds []string
	var args []any
	if tenantID != 0 {
		conds = append(conds, "tenant_id = ?")
		args = append(args, tenantID)
	}
	if f.Action != "" {
		conds = append(conds, "action = ?")
		args = append(args, f.Action)
	}
	if f.TargetType != "" {
		conds = append(conds, "target_type = ?")
		args = append(args, f.TargetType)
	}
	if f.Target != "" {
		conds = append(conds, "target_name = ?")
		args = append(args, f.Target)
	}
	if f.Since > 0 {
		conds = append(conds, "ts >= ?")
		args = append(args, f.Since)
	}
	if f.Q != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(f.Q) + "%"
		switch f.Col {
		case "actor":
			conds = append(conds, `actor LIKE ? ESCAPE '\'`)
			args = append(args, like)
		case "action":
			conds = append(conds, `action LIKE ? ESCAPE '\'`)
			args = append(args, like)
		case "target":
			conds = append(conds, `target_name LIKE ? ESCAPE '\'`)
			args = append(args, like)
		case "path":
			conds = append(conds, `path LIKE ? ESCAPE '\'`)
			args = append(args, like)
		default: // "" = every searchable column
			conds = append(conds, `(actor LIKE ? ESCAPE '\' OR action LIKE ? ESCAPE '\' OR target_name LIKE ? ESCAPE '\' OR path LIKE ? ESCAPE '\')`)
			args = append(args, like, like, like, like)
		}
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	var total int
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM events"+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := map[string]string{"ts": "ts", "actor": "actor", "action": "action", "target": "target_name", "path": "path"}[f.Sort]
	if order == "" {
		order = "ts"
	}
	dir := " asc"
	if f.Dir == "desc" {
		dir = " desc"
	}
	q := "SELECT id, ts, actor_id, actor, tenant_id, action, target_type, target_id, target_name, path, details, ip FROM events" +
		where + " ORDER BY " + order + dir + ", id DESC LIMIT ? OFFSET ?"
	args = append(args, f.Limit, f.Offset)

	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var (
			ev      Event
			actorID sql.NullInt64
			details string
		)
		if err := rows.Scan(&ev.ID, &ev.TS, &actorID, &ev.Actor, &ev.TenantID,
			&ev.Action, &ev.TargetType, &ev.TargetID, &ev.TargetName, &ev.Path, &details, &ev.IP); err != nil {
			return nil, 0, err
		}
		if actorID.Valid {
			v := actorID.Int64
			ev.ActorID = &v
		}
		if details != "" {
			_ = json.Unmarshal([]byte(details), &ev.Details)
		}
		ev.Time = time.Unix(ev.TS, 0).UTC()
		out = append(out, ev)
	}
	return out, total, rows.Err()
}
