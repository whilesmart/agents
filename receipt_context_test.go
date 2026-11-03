package agents

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestReceiptStaysInHistoryWithoutEnteringModelContext(t *testing.T) {
	raw, _ := json.Marshal(map[string]string{"audit": "private-audit", "large": strings.Repeat("x", 150000)})
	h := &host{result: ToolResult{State: "succeeded", Output: json.RawMessage(`{"saved":true}`), Receipt: raw}}
	step := 0
	model := modelFunc(func(_ context.Context, input Request) (Response, error) {
		step++
		if step == 1 {
			return Response{Complete: true, Calls: []Call{{ID: "save", Name: "save", Arguments: json.RawMessage(`{"title":"hello"}`)}}}, nil
		}
		if strings.Contains(input.Messages[1].Content, "private-audit") || strings.Contains(input.Messages[1].Content, "receipt") {
			t.Fatal("receipt entered model context")
		}
		return Response{Complete: true, Text: "saved"}, nil
	})
	result, err := instance(h, model).Run(context.Background(), "r", "a", nil)
	if err != nil || len(result.Actions) != 1 || len(result.Actions[0].Receipt) != len(raw) {
		t.Fatalf("receipt lost or model context inflated: %+v %v", result, err)
	}
}
