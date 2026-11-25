// Package store reads a teacher's working set from the shared PostgreSQL database and records sync events.
// The schema belongs to eduos-api (Prisma); this package never changes it.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ db *pgxpool.Pool }

func New(ctx context.Context, url string) (*Store, error) {
	db, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close()                         { s.db.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.db.Ping(ctx) }

// Event is a change sent from a phone, as recorded once it has been applied or refused.
type Event struct {
	ID         string
	UserID     string
	DeviceID   string
	Type       string
	OccurredAt time.Time
	Status     string // APPLIED or REJECTED
	HTTPStatus int
	Result     json.RawMessage
}

var ErrNotFound = errors.New("not found")

func (s *Store) FindEvent(ctx context.Context, id string) (*Event, error) {
	var e Event
	err := s.db.QueryRow(ctx, `SELECT id, "userId", "deviceId", type, "occurredAt", status::text, "httpStatus", result FROM "SyncEvent" WHERE id = $1`, id).
		Scan(&e.ID, &e.UserID, &e.DeviceID, &e.Type, &e.OccurredAt, &e.Status, &e.HTTPStatus, &e.Result)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &e, err
}

func (s *Store) SaveEvent(ctx context.Context, e Event) error {
	_, err := s.db.Exec(ctx, `INSERT INTO "SyncEvent" (id, "userId", "deviceId", type, "occurredAt", status, "httpStatus", result)
		VALUES ($1, $2, $3, $4, $5, $6::"SyncStatus", $7, $8) ON CONFLICT (id) DO NOTHING`,
		e.ID, e.UserID, e.DeviceID, e.Type, e.OccurredAt, e.Status, e.HTTPStatus, e.Result)
	return err
}

// Lock serialises work on one event id across service instances, so two uploads of the same change at the same
// moment cannot both apply it. Call the returned function to release the lock.
func (s *Store) Lock(ctx context.Context, id string) (func(), error) {
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, id); err != nil {
		conn.Release()
		return nil, err
	}
	return func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, id)
		conn.Release()
	}, nil
}

// SheetID finds the score sheet for a class subject in the current term, if one has been started.
func (s *Store) SheetID(ctx context.Context, classSubjectID string) (string, error) {
	var id string
	err := s.db.QueryRow(ctx, `SELECT sh.id FROM "ScoreSheet" sh JOIN "Term" t ON t.id = sh."termId" WHERE sh."classSubjectId" = $1 AND t."isCurrent"`, classSubjectID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}
