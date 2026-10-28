package app

import "testing"

func TestFeeEstimateUsesPerOrderMinimums(t *testing.T) {
	fees := defaultFeeConfig()
	buy := fees.Estimate(ActionBuy, mustDecimal(t, "100"), 10)
	if buy.String() != "1.03" {
		t.Fatalf("buy fee = %s, want 1.03 (platform min 1 + settlement 0.03)", buy)
	}
	sell := fees.Estimate(ActionSell, mustDecimal(t, "100"), 10)
	if sell.String() != "1.04166" {
		t.Fatalf("sell fee = %s, want 1.04166", sell)
	}
	large := fees.Estimate(ActionBuy, mustDecimal(t, "10"), 1000)
	if large.String() != "8" {
		t.Fatalf("large buy fee = %s, want 8 (0.005*1000 + 0.003*1000)", large)
	}
	if got := (FeeConfig{}).RoundTrip(mustDecimal(t, "100"), mustDecimal(t, "110"), 10); !got.IsZero() {
		t.Fatalf("zero fee config should cost nothing, got %s", got)
	}
}

func TestNetProfitSubtractsRoundTrip(t *testing.T) {
	fees := defaultFeeConfig()
	got := netProfit(fees, mustDecimal(t, "100"), mustDecimal(t, "101"), 10)
	if got.String() != "7.92834" {
		t.Fatalf("net profit = %s, want 7.92834", got)
	}
}

func TestEvaluateStrategyMinProfitIsNetOfFees(t *testing.T) {
	cfg := StockConfig{
		Symbol: "AAPL.US", InitialPrice: mustDecimal(t, "100"), SellPercent: mustDecimal(t, "1"), TrailPercent: mustDecimal(t, "0.5"),
		BuyPercent: mustDecimal(t, "1"), MinProfit: mustDecimal(t, "9"), MaxLots: 3, OrderQuantity: 10,
	}
	input := StrategyInput{
		Config: cfg, CurrentPrice: mustDecimal(t, "101"), PositionQty: 10, AvailableQty: 10, MarketOpen: true,
		State: SymbolState{BuyLadder: []string{"100"}},
	}
	lastBuy := mustDecimal(t, "100")
	d := resolveSellWait(input, 1, true, &lastBuy)
	if d == nil || d.ExpectedProfit.String() != "10" || containsString(d.Reason, "低于最低要求") {
		t.Fatalf("without fees expected gross 10 passing min_profit, got %+v", d)
	}
	input.Fees = defaultFeeConfig()
	d = resolveSellWait(input, 1, true, &lastBuy)
	if d == nil || d.ExpectedProfit.String() != "7.92834" || !containsString(d.Reason, "低于最低要求") {
		t.Fatalf("with fees expected net 7.92834 blocked by min_profit, got %+v", d)
	}
}

func containsString(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
