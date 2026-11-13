package agents

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

type modelFunc func(context.Context, Request) (Response, error)

func (f modelFunc) Generate(ctx context.Context, r Request) (Response, error) { return f(ctx, r) }

type host struct {
	calls   []Execution
	checks  int
	expired bool
	result  ToolResult
	usage   []*Usage
}

func (h *host) Tools(context.Context) ([]Tool, error) {
	return []Tool{{Name: "save", Version: 1, Effect: "write", InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"],"additionalProperties":false}`)}}, nil
}
func (h *host) Checkpoint(context.Context) error {
	h.checks++
	if h.expired {
		return errors.New("expired")
	}
	return nil
}
func (h *host) Execute(_ context.Context, e Execution) (ToolResult, error) {
	h.calls = append(h.calls, e)
	return h.result, nil
}
func (h *host) Reserve(context.Context, int, int64) error { return nil }
func (h *host) RecordUsage(_ context.Context, _ int, u *Usage, _ error) error {
	h.usage = append(h.usage, u)
	return nil
}
func instance(h Host, m Model) Runtime {
	return Runtime{Host: h, Model: m, Limits: Limits{Steps: 4, OutputTokens: 100, TotalTokens: 10000, ContextBytes: 20000}}
}
func TestToolExecutionAndReceipt(t *testing.T) {
	h := &host{result: ToolResult{State: "succeeded", Output: json.RawMessage(`{"saved":true}`), Receipt: json.RawMessage(`{"id":"receipt-1"}`)}}
	count := 0
	m := modelFunc(func(_ context.Context, r Request) (Response, error) {
		count++
		if count == 1 {
			return Response{Complete: true, Usage: &Usage{InputTokens: 1}, Calls: []Call{{ID: "call", Name: "save", Arguments: json.RawMessage(`{"title":"Hello"}`)}}}, nil
		}
		if len(r.Messages) != 3 || r.Messages[2].CallID != "call" {
			t.Fatal("tool output missing")
		}
		return Response{Complete: true, Text: "Saved", Usage: &Usage{InputTokens: 2}}, nil
	})
	result, err := instance(h, m).Run(context.Background(), "run", "attempt", []Message{{Role: "user", Content: "Save"}})
	if err != nil || result.Text != "Saved" || result.Usage.InputTokens != 3 || len(h.calls) != 1 || h.calls[0].OperationID() != "run:0" || len(result.Actions) != 1 {
		t.Fatalf("%+v %v", result, err)
	}
}
func TestStepCeilingReturnsDistinctError(t *testing.T) {
	h := &host{result: ToolResult{State: "succeeded", Output: json.RawMessage(`{"ok":true}`)}}
	m := modelFunc(func(_ context.Context, _ Request) (Response, error) {
		return Response{Complete: true, Usage: &Usage{InputTokens: 1}, Calls: []Call{{ID: "call", Name: "save", Arguments: json.RawMessage(`{"title":"again"}`)}}}, nil
	})
	_, err := instance(h, m).Run(context.Background(), "run", "attempt", []Message{{Role: "user", Content: "Loop"}})
	if !errors.Is(err, ErrStepCeiling) {
		t.Fatalf("expected ErrStepCeiling, got %v", err)
	}
	if errors.Is(err, ErrBudget) {
		t.Fatal("step exhaustion must not report as ErrBudget")
	}
}
func TestValidateWholeBatchBeforeAnySideEffect(t *testing.T) {
	for _, argument := range []string{`{"title":12}`, `{}`, `{"title":"valid","unknown":true}`} {
		h := &host{}
		m := modelFunc(func(context.Context, Request) (Response, error) {
			return Response{Complete: true, Calls: []Call{{ID: "first", Name: "save", Arguments: json.RawMessage(`{"title":"valid"}`)}, {ID: "second", Name: "save", Arguments: json.RawMessage(argument)}}}, nil
		})
		if _, err := instance(h, m).Run(context.Background(), "r", "a", nil); err == nil || len(h.calls) != 0 {
			t.Fatalf("partial execution for %s", argument)
		}
	}
}
func TestLeaseRecheckedAfterModel(t *testing.T) {
	h := &host{}
	m := modelFunc(func(context.Context, Request) (Response, error) {
		h.expired = true
		return Response{Complete: true, Text: "late"}, nil
	})
	if _, err := instance(h, m).Run(context.Background(), "r", "a", nil); err == nil {
		t.Fatal("late answer accepted")
	}
}
func TestApprovalAndUnknownOutcomesStopLoop(t *testing.T) {
	for _, failure := range []Failure{{Code: "approval_required"}, {Code: "lost", OutcomeUnknown: true}} {
		h := &host{result: ToolResult{State: "failed", Failure: &failure}}
		m := modelFunc(func(context.Context, Request) (Response, error) {
			return Response{Complete: true, Calls: []Call{{ID: "c", Name: "save", Arguments: json.RawMessage(`{"title":"valid"}`)}}}, nil
		})
		result, err := instance(h, m).Run(context.Background(), "r", "a", nil)
		if !errors.Is(err, ErrWaiting) || len(result.Actions) != 1 || len(h.calls) != 1 {
			t.Fatalf("%+v %v", result, err)
		}
	}
}
func TestInvalidProviderUsageDoesNotOverflow(t *testing.T) {
	h := &host{}
	m := modelFunc(func(context.Context, Request) (Response, error) {
		return Response{Complete: true, Text: "no", Usage: &Usage{InputTokens: math.MaxInt64, OutputTokens: 1}}, nil
	})
	if _, err := instance(h, m).Run(context.Background(), "r", "a", nil); err == nil || len(h.usage) != 1 || h.usage[0] != nil {
		t.Fatal("invalid usage accepted")
	}
}
func TestCompatibleCapturedResponse(t *testing.T) {
	raw, err := os.ReadFile("testdata/chat-completion.json")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Error(r.URL.Path)
		}
		_, _ = w.Write(raw)
	}))
	defer server.Close()
	model := CompatibleModel{BaseURL: server.URL, Client: server.Client(), Model: "fixture"}
	response, err := model.Generate(context.Background(), Request{Messages: []Message{{Role: "user", Content: "Hello"}}, MaxOutputTokens: 100})
	if err != nil || !response.Complete || response.Usage == nil || response.Usage.InputTokens != 19 || response.Usage.OutputTokens != 10 {
		t.Fatalf("%+v %v", response, err)
	}
}
