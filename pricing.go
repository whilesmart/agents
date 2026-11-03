package agents

import (
	"errors"
	"math/big"
)

type Price struct {
	Version                string `json:"version"`
	Currency               string `json:"currency"`
	InputMicrosPerMillion  int64  `json:"input_micros_per_million"`
	OutputMicrosPerMillion int64  `json:"output_micros_per_million"`
}

func (p Price) Validate() error {
	if p.Version == "" || len(p.Currency) != 3 || p.InputMicrosPerMillion < 0 || p.OutputMicrosPerMillion < 0 {
		return errors.New("model pricing requires a version, currency and nonnegative rates")
	}
	for _, letter := range p.Currency {
		if letter < 'A' || letter > 'Z' {
			return errors.New("pricing currency must use three uppercase letters")
		}
	}
	return nil
}
func (p Price) Cost(usage Usage) (int64, error) {
	if err := p.Validate(); err != nil {
		return 0, err
	}
	if usage.InputTokens < 0 || usage.OutputTokens < 0 {
		return 0, errors.New("token usage cannot be negative")
	}
	input := new(big.Int).Mul(big.NewInt(usage.InputTokens), big.NewInt(p.InputMicrosPerMillion))
	output := new(big.Int).Mul(big.NewInt(usage.OutputTokens), big.NewInt(p.OutputMicrosPerMillion))
	total := new(big.Int).Add(input, output)
	total.Add(total, big.NewInt(999999))
	total.Div(total, big.NewInt(1000000))
	if !total.IsInt64() {
		return 0, errors.New("token cost exceeds the supported range")
	}
	return total.Int64(), nil
}
func (p Price) Reserve(tokens int64) (int64, error) {
	rate := p.InputMicrosPerMillion
	if p.OutputMicrosPerMillion > rate {
		rate = p.OutputMicrosPerMillion
	}
	p.InputMicrosPerMillion = rate
	p.OutputMicrosPerMillion = rate
	return p.Cost(Usage{InputTokens: tokens})
}
