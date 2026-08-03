package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spsp4755/load-observatory/internal/core"
)

type PostgresStore struct {
	memory *MemoryStore
	pool   *pgxpool.Pool
}

func NewPostgresStore(ctx context.Context, url string) (*PostgresStore, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if _, err = pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS load_observatory_state (id boolean PRIMARY KEY DEFAULT true, state jsonb NOT NULL, updated_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		pool.Close()
		return nil, err
	}
	store := &PostgresStore{memory: NewMemoryStore(), pool: pool}
	var state []byte
	err = pool.QueryRow(ctx, `SELECT state FROM load_observatory_state WHERE id=true`).Scan(&state)
	if err == nil {
		if err = store.memory.Restore(state); err != nil {
			pool.Close()
			return nil, err
		}
	}
	return store, nil
}
func (s *PostgresStore) persist() {
	data, err := s.memory.Snapshot()
	if err != nil {
		return
	}
	_, _ = s.pool.Exec(context.Background(), `INSERT INTO load_observatory_state (id,state,updated_at) VALUES (true,$1,now()) ON CONFLICT (id) DO UPDATE SET state=excluded.state,updated_at=now()`, data)
}
func (s *PostgresStore) Close() { s.pool.Close() }
func (s *PostgresStore) CreateTarget(v core.Target) core.Target {
	r := s.memory.CreateTarget(v)
	s.persist()
	return r
}
func (s *PostgresStore) GetTarget(v string) (core.Target, bool) { return s.memory.GetTarget(v) }
func (s *PostgresStore) ListTargets() []core.Target             { return s.memory.ListTargets() }
func (s *PostgresStore) DeleteTarget(v string) bool {
	ok := s.memory.DeleteTarget(v)
	if ok {
		s.persist()
	}
	return ok
}
func (s *PostgresStore) CreateRun(v core.RunConfig) core.Run {
	r := s.memory.CreateRun(v)
	s.persist()
	return r
}
func (s *PostgresStore) GetRun(v string) (core.Run, bool) { return s.memory.GetRun(v) }
func (s *PostgresStore) ListRuns() []core.Run             { return s.memory.ListRuns() }
func (s *PostgresStore) ClaimRun() (core.Assignment, bool) {
	r, ok := s.memory.ClaimRun()
	if ok {
		s.persist()
	}
	return r, ok
}
func (s *PostgresStore) CompleteRun(id string, r core.RunResult) (core.Run, bool) {
	v, ok := s.memory.CompleteRun(id, r)
	if ok {
		s.persist()
	}
	return v, ok
}
func (s *PostgresStore) CompleteShard(id string, r core.RunResult) (core.Run, bool) {
	v, ok := s.memory.CompleteShard(id, r)
	if ok {
		s.persist()
	}
	return v, ok
}
func (s *PostgresStore) AddMonitoring(id string, sample core.MonitoringSample) {
	s.memory.AddMonitoring(id, sample)
	s.persist()
}
func (s *PostgresStore) TouchAgent()              { s.memory.TouchAgent() }
func (s *PostgresStore) Health() (int, int, bool) { return s.memory.Health() }
func (s *PostgresStore) CreateSearch(v core.AutoSearchConfig) core.AutoSearch {
	r := s.memory.CreateSearch(v)
	s.persist()
	return r
}
func (s *PostgresStore) GetSearch(v string) (core.AutoSearch, bool) { return s.memory.GetSearch(v) }
func (s *PostgresStore) ListSearches() []core.AutoSearch            { return s.memory.ListSearches() }
func (s *PostgresStore) CancelSearch(v string) (core.AutoSearch, bool) {
	r, ok := s.memory.CancelSearch(v)
	if ok {
		s.persist()
	}
	return r, ok
}
func (s *PostgresStore) AdvanceSearch(v string) { s.memory.AdvanceSearch(v); s.persist() }

var _ Store = (*PostgresStore)(nil)
var _ = fmt.Sprintf
var _ = time.Second
