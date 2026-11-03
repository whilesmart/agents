package runs

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/whilesmart/agents"
)

func testService() *Service {
	count := 0
	return &Service{Repository: NewMemory(), Access: AccessFunc(func(context.Context, Run, string) error { return nil }), Lease: time.Minute, NewID: func() string { count++; return fmt.Sprint(count) }}
}
func TestClaimAndExpiredAttemptRecovery(t *testing.T) {
	service := testService()
	now := time.Now()
	service.Now = func() time.Time { return now }
	run, err := service.Create(context.Background(), "scope", "user", []agents.Message{{Role: "user", Content: "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	_, first, err := service.Claim(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Claim(context.Background(), run.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate claim accepted")
	}
	now = now.Add(2 * time.Minute)
	next, second, err := service.Claim(context.Background(), run.ID)
	if err != nil || first.ID == second.ID || next.Attempts[0].Status != "abandoned" {
		t.Fatalf("%+v %v", next, err)
	}
	if err := service.Checkpoint(context.Background(), run.ID, first.ID); !errors.Is(err, ErrLease) {
		t.Fatal("expired worker can continue")
	}
	if err := service.Checkpoint(context.Background(), run.ID, second.ID); err != nil {
		t.Fatal(err)
	}
}
func TestCancellationRevokesRunningLease(t *testing.T) {
	s := testService()
	run, _ := s.Create(context.Background(), "scope", "user", nil)
	_, attempt, _ := s.Claim(context.Background(), run.ID)
	if _, err := s.Cancel(context.Background(), run.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Checkpoint(context.Background(), run.ID, attempt.ID); !errors.Is(err, ErrLease) {
		t.Fatal("cancelled run can execute")
	}
}
func TestRevokedAccessStopsExecution(t *testing.T) {
	s := testService()
	run, _ := s.Create(context.Background(), "scope", "user", nil)
	_, attempt, _ := s.Claim(context.Background(), run.ID)
	s.Access = AccessFunc(func(context.Context, Run, string) error { return errors.New("revoked") })
	if err := s.Checkpoint(context.Background(), run.ID, attempt.ID); err == nil {
		t.Fatal("revoked user retained access")
	}
}
func TestRepositoryDoesNotShareMutableMessages(t *testing.T) {
	s := testService()
	run, _ := s.Create(context.Background(), "scope", "user", []agents.Message{{Role: "user", Content: "original"}})
	run.Messages[0].Content = "mutated"
	saved, _ := s.Get(context.Background(), run.ID)
	if saved.Messages[0].Content != "original" {
		t.Fatal("caller changed stored messages")
	}
}

func TestRepositoryRejectsUsageThatCouldFreeBudget(t *testing.T) {
	s := testService()
	run, _ := s.Create(context.Background(), "scope", "user", nil)
	run, _, _ = s.Claim(context.Background(), run.ID)
	expected := run.Revision
	run.Revision++
	run.Attempts[0].Calls = []UsageRecord{{Step: 0, Reserved: 100, Recorded: true, Usage: &agents.Usage{InputTokens: -1}}}
	if err := s.Repository.Replace(context.Background(), run, expected); err == nil {
		t.Fatal("negative usage could free budget")
	}
}
