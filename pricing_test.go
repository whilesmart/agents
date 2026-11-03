package agents

import "testing"

func TestPriceUsesIntegerMicrosAndRoundsUp(t *testing.T) {
	price := Price{Version: "test", Currency: "USD", InputMicrosPerMillion: 150000, OutputMicrosPerMillion: 600000}
	cost, err := price.Cost(Usage{InputTokens: 1000, OutputTokens: 1000})
	if err != nil || cost != 750 {
		t.Fatalf("Cost %d %v", cost, err)
	}
	cost, err = price.Cost(Usage{InputTokens: 1})
	if err != nil || cost != 1 {
		t.Fatalf("Fractional micro-unit cost %d %v", cost, err)
	}
	reserved, err := price.Reserve(2000)
	if err != nil || reserved != 1200 {
		t.Fatalf("Reservation %d %v", reserved, err)
	}
	if _, err := price.Cost(Usage{InputTokens: -1}); err == nil {
		t.Fatal("Negative usage was priced")
	}
}
