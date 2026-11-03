package runs

import (
	"context"
	"github.com/whilesmart/agents"
)

func (s Service) reserve(ctx context.Context, id, attempt string, step int, tokens int64) error {
	if tokens < 1 || step < 0 {
		return agents.ErrBudget
	}
	if err := s.Checkpoint(ctx, id, attempt); err != nil {
		return err
	}
	run, err := s.load(ctx, id)
	if err != nil {
		return err
	}
	remaining := s.Limits.TotalTokens
	for _, a := range run.Attempts {
		for _, call := range a.Calls {
			used := call.Reserved
			if call.Usage != nil {
				if call.Usage.InputTokens > remaining || call.Usage.OutputTokens > remaining-call.Usage.InputTokens {
					return agents.ErrBudget
				}
				used = call.Usage.InputTokens + call.Usage.OutputTokens
			}
			if used > remaining {
				return agents.ErrBudget
			}
			remaining -= used
		}
	}
	if tokens > remaining {
		return agents.ErrBudget
	}
	last := &run.Attempts[len(run.Attempts)-1]
	if last.ID != attempt {
		return ErrLease
	}
	for _, call := range last.Calls {
		if call.Step == step {
			return ErrConflict
		}
	}
	expected := run.Revision
	last.Calls = append(last.Calls, UsageRecord{Step: step, Reserved: tokens})
	run.Revision++
	run.UpdatedAt = s.now()
	return s.Repository.Replace(ctx, run, expected)
}
func (s Service) recordUsage(ctx context.Context, id, attempt string, step int, usage *agents.Usage) error {
	run, err := s.load(ctx, id)
	if err != nil {
		return err
	}
	expected := run.Revision
	for i := range run.Attempts {
		if run.Attempts[i].ID == attempt {
			for j := range run.Attempts[i].Calls {
				call := &run.Attempts[i].Calls[j]
				if call.Step != step {
					continue
				}
				if call.Recorded {
					return ErrConflict
				}
				if usage != nil {
					if usage.InputTokens < 0 || usage.OutputTokens < 0 {
						return agents.ErrBudget
					}
					copy := *usage
					call.Usage = &copy
				}
				call.Recorded = true
				run.Revision++
				run.UpdatedAt = s.now()
				return s.Repository.Replace(ctx, run, expected)
			}
		}
	}
	return ErrConflict
}
