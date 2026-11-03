package agents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
)

var ErrReceiptConflict = errors.New("operation receipt conflict")

type Receipt struct {
	Key         string      `json:"key"`
	Fingerprint string      `json:"fingerprint"`
	Result      *ToolResult `json:"result,omitempty"`
}

// Journal claims are atomic; a pending receipt must never be treated as a new operation.
type Journal interface {
	Claim(context.Context, string, string) (Receipt, bool, error)
	Complete(context.Context, string, string, ToolResult) error
}
type journalHost struct {
	Host
	namespace string
	journal   Journal
}

func WithJournal(host Host, namespace string, journal Journal) (Host, error) {
	if host == nil || namespace == "" || journal == nil {
		return nil, errors.New("host, namespace and journal are required")
	}
	return journalHost{Host: host, namespace: namespace, journal: journal}, nil
}
func (h journalHost) Execute(ctx context.Context, e Execution) (ToolResult, error) {
	if err := h.Checkpoint(ctx); err != nil {
		return ToolResult{}, err
	}
	if e.RunID == "" || e.Ordinal < 0 || !json.Valid(e.Arguments) {
		return ToolResult{}, errors.New("invalid operation identity or arguments")
	}
	var input any
	decoder := json.NewDecoder(strings.NewReader(string(e.Arguments)))
	decoder.UseNumber()
	if err := decoder.Decode(&input); err != nil {
		return ToolResult{}, err
	}
	raw, err := json.Marshal(struct {
		Tool    string
		Version uint32
		Input   any
	}{e.Tool.Name, e.Tool.Version, input})
	if err != nil {
		return ToolResult{}, err
	}
	digest := sha256.Sum256(raw)
	fingerprint := hex.EncodeToString(digest[:])
	key := OperationKey(h.namespace, e)
	receipt, claimed, err := h.journal.Claim(ctx, key, fingerprint)
	if err != nil {
		return ToolResult{}, err
	}
	if receipt.Key != key || receipt.Fingerprint != fingerprint {
		return ToolResult{}, ErrReceiptConflict
	}
	if claimed && receipt.Result != nil {
		return ToolResult{}, ErrReceiptConflict
	}
	if !claimed {
		if receipt.Result != nil {
			return *receipt.Result, nil
		}
		return ToolResult{State: "failed", Failure: &Failure{Code: "operation_uncertain", OutcomeUnknown: true}}, nil
	}
	result, executionErr := h.Host.Execute(ctx, e)
	if executionErr != nil || result.Validate() != nil {
		result = ToolResult{State: "failed", Failure: &Failure{Code: "operation_uncertain", OutcomeUnknown: true}}
	}
	if err := h.journal.Complete(ctx, key, fingerprint, result); err != nil {
		return ToolResult{State: "failed", Failure: &Failure{Code: "operation_uncertain", OutcomeUnknown: true}}, nil
	}
	return result, nil
}

type MemoryJournal struct {
	mu   sync.Mutex
	rows map[string]Receipt
}

func NewMemoryJournal() *MemoryJournal { return &MemoryJournal{rows: map[string]Receipt{}} }
func cloneReceipt(row Receipt) (Receipt, error) {
	raw, err := json.Marshal(row)
	if err != nil {
		return Receipt{}, err
	}
	var copy Receipt
	err = json.Unmarshal(raw, &copy)
	return copy, err
}
func (j *MemoryJournal) Claim(ctx context.Context, key, fingerprint string) (Receipt, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Receipt{}, false, err
	}
	if row, ok := j.rows[key]; ok {
		if row.Fingerprint != fingerprint {
			return Receipt{}, false, ErrReceiptConflict
		}
		copy, err := cloneReceipt(row)
		return copy, false, err
	}
	row := Receipt{Key: key, Fingerprint: fingerprint}
	j.rows[key] = row
	return row, true, nil
}
func (j *MemoryJournal) Complete(ctx context.Context, key, fingerprint string, result ToolResult) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := result.Validate(); err != nil {
		return err
	}
	row, ok := j.rows[key]
	if !ok || row.Fingerprint != fingerprint || row.Result != nil {
		return ErrReceiptConflict
	}
	copy, err := cloneReceipt(Receipt{Key: key, Fingerprint: fingerprint, Result: &result})
	if err != nil {
		return err
	}
	j.rows[key] = copy
	return nil
}

func OperationKey(namespace string, e Execution) string {
	raw, _ := json.Marshal(struct {
		Namespace string
		Run       string
		Ordinal   int
	}{namespace, e.RunID, e.Ordinal})
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}
