package discovery

import (
	"context"
	"encoding/json"
	"time"
)

func (s *SQLStore) CreateAdminJob(ctx context.Context, id, kind, scope string, created int64) error {
	_, err := s.db.ExecContext(ctx, s.rebind("INSERT INTO admin_jobs(id,kind,scope,status,created_at) VALUES(?,?,?,'RUNNING',?)"), id, kind, scope, created)
	return err
}

func (s *SQLStore) FinishAdminJob(ctx context.Context, id, status, result, detail string) error {
	_, err := s.db.ExecContext(ctx, s.rebind("UPDATE admin_jobs SET status=?,result=?,error=?,finished_at=? WHERE id=?"), status, result, detail, time.Now().Unix(), id)
	return err
}

func (s *SQLStore) InterruptRunningWork(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	for _, q := range []string{
		"UPDATE admin_jobs SET status='INTERRUPTED',error='服务已重启，可重新提交',finished_at=? WHERE status='RUNNING'",
		"UPDATE sync_batches SET status='INTERRUPTED',error_message='服务已重启，可重新提交',finished_at=? WHERE status='RUNNING'",
		"UPDATE monitor_roots SET last_status='INTERRUPTED',last_error='服务已重启，可重新提交',updated_at=? WHERE last_status='RUNNING'",
	} {
		if _, err = tx.ExecContext(ctx, s.rebind(q), now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type AdminJob struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	Scope     string          `json:"scope"`
	Status    string          `json:"status"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
	CreatedAt int64           `json:"created_at"`
}

func scanJob(row interface{ Scan(...any) error }) (AdminJob, error) {
	var j AdminJob
	var raw string
	err := row.Scan(&j.ID, &j.Kind, &j.Scope, &j.Status, &raw, &j.Error, &j.CreatedAt)
	if raw != "" {
		j.Result = json.RawMessage(raw)
	}
	return j, err
}

func (s *SQLStore) GetAdminJob(ctx context.Context, id string) (AdminJob, error) {
	return scanJob(s.db.QueryRowContext(ctx, s.rebind("SELECT id,kind,scope,status,result,error,created_at FROM admin_jobs WHERE id=?"), id))
}

func (s *SQLStore) ListAdminJobs(ctx context.Context) ([]AdminJob, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,kind,scope,status,result,error,created_at FROM admin_jobs ORDER BY CASE WHEN status='RUNNING' THEN 0 ELSE 1 END,created_at DESC,id DESC LIMIT 50")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AdminJob{}
	for rows.Next() {
		j, e := scanJob(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
