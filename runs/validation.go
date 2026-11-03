package runs

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"unicode/utf8"
)

func (r Run) Validate() error {
	valid := func(s string) bool {
		return s != "" && len(s) <= 2048 && utf8.ValidString(s) && !strings.ContainsAny(s, "\x00\r\n")
	}
	if !valid(r.ID) || !valid(r.Scope) || !valid(r.Requester) || r.Revision == 0 || r.CreatedAt.IsZero() || r.UpdatedAt.Before(r.CreatedAt) {
		return errors.New("invalid run record")
	}
	switch r.Status {
	case "queued", "running", "waiting", "failed", "completed", "cancelled":
	default:
		return errors.New("invalid run status")
	}
	raw, err := json.Marshal(r.Messages)
	if err != nil || len(raw) > 1<<20 {
		return errors.New("invalid run messages")
	}
	for _, m := range r.Messages {
		if m.Role == "" {
			return errors.New("message role is required")
		}
	}
	ids := map[string]bool{}
	for i, a := range r.Attempts {
		if !valid(a.ID) || ids[a.ID] || a.Number != uint64(i+1) || a.StartedAt.IsZero() {
			return errors.New("invalid attempt record")
		}
		ids[a.ID] = true
		for step, call := range a.Calls {
			if call.Step != step || call.Reserved < 1 || call.Usage != nil && !call.Recorded {
				return errors.New("invalid usage reservation")
			}
			if call.Usage != nil && (call.Usage.InputTokens < 0 || call.Usage.OutputTokens < 0 || call.Usage.InputTokens > math.MaxInt64-call.Usage.OutputTokens) {
				return errors.New("invalid recorded usage")
			}
		}
		if a.Result != nil {
			for _, action := range a.Result.Actions {
				if err := action.Validate(); err != nil {
					return err
				}
			}
		}

		switch a.Status {
		case "running":
			if a.FinishedAt != nil {
				return errors.New("running attempt has an end time")
			}
		case "abandoned", "completed", "waiting", "failed", "cancelled":
			if a.FinishedAt == nil || a.FinishedAt.Before(a.StartedAt) {
				return errors.New("finished attempt needs an end time")
			}
		default:
			return errors.New("invalid attempt status")
		}
	}
	if r.Status == "running" && (len(r.Attempts) == 0 || r.LeaseUntil == nil || r.Attempts[len(r.Attempts)-1].Status != "running") {
		return errors.New("running attempt and lease are required")
	}
	if r.Status != "running" && r.LeaseUntil != nil {
		return errors.New("inactive run has a lease")
	}
	if r.Status == "completed" && r.Result == nil {
		return errors.New("completed run has no result")
	}
	if r.Result != nil {
		for _, action := range r.Result.Actions {
			if err := action.Validate(); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s Service) load(ctx context.Context, id string) (Run, error) {
	if !s.valid() {
		return Run{}, errors.New("invalid run configuration")
	}
	run, err := s.Repository.Get(ctx, id)
	if err != nil {
		return Run{}, err
	}
	if err := run.Validate(); err != nil {
		return Run{}, err
	}
	return run, nil
}
