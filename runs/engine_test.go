package runs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/whilesmart/agents"
)

type testModel func(context.Context, agents.Request) (agents.Response, error)

func (f testModel) Generate(ctx context.Context, r agents.Request) (agents.Response, error) {
	return f(ctx, r)
}
func runtimeHost() agents.Host {
	return agents.HostAdapter{Toolset: emptyTools{}, Current: func(context.Context) error { return nil }, Budget: func(context.Context, int, int64) error { return nil }, Meter: func(context.Context, int, *agents.Usage, error) error { return nil }}
}

type emptyTools struct{}

func (emptyTools) Tools(context.Context) ([]agents.Tool, error) { return nil, nil }
func (emptyTools) Execute(context.Context, agents.Execution) (agents.ToolResult, error) {
	return agents.ToolResult{}, errors.New("no tools")
}
func TestExecutionPersistsUsageAndFinishedAttempt(t *testing.T) {
	s := testService()
	s.Model = testModel(func(context.Context, agents.Request) (agents.Response, error) {
		return agents.Response{Complete: true, Text: "done", Usage: &agents.Usage{InputTokens: 2, OutputTokens: 3}}, nil
	})
	s.Hosts = HostFunc(func(context.Context, Run, Attempt) (agents.Host, error) { return runtimeHost(), nil })
	s.Limits = agents.Limits{Steps: 2, OutputTokens: 100, TotalTokens: 10000, ContextBytes: 10000}
	run, err := s.Create(context.Background(), "workspace", "user", []agents.Message{{Role: "user", Content: "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	final, err := s.Execute(context.Background(), run.ID)
	if err != nil || final.Status != "completed" || final.Result.Text != "done" || len(final.Attempts[0].Calls) != 1 || final.Attempts[0].Calls[0].Usage.InputTokens != 2 {
		t.Fatalf("%+v %v", final, err)
	}
}
func TestUnknownUsageCannotBeFreedByRetry(t *testing.T) {
	s := testService()
	calls := 0
	s.Model = testModel(func(context.Context, agents.Request) (agents.Response, error) {
		calls++
		return agents.Response{}, errors.New("interrupted")
	})
	s.Hosts = HostFunc(func(context.Context, Run, Attempt) (agents.Host, error) { return runtimeHost(), nil })
	s.Limits = agents.Limits{Steps: 1, OutputTokens: 100, TotalTokens: 700, ContextBytes: 10000}
	run, _ := s.Create(context.Background(), "workspace", "user", []agents.Message{{Role: "user", Content: "hello"}})
	for i := 0; i < 10; i++ {
		final, _ := s.Execute(context.Background(), run.ID)
		if final.Status != "failed" {
			t.Fatalf("%+v", final)
		}
		if _, err := s.Resume(context.Background(), run.ID); err != nil {
			t.Fatal(err)
		}
	}
	if calls >= 10 {
		t.Fatal("retries reset unknown usage")
	}
}
func TestLongAttemptCanRenewBeforeExpiry(t *testing.T) {
	s := testService()
	now := time.Now()
	s.Now = func() time.Time { return now }
	run, _ := s.Create(context.Background(), "workspace", "user", nil)
	_, attempt, _ := s.Claim(context.Background(), run.ID)
	now = now.Add(30 * time.Second)
	renewed, err := s.Renew(context.Background(), run.ID, attempt.ID)
	if err != nil || renewed.LeaseUntil.Sub(now) != s.Lease {
		t.Fatal(err)
	}
}
