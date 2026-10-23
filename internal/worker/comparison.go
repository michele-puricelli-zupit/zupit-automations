package worker

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/zupit-it/zupit-automations/internal/connectors"
	"github.com/zupit-it/zupit-automations/internal/logging"
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
				w.Log.Error("Lettura coda confronti Jev fallita", "event", "comparison.claim_failed", logging.Error(err))
				continue
			}
			w.compareOne(ctx, job)
		}
	}
}

func (w *Worker) compareOne(ctx context.Context, job model.Comparison) {
	log := w.Log.With("comparison_id", job.ID, "run_id", job.RunID, "attempt", job.Attempts, "phase", job.Status)
	attempt, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if job.Status == "evaluating" {
		log.Info("Analisi Jev avviata", "event", "comparison.started")
		result, err := w.Clients.Compare(attempt, job.Snapshot)
		save, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if err != nil {
			f := connectors.Classify(err)
			if f.Retryable && job.Attempts < 3 {
				_, _, next := Outcome(job.Attempts, f, time.Now())
				log.Warn("Analisi Jev non disponibile: retry pianificato", "event", "comparison.retry", "next_at", next, logging.Error(err))
				if e := w.Store.FinishComparison(save, job.ID, "pending", f.Message, "", next); e != nil {
					log.Error("Salvataggio retry Jev fallito", "event", "comparison.persist_failed", logging.Error(e))
				}
				return
			}
			result.B.Error = f.Message
			log.Error("Analisi Jev fallita: il flusso A resta indipendente", "event", "comparison.analysis_failed", logging.Error(err))
		}
		if err = w.Store.SaveComparisonResult(save, job.ID, result); err != nil {
			log.Error("Salvataggio proposta Jev fallito", "event", "comparison.persist_failed", logging.Error(err))
		} else {
			log.Info("Esito analisi Jev salvato", "event", "comparison.evaluated", "model", result.Model, "duration_ms", result.DurationMS, "input_tokens", result.InputTokens, "output_tokens", result.OutputTokens, "success", result.B.Error == "")
		}
		return
	}
	log.Info("Invio report Slack avviato: riuso dell’analisi salvata", "event", "comparison.report_started")
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
	if e := w.Store.FinishComparison(save, job.ID, status, message, ref, next); e != nil {
		log.Error("Salvataggio esito report fallito", "event", "comparison.persist_failed", logging.Error(e))
	} else {
		level := slog.LevelInfo
		if err != nil {
			level = slog.LevelWarn
		}
		attrs := []any{"event", "comparison.report_completed", "status", status, logging.Error(err)}
		if status == "uncertain" {
			attrs = append(attrs, "hint", "Verificare il DM prima di ripetere l’invio")
		} else if status == "evaluated" {
			attrs = append(attrs, "next_at", next)
		}
		log.Log(save, level, "Invio report confronto concluso", attrs...)
	}
}
