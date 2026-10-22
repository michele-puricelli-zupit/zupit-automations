package store

import (
	"context"
	"time"

	dbent "github.com/zupit-it/zupit-automations/ent"
	"github.com/zupit-it/zupit-automations/ent/comparison"
	"github.com/zupit-it/zupit-automations/internal/model"
)

func asComparison(row *dbent.Comparison) model.Comparison {
	return model.Comparison{ID: row.ID, RunID: row.RunID, Status: row.Status, Attempts: row.Attempts,
		Result: row.Result, Error: row.Error, Reference: row.Reference, NextAt: row.NextAt}
}

func (s *Store) ComparisonForRun(ctx context.Context, runID int) (*model.Comparison, error) {
	row, err := s.Client.Comparison.Query().Where(comparison.RunID(runID)).Only(ctx)
	if dbent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result := asComparison(row)
	return &result, nil
}

func (s *Store) ClaimComparison(ctx context.Context, canEvaluate bool) (model.Comparison, error) {
	var id int
	err := s.DB.QueryRowContext(ctx, `WITH candidate AS (
 SELECT c.id FROM comparisons c JOIN runs r ON r.id=c.run_id JOIN flows f ON f.id=r.flow_id
 WHERE c.next_at<=now() AND (c.status='evaluated' OR ($1 AND c.status='pending'))
 AND f.enabled=TRUE AND f.definition->'comparison' IS NOT NULL AND f.definition->'comparison'<>'null'::jsonb
 ORDER BY c.next_at,c.id FOR UPDATE OF c SKIP LOCKED LIMIT 1)
 UPDATE comparisons SET status=CASE WHEN status='pending' THEN 'evaluating' ELSE 'sending' END,
 attempts=attempts+1 WHERE id=(SELECT id FROM candidate) RETURNING id`, canEvaluate).Scan(&id)
	if err != nil {
		return model.Comparison{}, err
	}
	row, err := s.Client.Comparison.Get(ctx, id)
	if err != nil {
		return model.Comparison{}, err
	}
	run, err := s.Client.Run.Get(ctx, row.RunID)
	if err != nil {
		return model.Comparison{}, err
	}
	result := asComparison(row)
	result.Snapshot = run.Snapshot
	return result, nil
}

func (s *Store) SaveComparisonResult(ctx context.Context, id int, result model.ComparisonResult) error {
	return s.Client.Comparison.UpdateOneID(id).Where(comparison.StatusEQ("evaluating")).
		SetResult(result).SetStatus("evaluated").SetAttempts(0).SetError("").SetNextAt(time.Now()).Exec(ctx)
}

func (s *Store) FinishComparison(ctx context.Context, id int, status, message, reference string, next time.Time) error {
	return s.Client.Comparison.UpdateOneID(id).SetStatus(status).SetError(message).SetReference(reference).SetNextAt(next).Exec(ctx)
}
