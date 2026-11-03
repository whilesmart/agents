package agents

import (
	"encoding/json"
	"github.com/google/jsonschema-go/jsonschema"
)

func resolveSchema(raw json.RawMessage) (*jsonschema.Resolved, error) {
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, err
	}
	return schema.Resolve(nil)
}
func validateInput(raw, input json.RawMessage) error {
	resolved, err := resolveSchema(raw)
	if err != nil {
		return err
	}
	var value any
	if err := json.Unmarshal(input, &value); err != nil {
		return err
	}
	return resolved.Validate(value)
}
