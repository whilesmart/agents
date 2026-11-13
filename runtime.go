package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
)

var ErrContextLimit = errors.New("agent context limit exceeded")
var ErrBudget = errors.New("agent budget exhausted")
var ErrIncomplete = errors.New("agent response is incomplete")
var ErrWaiting = errors.New("agent action requires approval or reconciliation")
var ErrStepCeiling = errors.New("agent reached the step ceiling before completing")

// MaxSteps is the upper bound on Limits.Steps. It is a runaway safety ceiling,
// not a usage governor: set it high enough that real multi-resource work never
// reaches it.
const MaxSteps = 512

type Usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Version     uint32          `json:"version,omitempty"`
	Effect      string          `json:"effect,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type Call struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content,omitempty"`
	Calls   []Call `json:"calls,omitempty"`
	CallID  string `json:"call_id,omitempty"`
}

type Request struct {
	Messages        []Message
	Tools           []Tool
	MaxOutputTokens int
}

type Response struct {
	Text     string
	Calls    []Call
	Usage    *Usage
	Complete bool
}

type Model interface {
	Generate(context.Context, Request) (Response, error)
}

type Execution struct {
	RunID     string
	AttemptID string
	Ordinal   int
	Tool      Tool
	Arguments json.RawMessage
}

func (e Execution) OperationID() string { return fmt.Sprintf("%s:%d", e.RunID, e.Ordinal) }

// Host must recheck execution authority and durably record unknown usage, including failed calls.
type Host interface {
	Tools(context.Context) ([]Tool, error)
	Checkpoint(context.Context) error
	Execute(context.Context, Execution) (ToolResult, error)
	Reserve(context.Context, int, int64) error
	RecordUsage(context.Context, int, *Usage, error) error
}

type Limits struct {
	Steps        int
	OutputTokens int
	TotalTokens  int64
	ContextBytes int
}

type Runtime struct {
	Model  Model
	Host   Host
	Limits Limits
}

type Result struct {
	Text         string       `json:"text"`
	Usage        Usage        `json:"usage"`
	UnknownUsage bool         `json:"unknown_usage"`
	Actions      []ToolResult `json:"actions"`
}

