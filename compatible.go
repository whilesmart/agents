package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

type CompatibleModel struct {
	BaseURL         string
	APIKey          string
	Model           string
	TokenParameter  string
	ReasoningEffort string
	Client          *http.Client
	JSONOutput      bool
}

func ValidateReasoningEffort(value string) error {
	switch value {
	case "", "none", "minimal", "low", "medium", "high", "xhigh", "max":
		return nil
	default:
		return errors.New("invalid reasoning effort")
	}
}

func (m *CompatibleModel) Generate(ctx context.Context, input Request) (Response, error) {
	var output Response
	if err := ValidateReasoningEffort(m.ReasoningEffort); err != nil {
		return output, err
	}
	messages := make([]map[string]any, 0, len(input.Messages))
	for _, message := range input.Messages {
		value := map[string]any{"role": message.Role, "content": message.Content}
		if message.CallID != "" {
			value["tool_call_id"] = message.CallID
		}
		if len(message.Calls) > 0 {
			calls := []map[string]any{}
			for _, call := range message.Calls {
				calls = append(calls, map[string]any{"id": call.ID, "type": "function", "function": map[string]any{"name": call.Name, "arguments": string(call.Arguments)}})
			}
			value["tool_calls"] = calls
		}
		messages = append(messages, value)
	}
	tokenParameter := m.TokenParameter
	if tokenParameter == "" {
		tokenParameter = "max_completion_tokens"
	}
	if tokenParameter != "max_completion_tokens" && tokenParameter != "max_tokens" {
		return output, errors.New("invalid token parameter")
	}
	body := map[string]any{"model": m.Model, "messages": messages, tokenParameter: input.MaxOutputTokens, "stream": false}
	if m.ReasoningEffort != "" {
		body["reasoning_effort"] = m.ReasoningEffort
	}
	if m.JSONOutput {
		body["response_format"] = map[string]string{"type": "json_object"}
	}
	if len(input.Tools) > 0 {
		tools := []map[string]any{}
		for _, tool := range input.Tools {
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": tool.Name, "description": tool.Description, "parameters": tool.InputSchema}})
		}
		body["tools"] = tools
	}
	data, err := json.Marshal(body)
	if err != nil {
		return output, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(m.BaseURL, "/")+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return output, err
	}
	req.Header.Set("Authorization", "Bearer "+m.APIKey)
	req.Header.Set("Content-Type", "application/json")
	client := m.Client
	if client == nil {
		return output, errors.New("model HTTP client is required")
	}
	res, err := client.Do(req)
	if err != nil {
		return output, errors.New("model endpoint could not be reached")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return output, errors.New("model endpoint rejected the request")
	}
	data, err = io.ReadAll(io.LimitReader(res.Body, (512<<10)+1))
	if err != nil || len(data) > 512<<10 {
		return output, errors.New("model response exceeds limit")
	}
	var response struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content string `json:"content"`
				Calls   []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage *struct {
			Input  int64 `json:"prompt_tokens"`
			Output int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(data, &response) != nil {
		return output, errors.New("model returned invalid JSON")
	}
	if response.Usage != nil {
		output.Usage = &Usage{InputTokens: response.Usage.Input, OutputTokens: response.Usage.Output}
	}
	if len(response.Choices) != 1 {
		return output, ErrIncomplete
	}
	choice := response.Choices[0]
	output.Text = choice.Message.Content
	for _, call := range choice.Message.Calls {
		if call.Type != "function" || !json.Valid([]byte(call.Function.Arguments)) {
			return output, errors.New("model returned an invalid tool call")
		}
		output.Calls = append(output.Calls, Call{ID: call.ID, Name: call.Function.Name, Arguments: json.RawMessage(call.Function.Arguments)})
	}
	output.Complete = choice.FinishReason == "stop" && len(output.Calls) == 0 || choice.FinishReason == "tool_calls" && len(output.Calls) > 0
	if !output.Complete {
		return output, ErrIncomplete
	}
	return output, nil
}
