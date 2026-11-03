package runs

import (
	"context"
	"errors"
	"time"

	"github.com/whilesmart/agents"
)

var ErrConflict = errors.New("run revision conflict")
var ErrNotFound = errors.New("run not found")
var ErrLease = errors.New("run attempt is no longer current")

type UsageRecord struct {
	Step     int           `json:"step"`
	Reserved int64         `json:"reserved"`
	Usage    *agents.Usage `json:"usage,omitempty"`
	Recorded bool          `json:"recorded"`
}

type Attempt struct {
	Calls      []UsageRecord  `json:"calls,omitempty"`
	Result     *agents.Result `json:"result,omitempty"`
	ID         string         `json:"id"`
	Number     uint64         `json:"number"`
	Status     string         `json:"status"`
	StartedAt  time.Time      `json:"started_at"`
	FinishedAt *time.Time     `json:"finished_at,omitempty"`
	Error      string         `json:"error,omitempty"`
}
type Run struct {
	ID         string           `json:"id"`
	Scope      string           `json:"scope"`
	Requester  string           `json:"requester"`
	Revision   uint64           `json:"revision"`
	Status     string           `json:"status"`
	Messages   []agents.Message `json:"messages"`
	Attempts   []Attempt        `json:"attempts"`
	LeaseUntil *time.Time       `json:"lease_until,omitempty"`
	Result     *agents.Result   `json:"result,omitempty"`
	Error      string           `json:"error,omitempty"`
	CreatedAt  time.Time        `json:"created_at"`
	UpdatedAt  time.Time        `json:"updated_at"`
}

// Replace must atomically compare revisions and preserve scope, requester and creation time.
type Repository interface {
	Create(context.Context, Run) error
	Get(context.Context, string) (Run, error)
	Replace(context.Context, Run, uint64) error
}
type Access interface {
	Authorize(context.Context, Run, string) error
}
type AccessFunc func(context.Context, Run, string) error

func (f AccessFunc) Authorize(ctx context.Context, r Run, action string) error {
	return f(ctx, r, action)
}

type HostFactory interface {
	Host(context.Context, Run, Attempt) (agents.Host, error)
}
type HostFunc func(context.Context, Run, Attempt) (agents.Host, error)

func (f HostFunc) Host(ctx context.Context, r Run, a Attempt) (agents.Host, error) {
	return f(ctx, r, a)
}