func (r Runtime) Run(ctx context.Context, runID, attemptID string, messages []Message) (result Result, err error) {
	l := r.Limits
	if r.Model == nil || r.Host == nil || strings.TrimSpace(runID) == "" || strings.TrimSpace(attemptID) == "" || l.Steps < 1 || l.Steps > MaxSteps || l.OutputTokens < 1 || l.TotalTokens < int64(l.OutputTokens) || l.ContextBytes < 1 {
		return result, errors.New("invalid agent runtime configuration")
	}
	messages = append([]Message(nil), messages...)
	ordinal := 0
	readResults := map[string]int{}
	remaining := l.TotalTokens
	for step := 0; step < l.Steps; step++ {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		if err = r.Host.Checkpoint(ctx); err != nil {
			return result, err
		}
		tools, toolErr := r.Host.Tools(ctx)
		if toolErr != nil {
			return result, toolErr
		}
		byName := make(map[string]Tool, len(tools))
		for _, tool := range tools {
			if tool.Name == "" || !json.Valid(tool.InputSchema) {
				return result, errors.New("invalid registered tool")
			}
			if _, err := resolveSchema(tool.InputSchema); err != nil {
				return result, errors.New("invalid registered tool schema")
			}
			if _, exists := byName[tool.Name]; exists {
				return result, errors.New("duplicate registered tool")
			}
			byName[tool.Name] = tool
		}
		encoded, encodeErr := json.Marshal(struct {
			Messages []Message
			Tools    []Tool
		}{messages, tools})
		if encodeErr != nil || len(encoded) > l.ContextBytes {
			return result, ErrContextLimit
		}
		// One token per input byte reserves a conservative bound before usage is known.
		reserve := int64(len(encoded)) + int64(l.OutputTokens)
		if reserve > remaining {
			return result, ErrBudget
		}
		if err := r.Host.Reserve(ctx, step, reserve); err != nil {
			return result, err
		}
		remaining -= reserve
		response, modelErr := r.Model.Generate(ctx, Request{Messages: messages, Tools: tools, MaxOutputTokens: l.OutputTokens})
		if response.Usage != nil && (response.Usage.InputTokens < 0 || response.Usage.OutputTokens < 0 || response.Usage.InputTokens > math.MaxInt64-response.Usage.OutputTokens) {
			response.Usage = nil
			modelErr = errors.New("invalid provider usage")
		}
		if usageErr := r.Host.RecordUsage(ctx, step, response.Usage, modelErr); usageErr != nil {
			return result, usageErr
		}
		if response.Usage == nil {
			result.UnknownUsage = true
		} else {
			u := response.Usage
			if u.InputTokens < 0 || u.OutputTokens < 0 {
				return result, errors.New("invalid provider usage")
			}
			if result.Usage.InputTokens > math.MaxInt64-u.InputTokens || result.Usage.OutputTokens > math.MaxInt64-u.OutputTokens {
				return result, ErrBudget
			}
			result.Usage.InputTokens += u.InputTokens
			result.Usage.OutputTokens += u.OutputTokens
			used := u.InputTokens + u.OutputTokens
			if used > remaining+reserve {
				return result, ErrBudget
			}
			remaining += reserve - used
			if remaining < 0 {
				return result, ErrBudget
			}
		}
		if modelErr != nil {
			return result, modelErr
		}
		if err = ctx.Err(); err != nil {
			return result, err
		}
		if err = r.Host.Checkpoint(ctx); err != nil {
			return result, err
		}
		if !response.Complete {
			return result, ErrIncomplete
		}
		if len(response.Calls) == 0 {
			if strings.TrimSpace(response.Text) == "" {
				return result, ErrIncomplete
			}
			result.Text = response.Text
			return result, nil
		}
		if len(response.Calls) > 16 {
			return result, errors.New("too many tool calls")
		}
		messages = append(messages, Message{Role: "assistant", Content: response.Text, Calls: response.Calls})
		ids := map[string]bool{}
		for _, call := range response.Calls {
			tool, exists := byName[call.Name]
			if !exists || call.ID == "" || ids[call.ID] || !json.Valid(call.Arguments) {
				return result, errors.New("invalid tool call")
			}
			ids[call.ID] = true
			if err := validateInput(tool.InputSchema, call.Arguments); err != nil {
				return result, errors.New("tool arguments do not match the registered schema")
			}
		}
		for _, call := range response.Calls {
			tool := byName[call.Name]
			if err = r.Host.Checkpoint(ctx); err != nil {
				return result, err
			}
			action, actionErr := r.Host.Execute(ctx, Execution{RunID: runID, AttemptID: attemptID, Ordinal: ordinal, Tool: tool, Arguments: call.Arguments})
			ordinal++
			if actionErr != nil {
				return result, actionErr
			}
			if action.Validate() != nil {
				return result, errors.New("invalid tool result")
			}
			result.Actions = append(result.Actions, action)
			if action.Failure != nil && (action.Failure.OutcomeUnknown || action.Failure.Code == "approval_required") {
				return result, ErrWaiting
			}
			contextResult := action
			contextResult.Receipt = nil
			data, marshalErr := json.Marshal(contextResult)
			if marshalErr != nil || len(data) > 128<<10 {
				return result, errors.New("tool result exceeds context limit")
			}
			if tool.Effect == "read" && action.State == "succeeded" {
				var arguments any
				decoder := json.NewDecoder(strings.NewReader(string(call.Arguments)))
				decoder.UseNumber()
				if err := decoder.Decode(&arguments); err != nil {
					return result, err
				}
				canonical, _ := json.Marshal(arguments)
				key := fmt.Sprintf("%s:%d:%s", call.Name, tool.Version, canonical)
				if previous, exists := readResults[key]; exists {
					var earlier ToolResult
					if json.Unmarshal([]byte(messages[previous].Content), &earlier) == nil && earlier.State == "succeeded" {
						earlier.Output = json.RawMessage(`{"superseded":true}`)
						compacted, _ := json.Marshal(earlier)
						messages[previous].Content = string(compacted)
					}
				}
				readResults[key] = len(messages)
			}
			messages = append(messages, Message{Role: "tool", CallID: call.ID, Content: string(data)})
		}
	}
	return result, ErrStepCeiling
}
