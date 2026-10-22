package worker

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/zupit-it/zupit-automations/internal/connectors"
	"github.com/zupit-it/zupit-automations/internal/model"
)

func (w *Worker) comparisons(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			job, err := w.Store.ClaimComparison(ctx, w.Clients.TypeSafeAPIKey != "")
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				w.Log.Error("lettura confronti Jev fallita")
				continue
			}
			w.compareOne(ctx, job)
		}
	}
}

func (w *Worker) compareOne(ctx context.Context, job model.Comparison) {
	attempt, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if job.Status == "evaluating" {
		result, err := w.Clients.Compare(attempt, job.Snapshot)
		save, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if err != nil {
			f := connectors.Classify(err)
			if f.Retryable && job.Attempts < 3 {
				_, _, next := Outcome(job.Attempts, f, time.Now())
				if e := w.Store.FinishComparison(save, job.ID, "pending", f.Message, "", next); e != nil {
					w.Log.Error("salvataggio retry Jev fallito", "comparison_id", job.ID)
				}
				return
			}
			result.B.Error = f.Message
		}
		if err = w.Store.SaveComparisonResult(save, job.ID, result); err != nil {
			w.Log.Error("salvataggio proposta Jev fallito", "comparison_id", job.ID)
		}
		return
	}
	var ref string
	var err error
	if cfg := job.Snapshot.Definition.Comparison; cfg != nil {
		ref, err = w.Clients.SendComparison(attempt, *cfg, connectors.ComparisonReport(job, w.BaseURL))
	} else {
		err = &connectors.Failure{Message: "Confronto senza configurazione"}
	}
	status, message, next := Outcome(job.Attempts, err, time.Now())
	if status == "pending" {
		if connectors.Classify(err).Auth {
			status = "failed"
		} else {
			status = "evaluated"
		}
	}
	// B never changes connection state or the status of A's actions.
	save, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	if err = w.Store.FinishComparison(save, job.ID, status, message, ref, next); err != nil {
		w.Log.Error("salvataggio esito report fallito", "comparison_id", job.ID)
	}
}
