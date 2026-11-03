package agents

import (
	"context"
	"encoding/json"
	"testing"
)

func TestJournalReusesCompletedActionAndRejectsChangedRetry(t *testing.T) {
	original := &host{result: ToolResult{State: "succeeded", Output: json.RawMessage(`{"id":"saved"}`)}}
	wrapped, err := WithJournal(original, "account", NewMemoryJournal())
	if err != nil {
		t.Fatal(err)
	}
	execution := Execution{RunID: "run", AttemptID: "first", Ordinal: 0, Tool: Tool{Name: "save", Version: 1}, Arguments: json.RawMessage(`{"title":"hello"}`)}
	first, err := wrapped.Execute(context.Background(), execution)
	if err != nil {
		t.Fatal(err)
	}
	execution.AttemptID = "second"
	second, err := wrapped.Execute(context.Background(), execution)
	if err != nil || len(original.calls) != 1 || string(second.Output) != string(first.Output) {
		t.Fatal("retried write executed twice")
	}
	execution.Arguments = json.RawMessage(`{"title":"different"}`)
	if _, err := wrapped.Execute(context.Background(), execution); err != ErrReceiptConflict {
		t.Fatal("changed retry accepted")
	}
}
func TestPendingReceiptNeverRepeatsAnUnknownWrite(t *testing.T) {
	original := &host{result: ToolResult{State: "succeeded"}}
	journal := NewMemoryJournal()
	wrapped, _ := WithJournal(original, "account", journal)
	execution := Execution{RunID: "r", Tool: Tool{Name: "save", Version: 1}, Arguments: json.RawMessage(`{}`)}
	first, _ := wrapped.Execute(context.Background(), execution)
	if first.State != "succeeded" {
		t.Fatal(first)
	}
	journal.mu.Lock()
	row := journal.rows[OperationKey("account", execution)]
	row.Result = nil
	journal.rows[OperationKey("account", execution)] = row
	journal.mu.Unlock()
	result, err := wrapped.Execute(context.Background(), execution)
	if err != nil || result.Failure == nil || !result.Failure.OutcomeUnknown || len(original.calls) != 1 {
		t.Fatal("uncertain action repeated")
	}
}

func TestOperationNamespacesCannotCollide(t *testing.T) {
	if OperationKey("a:b", Execution{RunID: "c", Ordinal: 1}) == OperationKey("a", Execution{RunID: "b:c", Ordinal: 1}) {
		t.Fatal("ambiguous operation scope")
	}
}
