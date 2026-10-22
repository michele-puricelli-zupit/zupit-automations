package store

import (
	"context"
	"database/sql"
	"errors"
	"github.com/zupit-it/zupit-automations/ent/session"
	"github.com/zupit-it/zupit-automations/ent/user"
	"github.com/zupit-it/zupit-automations/internal/model"
	"strings"
	"time"
)

type Attempt struct {
	StateHash, Nonce, Verifier, Purpose, BindingHash string
	ConnectionID, UserID                             int
	ExpiresAt                                        time.Time
}

func (s *Store) SaveAttempt(ctx context.Context, a Attempt) error {
	return s.Client.AuthAttempt.Create().SetStateHash(a.StateHash).SetNonce(a.Nonce).SetVerifier(a.Verifier).SetPurpose(a.Purpose).SetConnectionID(a.ConnectionID).SetUserID(a.UserID).SetBindingHash(a.BindingHash).SetExpiresAt(a.ExpiresAt).Exec(ctx)
}
func (s *Store) ConsumeAttempt(ctx context.Context, state, binding, purpose string) (Attempt, error) {
	var a Attempt
	err := s.DB.QueryRowContext(ctx, `DELETE FROM auth_attempts WHERE state_hash=$1 AND binding_hash=$2 AND purpose=$3 AND expires_at>now() RETURNING nonce,verifier,purpose,connection_id,user_id,binding_hash,expires_at`, state, binding, purpose).Scan(&a.Nonce, &a.Verifier, &a.Purpose, &a.ConnectionID, &a.UserID, &a.BindingHash, &a.ExpiresAt)
	return a, err
}
func (s *Store) ResolveUser(ctx context.Context, subject, email, name string, admins map[string]bool) (model.User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	tx, err := s.Client.Tx(ctx)
	if err != nil {
		return model.User{}, err
	}
	defer tx.Rollback()
	row, err := tx.User.Query().Where(user.Subject(subject)).Only(ctx)
	if err != nil {
		if !isNotFound(err) {
			return model.User{}, err
		}
		row, err = tx.User.Create().SetSubject(subject).SetEmail(email).SetName(name).SetAdmin(admins[email]).Save(ctx)
	} else {
		row, err = tx.User.UpdateOne(row).SetEmail(email).SetName(name).SetAdmin(admins[email]).Save(ctx)
	}
	if err != nil {
		return model.User{}, err
	}
	if !row.Active {
		return model.User{}, errors.New("utente disabilitato")
	}
	if err = tx.Commit(); err != nil {
		return model.User{}, err
	}
	return model.User{ID: row.ID, Email: row.Email, Name: row.Name, Admin: row.Admin, Active: row.Active}, nil
}
func (s *Store) CreateSession(ctx context.Context, id int, hash string, now time.Time) error {
	return s.Client.Session.Create().SetUserID(id).SetTokenHash(hash).SetLastSeenAt(now).SetExpiresAt(now.Add(8 * time.Hour)).Exec(ctx)
}
func (s *Store) Session(ctx context.Context, hash string, admins map[string]bool) (model.User, error) {
	now := time.Now()
	row, err := s.Client.Session.Query().Where(session.TokenHash(hash), session.ExpiresAtGT(now), session.LastSeenAtGT(now.Add(-time.Hour))).Only(ctx)
	if err != nil {
		return model.User{}, err
	}
	u, err := s.Client.User.Get(ctx, row.UserID)
	if err != nil {
		return model.User{}, err
	}
	if !u.Active {
		return model.User{}, sql.ErrNoRows
	}
	if now.Sub(row.LastSeenAt) > 5*time.Minute {
		if err = s.Client.Session.UpdateOneID(row.ID).SetLastSeenAt(now).Exec(ctx); err != nil {
			return model.User{}, err
		}
	}
	return model.User{ID: u.ID, Email: u.Email, Name: u.Name, Admin: admins[u.Email], Active: u.Active}, nil
}
func (s *Store) DeleteSession(ctx context.Context, hash string) error {
	_, err := s.Client.Session.Delete().Where(session.TokenHash(hash)).Exec(ctx)
	return err
}
func (s *Store) CleanupAuth(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at<now() OR last_seen_at<now()-interval '1 hour'`)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `DELETE FROM auth_attempts WHERE expires_at<now()`)
	return err
}