type Service struct {
	Repository Repository
	Access     Access
	Hosts      HostFactory
	Model      agents.Model
	Limits     agents.Limits
	Lease      time.Duration
	Now        func() time.Time
	NewID      func() string
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func (s Service) valid() bool {
	return s.Repository != nil && s.Access != nil && s.NewID != nil && s.Lease > 0
}
func (s Service) Create(ctx context.Context, scope, requester string, messages []agents.Message) (Run, error) {
	if !s.valid() || scope == "" || requester == "" {
		return Run{}, errors.New("invalid run configuration")
	}
	now := s.now()
	run := Run{ID: s.NewID(), Scope: scope, Requester: requester, Revision: 1, Status: "queued", Messages: append([]agents.Message{}, messages...), Attempts: []Attempt{}, CreatedAt: now, UpdatedAt: now}
	if run.ID == "" {
		return Run{}, errors.New("run ID is required")
	}
	if err := run.Validate(); err != nil {
		return Run{}, err
	}
	if err := s.Access.Authorize(ctx, run, "create"); err != nil {
		return Run{}, err
	}
	if err := s.Repository.Create(ctx, run); err != nil {
		return Run{}, err
	}
	return run, nil
}
func (s Service) Get(ctx context.Context, id string) (Run, error) {
	if !s.valid() {
		return Run{}, errors.New("invalid run configuration")
	}
	run, err := s.load(ctx, id)
	if err != nil {
		return Run{}, err
	}
	if err := s.Access.Authorize(ctx, run, "read"); err != nil {
		return Run{}, err
	}
	return run, nil
}
func (s Service) Claim(ctx context.Context, id string) (Run, Attempt, error) {
	if !s.valid() {
		return Run{}, Attempt{}, errors.New("invalid run configuration")
	}
	run, err := s.load(ctx, id)
	if err != nil {
		return Run{}, Attempt{}, err
	}
	if err := s.Access.Authorize(ctx, run, "execute"); err != nil {
		return Run{}, Attempt{}, err
	}
	now := s.now()
	if run.Status == "running" && run.LeaseUntil != nil && !run.LeaseUntil.After(now) {
		last := &run.Attempts[len(run.Attempts)-1]
		last.Status = "abandoned"
		last.FinishedAt = &now
	} else if run.Status != "queued" {
		return Run{}, Attempt{}, ErrConflict
	}
	expected := run.Revision
	attempt := Attempt{ID: s.NewID(), Number: uint64(len(run.Attempts) + 1), Status: "running", StartedAt: now}
	if attempt.ID == "" {
		return Run{}, Attempt{}, errors.New("attempt ID is required")
	}
	expiry := now.Add(s.Lease)
	run.LeaseUntil = &expiry
	run.Attempts = append(run.Attempts, attempt)
	run.Status = "running"
	run.Revision++
	run.UpdatedAt = now
	if err := s.Repository.Replace(ctx, run, expected); err != nil {
		return Run{}, Attempt{}, err
	}
	return run, attempt, nil
}
func (s Service) Checkpoint(ctx context.Context, id, attemptID string) error {
	run, err := s.load(ctx, id)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if run.Status != "running" || len(run.Attempts) == 0 || run.Attempts[len(run.Attempts)-1].ID != attemptID || run.LeaseUntil == nil || !run.LeaseUntil.After(s.now()) {
		return ErrLease
	}
	return s.Access.Authorize(ctx, run, "execute")
}
func (s Service) Cancel(ctx context.Context, id string) (Run, error) {
	return s.transition(ctx, id, "cancelled", "cancel")
}
func (s Service) Resume(ctx context.Context, id string) (Run, error) {
	return s.transition(ctx, id, "queued", "resume")
}
func (s Service) transition(ctx context.Context, id, status, action string) (Run, error) {
	if !s.valid() {
		return Run{}, errors.New("invalid run configuration")
	}
	run, err := s.load(ctx, id)
	if err != nil {
		return Run{}, err
	}
	if err := s.Access.Authorize(ctx, run, action); err != nil {
		return Run{}, err
	}
	if action == "resume" && run.Status != "waiting" && run.Status != "failed" || action == "cancel" && run.Status != "queued" && run.Status != "running" && run.Status != "waiting" {
		return Run{}, ErrConflict
	}
	now := s.now()
	expected := run.Revision
	if run.Status == "running" {
		last := &run.Attempts[len(run.Attempts)-1]
		last.Status = status
		last.FinishedAt = &now
	}
	run.Status = status
	if action == "resume" {
		run.Result = nil
		run.Error = ""
	}
	run.LeaseUntil = nil
	run.Revision++
	run.UpdatedAt = now
	if err := s.Repository.Replace(ctx, run, expected); err != nil {
		return Run{}, err
	}
	return run, nil
}

type executionHost struct {
	agents.Host
	service          Service
	runID, attemptID string
}

func (h executionHost) Reserve(ctx context.Context, step int, tokens int64) error {
	if err := h.service.reserve(ctx, h.runID, h.attemptID, step, tokens); err != nil {
		return err
	}
	return h.Host.Reserve(ctx, step, tokens)
}
func (h executionHost) RecordUsage(ctx context.Context, step int, usage *agents.Usage, failure error) error {
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := h.service.recordUsage(persist, h.runID, h.attemptID, step, usage); err != nil {
		return err
	}
	return h.Host.RecordUsage(ctx, step, usage, failure)
}
func (h executionHost) Checkpoint(ctx context.Context) error {
	if err := h.service.Checkpoint(ctx, h.runID, h.attemptID); err != nil {
		return err
	}
	return h.Host.Checkpoint(ctx)
}
func (s Service) Execute(ctx context.Context, id string) (Run, error) {
	if s.Hosts == nil || s.Model == nil {
		return Run{}, errors.New("model and host factory are required")
	}
	run, attempt, err := s.Claim(ctx, id)
	if err != nil {
		return Run{}, err
	}
	host, hostErr := s.Hosts.Host(ctx, run, attempt)
	result := agents.Result{}
	if hostErr == nil && host == nil {
		hostErr = errors.New("host factory returned no host")
	}
	if hostErr == nil {
		result, hostErr = (agents.Runtime{Host: executionHost{Host: host, service: s, runID: run.ID, attemptID: attempt.ID}, Model: s.Model, Limits: s.Limits}).Run(ctx, run.ID, attempt.ID, run.Messages)
	}
	if err := s.Checkpoint(ctx, id, attempt.ID); err != nil {
		return Run{}, err
	}
	current, err := s.load(ctx, id)
	if err != nil {
		return Run{}, err
	}
	expected := current.Revision
	now := s.now()
	last := &current.Attempts[len(current.Attempts)-1]
	if last.ID != attempt.ID || current.Status != "running" {
		return Run{}, ErrLease
	}
	current.Status = "completed"
	if hostErr != nil {
		current.Status = "failed"
		if errors.Is(hostErr, agents.ErrWaiting) {
			current.Status = "waiting"
		}
		current.Error = "execution_failed"
		last.Error = "execution_failed"
	}
	last.Result = &result
	current.Result = &result
	current.LeaseUntil = nil
	current.UpdatedAt = now
	current.Revision++
	last.Status = current.Status
	last.FinishedAt = &now
	if err := s.Repository.Replace(ctx, current, expected); err != nil {
		return Run{}, err
	}
	return current, hostErr
}

func (s Service) Renew(ctx context.Context, id, attemptID string) (Run, error) {
	if err := s.Checkpoint(ctx, id, attemptID); err != nil {
		return Run{}, err
	}
	run, err := s.load(ctx, id)
	if err != nil {
		return Run{}, err
	}
	if run.Status != "running" || run.Attempts[len(run.Attempts)-1].ID != attemptID {
		return Run{}, ErrLease
	}
	expected := run.Revision
	now := s.now()
	expiry := now.Add(s.Lease)
	run.LeaseUntil = &expiry
	run.UpdatedAt = now
	run.Revision++
	if err := s.Repository.Replace(ctx, run, expected); err != nil {
		return Run{}, err
	}
	return run, nil
}
