package agents

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"sync"
)

var ErrToolDenied = errors.New("tool access denied")
var ErrToolExists = errors.New("tool is already registered")

type Handler func(context.Context, Execution) (ToolResult, error)
type ToolAuthorizer interface {
	AuthorizeTool(context.Context, Tool, string) error
}
type ToolAuthorizeFunc func(context.Context, Tool, string) error

func (f ToolAuthorizeFunc) AuthorizeTool(ctx context.Context, tool Tool, action string) error {
	return f(ctx, tool, action)
}

type registration struct {
	tool    Tool
	handler Handler
}
type Tools struct {
	mu         sync.RWMutex
	policy     ToolAuthorizer
	registered map[string]registration
}

func NewTools(policy ToolAuthorizer) (*Tools, error) {
	if policy == nil {
		return nil, errors.New("tool authorizer is required")
	}
	return &Tools{policy: policy, registered: map[string]registration{}}, nil
}
func (t *Tools) Register(tool Tool, handler Handler) error {
	if tool.Name == "" || tool.Version == 0 || handler == nil {
		return errors.New("tool name, version and handler are required")
	}
	if _, err := resolveSchema(tool.InputSchema); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.registered[tool.Name]; ok {
		return ErrToolExists
	}
	tool.InputSchema = bytes.Clone(tool.InputSchema)
	t.registered[tool.Name] = registration{tool: tool, handler: handler}
	return nil
}
func (t *Tools) Tools(ctx context.Context) ([]Tool, error) {
	t.mu.RLock()
	values := make([]registration, 0, len(t.registered))
	for _, r := range t.registered {
		values = append(values, r)
	}
	t.mu.RUnlock()
	rows := []Tool{}
	for _, r := range values {
		tool := r.tool
		tool.InputSchema = bytes.Clone(tool.InputSchema)
		if err := t.policy.AuthorizeTool(ctx, tool, "discover"); err != nil {
			if errors.Is(err, ErrToolDenied) {
				continue
			}
			return nil, err
		}
		rows = append(rows, tool)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows, nil
}
func (t *Tools) Execute(ctx context.Context, e Execution) (ToolResult, error) {
	t.mu.RLock()
	r, ok := t.registered[e.Tool.Name]
	t.mu.RUnlock()
	if !ok || e.Tool.Version != r.tool.Version {
		return ToolResult{}, ErrToolDenied
	}
	tool := r.tool
	tool.InputSchema = bytes.Clone(tool.InputSchema)
	if err := t.policy.AuthorizeTool(ctx, tool, "execute"); err != nil {
		return ToolResult{}, err
	}
	if err := validateInput(tool.InputSchema, e.Arguments); err != nil {
		return ToolResult{}, err
	}
	e.Tool = tool
	e.Arguments = bytes.Clone(e.Arguments)
	result, err := r.handler(ctx, e)
	if err == nil {
		err = result.Validate()
	}
	return result, err
}

type Toolset interface {
	Tools(context.Context) ([]Tool, error)
	Execute(context.Context, Execution) (ToolResult, error)
}
type HostAdapter struct {
	Toolset Toolset
	Current func(context.Context) error
	Budget  func(context.Context, int, int64) error
	Meter   func(context.Context, int, *Usage, error) error
}

func (h HostAdapter) Checkpoint(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if h.Current == nil {
		return errors.New("execution checkpoint is required")
	}
	return h.Current(ctx)
}
func (h HostAdapter) Tools(ctx context.Context) ([]Tool, error) {
	if err := h.Checkpoint(ctx); err != nil {
		return nil, err
	}
	if h.Toolset == nil {
		return nil, errors.New("toolset is required")
	}
	return h.Toolset.Tools(ctx)
}
func (h HostAdapter) Execute(ctx context.Context, e Execution) (ToolResult, error) {
	if err := h.Checkpoint(ctx); err != nil {
		return ToolResult{}, err
	}
	if h.Toolset == nil {
		return ToolResult{}, errors.New("toolset is required")
	}
	return h.Toolset.Execute(ctx, e)
}
func (h HostAdapter) Reserve(ctx context.Context, step int, tokens int64) error {
	if err := h.Checkpoint(ctx); err != nil {
		return err
	}
	if h.Budget == nil {
		return errors.New("budget reservation is required")
	}
	return h.Budget(ctx, step, tokens)
}
func (h HostAdapter) RecordUsage(ctx context.Context, step int, usage *Usage, failure error) error {
	if h.Meter == nil {
		return errors.New("usage recorder is required")
	}
	return h.Meter(ctx, step, usage, failure)
}
