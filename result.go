package agents

import (
	"encoding/json"
	"errors"
)

type Failure struct {
	Code           string `json:"code"`
	Message        string `json:"message,omitempty"`
	OutcomeUnknown bool   `json:"outcome_unknown,omitempty"`
}

type ToolResult struct {
	State   string          `json:"state"`
	Output  json.RawMessage `json:"output,omitempty"`
	Failure *Failure        `json:"failure,omitempty"`
	Receipt json.RawMessage `json:"receipt,omitempty"`
}

func (r ToolResult) Validate() error {
	if len(r.Output) > 0 && !json.Valid(r.Output) || len(r.Receipt) > 0 && !json.Valid(r.Receipt) {
		return errors.New("invalid tool result JSON")
	}
	switch r.State {
	case "succeeded":
		if r.Failure != nil {
			return errors.New("successful tool has a failure")
		}
	case "failed":
		if r.Failure == nil || r.Failure.Code == "" {
			return errors.New("failed tool requires a failure code")
		}
	default:
		return errors.New("invalid tool result state")
	}
	return nil
}
