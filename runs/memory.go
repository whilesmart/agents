package runs

import (
	"context"
	"encoding/json"
	"sync"
)

type Memory struct {
	mu   sync.Mutex
	rows map[string]Run
}

func NewMemory() *Memory { return &Memory{rows: map[string]Run{}} }
func clone(run Run) (Run, error) {
	raw, err := json.Marshal(run)
	if err != nil {
		return Run{}, err
	}
	var value Run
	err = json.Unmarshal(raw, &value)
	return value, err
}
func (m *Memory) Create(ctx context.Context, run Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := run.Validate(); err != nil {
		return err
	}
	if _, ok := m.rows[run.ID]; ok {
		return ErrConflict
	}
	value, err := clone(run)
	if err != nil {
		return err
	}
	m.rows[run.ID] = value
	return nil
}
func (m *Memory) Get(ctx context.Context, id string) (Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Run{}, err
	}
	row, ok := m.rows[id]
	if !ok {
		return Run{}, ErrNotFound
	}
	return clone(row)
}
func (m *Memory) Replace(ctx context.Context, run Run, expected uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := run.Validate(); err != nil {
		return err
	}
	before, ok := m.rows[run.ID]
	if !ok {
		return ErrNotFound
	}
	if before.Revision != expected || run.Revision != expected+1 || before.Scope != run.Scope || before.Requester != run.Requester || !before.CreatedAt.Equal(run.CreatedAt) {
		return ErrConflict
	}
	value, err := clone(run)
	if err != nil {
		return err
	}
	m.rows[run.ID] = value
	return nil
}
