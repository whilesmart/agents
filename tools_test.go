package agents

import (
	"context"
	"encoding/json"
	"testing"
)

func TestApplicationsRegisterOwnAuthorizedTools(t *testing.T) {
	allowed := true
	registry, err := NewTools(ToolAuthorizeFunc(func(context.Context, Tool, string) error {
		if !allowed {
			return ErrToolDenied
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	tool := Tool{Name: "inventory_reserve", Version: 2, Effect: "write", InputSchema: json.RawMessage(`{"type":"object","properties":{"quantity":{"type":"integer","minimum":1}},"required":["quantity"],"additionalProperties":false}`)}
	if err := registry.Register(tool, func(context.Context, Execution) (ToolResult, error) {
		calls++
		return ToolResult{State: "succeeded", Output: json.RawMessage(`{"reserved":true}`)}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(tool, func(context.Context, Execution) (ToolResult, error) {
		t.Fatal("replaced handler executed")
		return ToolResult{}, nil
	}); err != ErrToolExists {
		t.Fatal("registered tool replaced")
	}
	catalogue, _ := registry.Tools(context.Background())
	if len(catalogue) != 1 || catalogue[0].Name != tool.Name {
		t.Fatal(catalogue)
	}
	_, err = registry.Execute(context.Background(), Execution{Tool: tool, Arguments: json.RawMessage(`{"quantity":0}`)})
	if err == nil || calls != 0 {
		t.Fatal("invalid input reached application")
	}
	result, err := registry.Execute(context.Background(), Execution{Tool: tool, Arguments: json.RawMessage(`{"quantity":3}`)})
	if err != nil || result.State != "succeeded" || calls != 1 {
		t.Fatalf("%+v %v", result, err)
	}
	allowed = false
	catalogue, err = registry.Tools(context.Background())
	if err != nil || len(catalogue) != 0 {
		t.Fatal("unauthorized tool discovered")
	}
	if _, err := registry.Execute(context.Background(), Execution{Tool: tool, Arguments: json.RawMessage(`{"quantity":3}`)}); err != ErrToolDenied || calls != 1 {
		t.Fatal("registration bypassed authorization")
	}
}
