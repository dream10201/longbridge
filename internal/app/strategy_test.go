package app

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func mustDecimal(t *testing.T, raw string) decimal.Decimal {
	t.Helper()
	value, err := decimal.NewFromString(raw)
	if err != nil {
		t.Fatalf("parse decimal %s: %v", raw, err)
	}
	return value
}

func TestEvaluateStrategyInitialBuy(t *testing.T) {
	cfg := StockConfig{
		Symbol:        "AAPL.US",
		InitialPrice:  mustDecimal(t, "100"),
		SellPercent:   mustDecimal(t, "2"),
		TrailPercent:  mustDecimal(t, "1"),
		BuyPercent:    mustDecimal(t, "1"),
		MinProfit:     mustDecimal(t, "0"),
		MaxLots:       3,
		OrderQuantity: 10,
	}

	decision := EvaluateStrategy(StrategyInput{
		Config:       cfg,
		CurrentPrice: mustDecimal(t, "99.99"),
		MarketOpen:   true,
	})

	if decision.Action != ActionNone {
		t.Fatalf("expected no immediate buy before rebound, got %s", decision.Action)
	}
	if decision.Reason == "" {
		t.Fatal("expected trailing buy reason")
	}
}

func TestEvaluateStrategySellAfterRise(t *testing.T) {
	cfg := StockConfig{
		Symbol:        "AAPL.US",
		InitialPrice:  mustDecimal(t, "100"),
		SellPercent:   mustDecimal(t, "2"),
		TrailPercent:  mustDecimal(t, "1"),
		BuyPercent:    mustDecimal(t, "1"),
		MinProfit:     mustDecimal(t, "5"),
		MaxLots:       3,
		OrderQuantity: 10,
	}

	decision := EvaluateStrategy(StrategyInput{
		Config:       cfg,
		CurrentPrice: mustDecimal(t, "102.80"),
		PositionQty:  10,
		AvailableQty: 10,
		CostPrice:    new(mustDecimal(t, "100")),
		State: SymbolState{
			LastAction:   ActionBuy,
			LastBuyPrice: "100",
		},
		MarketOpen: true,
	})

	if decision.Action != ActionNone {
		t.Fatalf("expected no immediate sell before pullback, got %s", decision.Action)
	}
	if decision.Reason == "" {
		t.Fatal("expected trailing sell reason")
	}
}

func TestEvaluateStrategyBuyBackAfterSell(t *testing.T) {
	cfg := StockConfig{
		Symbol:        "AAPL.US",
		InitialPrice:  mustDecimal(t, "100"),
		SellPercent:   mustDecimal(t, "2"),
		TrailPercent:  mustDecimal(t, "1"),
		BuyPercent:    mustDecimal(t, "1"),
		MinProfit:     mustDecimal(t, "0"),
		MaxLots:       3,
		OrderQuantity: 10,
	}

	decision := EvaluateStrategy(StrategyInput{
		Config:       cfg,
		CurrentPrice: mustDecimal(t, "99"),
		State: SymbolState{
			LastAction:    ActionSell,
			LastSellPrice: "100",
			SellAnchors:   []string{"100"},
		},
		MarketOpen: true,
	})

	if decision.Action != ActionNone {
		t.Fatalf("expected no immediate buy back before rebound, got %s", decision.Action)
	}
}

func TestEvaluateStrategyAddOnBuyUsesAdaptivePercent(t *testing.T) {
	cfg := StockConfig{
		Symbol:        "AAPL.US",
		InitialPrice:  mustDecimal(t, "100"),
		SellPercent:   mustDecimal(t, "2"),
		TrailPercent:  mustDecimal(t, "1"),
		BuyPercent:    mustDecimal(t, "2"),
		MinProfit:     mustDecimal(t, "0"),
		MaxLots:       4,
		OrderQuantity: 10,
	}

	decision := EvaluateStrategy(StrategyInput{
		Config:       cfg,
		CurrentPrice: mustDecimal(t, "96.90"),
		PositionQty:  20,
		AvailableQty: 20,
		State: SymbolState{
			LastAction:   ActionBuy,
			LastBuyPrice: "100",
			BuyLadder:    []string{"102", "100"},
		},
		MarketOpen: true,
	})

	if decision.Action != ActionNone {
		t.Fatalf("expected no immediate adaptive buy before rebound, got %s", decision.Action)
	}
}

func TestEvaluateStrategySellReducesNextBuyStep(t *testing.T) {
	cfg := StockConfig{
		Symbol:        "AAPL.US",
		InitialPrice:  mustDecimal(t, "100"),
		SellPercent:   mustDecimal(t, "2"),
		TrailPercent:  mustDecimal(t, "1"),
		BuyPercent:    mustDecimal(t, "2"),
		MinProfit:     mustDecimal(t, "0"),
		MaxLots:       4,
		OrderQuantity: 10,
	}

	decision := EvaluateStrategy(StrategyInput{
		Config:       cfg,
		CurrentPrice: mustDecimal(t, "106.70"),
		PositionQty:  20,
		AvailableQty: 20,
		State: SymbolState{
			LastAction:    ActionSell,
			LastSellPrice: "110",
			BuyLadder:     []string{"100", "97"},
			SellAnchors:   []string{"110"},
		},
		MarketOpen: true,
	})

	if decision.Action != ActionNone {
		t.Fatalf("expected no immediate reduced-step buy before rebound, got %s", decision.Action)
	}
}

func TestEvaluateStrategyHoldingAddOnIgnoresSellAnchor(t *testing.T) {
	cfg := StockConfig{
		Symbol:        "AAPL.US",
		InitialPrice:  mustDecimal(t, "100"),
		SellPercent:   mustDecimal(t, "20"),
		TrailPercent:  mustDecimal(t, "1"),
		BuyPercent:    mustDecimal(t, "2"),
		MinProfit:     mustDecimal(t, "0"),
		MaxLots:       4,
		OrderQuantity: 10,
	}

	decision := EvaluateStrategy(StrategyInput{
		Config:       cfg,
		CurrentPrice: mustDecimal(t, "106.70"),
		PositionQty:  20,
		AvailableQty: 0,
		State: SymbolState{
			LastAction:    ActionSell,
			LastSellPrice: "110",
			BuyLadder:     []string{"100", "97"},
			SellAnchors:   []string{"110"},
		},
		MarketOpen: true,
	})

	if decision.Action == ActionBuy {
		t.Fatal("expected holding add-on buy to ignore sell anchor")
	}
	if !decision.ReferencePrice.Equal(mustDecimal(t, "97")) {
		t.Fatalf("expected buy reference to use last buy 97, got %s", decision.ReferencePrice)
	}
}

func TestEvaluateStrategyBuyOnTrailingRebound(t *testing.T) {
	cfg := StockConfig{
		Symbol:        "AAPL.US",
		InitialPrice:  mustDecimal(t, "100"),
		SellPercent:   mustDecimal(t, "2"),
		TrailPercent:  mustDecimal(t, "1"),
		BuyPercent:    mustDecimal(t, "1"),
		MinProfit:     mustDecimal(t, "0"),
		MaxLots:       3,
		OrderQuantity: 10,
	}

	decision := EvaluateStrategy(StrategyInput{
		Config:       cfg,
		CurrentPrice: mustDecimal(t, "95.95"),
		State: SymbolState{
			LastAction: ActionNone,
			BuyArmed:   true,
			BuyLowest:  "95",
		},
		MarketOpen: true,
	})

	if decision.Action != ActionBuy {
		t.Fatalf("expected buy on trailing rebound, got %s", decision.Action)
	}
}

func TestEvaluateStrategyDoesNotBuyImmediatelyWhenJustArmedAtBaseTarget(t *testing.T) {
	cfg := StockConfig{
		Symbol:        "AAPL.US",
		InitialPrice:  mustDecimal(t, "100"),
		SellPercent:   mustDecimal(t, "2"),
		TrailPercent:  mustDecimal(t, "1"),
		BuyPercent:    mustDecimal(t, "1"),
		MinProfit:     mustDecimal(t, "0"),
		MaxLots:       3,
		OrderQuantity: 10,
	}

	decision := EvaluateStrategy(StrategyInput{
		Config:       cfg,
		CurrentPrice: mustDecimal(t, "100"),
		State: SymbolState{
			BuyArmed:  true,
			BuyLowest: "100",
		},
		MarketOpen: true,
	})

	if decision.Action == ActionBuy {
		t.Fatalf("expected no buy without actual rebound, got %s", decision.Action)
	}
}

func TestEvaluateStrategyIgnoresStaleBuyTrailingLowestAboveBaseTarget(t *testing.T) {
	cfg := StockConfig{
		Symbol:        "AAPL.US",
		InitialPrice:  mustDecimal(t, "100"),
		SellPercent:   mustDecimal(t, "2"),
		TrailPercent:  mustDecimal(t, "1"),
		BuyPercent:    mustDecimal(t, "10"),
		MinProfit:     mustDecimal(t, "0"),
		MaxLots:       3,
		OrderQuantity: 10,
	}

	decision := EvaluateStrategy(StrategyInput{
		Config:       cfg,
		CurrentPrice: mustDecimal(t, "98"),
		PositionQty:  10,
		AvailableQty: 0,
		State: SymbolState{
			BuyLadder: []string{"100"},
			BuyArmed:  true,
			BuyLowest: "98",
		},
		MarketOpen: true,
	})

	if decision.Action == ActionBuy {
		t.Fatal("expected stale buy trailing state to be ignored")
	}
	if !decision.TriggerPrice.Equal(mustDecimal(t, "90")) {
		t.Fatalf("expected base target 90, got %s", decision.TriggerPrice)
	}
}

func TestEvaluateStrategySellOnTrailingPullback(t *testing.T) {
	cfg := StockConfig{
		Symbol:        "AAPL.US",
		InitialPrice:  mustDecimal(t, "100"),
		SellPercent:   mustDecimal(t, "2"),
		TrailPercent:  mustDecimal(t, "1"),
		BuyPercent:    mustDecimal(t, "1"),
		MinProfit:     mustDecimal(t, "5"),
		MaxLots:       3,
		OrderQuantity: 10,
	}

	decision := EvaluateStrategy(StrategyInput{
		Config:       cfg,
		CurrentPrice: mustDecimal(t, "103.95"),
		PositionQty:  10,
		AvailableQty: 10,
		CostPrice:    new(mustDecimal(t, "100")),
		State: SymbolState{
			LastAction:   ActionBuy,
			LastBuyPrice: "100",
			BuyLadder:    []string{"100"},
			SellArmed:    true,
			SellHighest:  "105",
		},
		MarketOpen: true,
	})

	if decision.Action != ActionSell {
		t.Fatalf("expected sell on trailing pullback, got %s", decision.Action)
	}
}

func TestEvaluateStrategyDoesNotSellImmediatelyWhenJustArmedAtBaseTarget(t *testing.T) {
	cfg := StockConfig{
		Symbol:        "AAPL.US",
		InitialPrice:  mustDecimal(t, "100"),
		SellPercent:   mustDecimal(t, "2"),
		TrailPercent:  mustDecimal(t, "1"),
		BuyPercent:    mustDecimal(t, "1"),
		MinProfit:     mustDecimal(t, "0"),
		MaxLots:       3,
		OrderQuantity: 10,
	}

	decision := EvaluateStrategy(StrategyInput{
		Config:       cfg,
		CurrentPrice: mustDecimal(t, "102"),
		PositionQty:  10,
		AvailableQty: 10,
		CostPrice:    new(mustDecimal(t, "100")),
		State: SymbolState{
			LastAction:   ActionBuy,
			LastBuyPrice: "100",
			BuyLadder:    []string{"100"},
			SellArmed:    true,
			SellHighest:  "102",
		},
		MarketOpen: true,
	})

	if decision.Action == ActionSell {
		t.Fatalf("expected no sell without actual pullback, got %s", decision.Action)
	}
}

func TestEvaluateStrategySkipsBuyWhenPriceGapsAboveTrigger(t *testing.T) {
	input := StrategyInput{
		Config: StockConfig{
			Symbol: "TSLA.US", InitialPrice: mustDecimal(t, "350"), SellPercent: mustDecimal(t, "3.8"),
			TrailPercent: mustDecimal(t, "1.8"), BuyPercent: mustDecimal(t, "7"), MinProfit: mustDecimal(t, "30"),
			MaxLots: 4, OrderQuantity: 10,
		},
		CurrentPrice: mustDecimal(t, "380"),
		State:        SymbolState{BuyArmed: true, BuyLowest: "345"},
		MarketOpen:   true,
	}
	got := EvaluateStrategy(input)
	if got.Action != ActionNone {
		t.Fatalf("expected no buy when price gapped far above trigger, got %+v", got)
	}
	if !strings.Contains(got.Reason, "放弃本次买入") {
		t.Fatalf("unexpected reason %q", got.Reason)
	}

	input.CurrentPrice = mustDecimal(t, "356")
	if got := EvaluateStrategy(input); got.Action != ActionBuy {
		t.Fatalf("expected buy within chase limit, got %+v", got)
	}
}

func TestEvaluateStrategySkipsSellWhenPriceGapsBelowTrigger(t *testing.T) {
	input := StrategyInput{
		Config: StockConfig{
			Symbol: "TSLA.US", InitialPrice: mustDecimal(t, "350"), SellPercent: mustDecimal(t, "3.8"),
			TrailPercent: mustDecimal(t, "1.8"), BuyPercent: mustDecimal(t, "7"), MinProfit: mustDecimal(t, "30"),
			MaxLots: 1, OrderQuantity: 10,
		},
		CurrentPrice: mustDecimal(t, "355"),
		PositionQty:  10,
		AvailableQty: 10,
		State:        SymbolState{BuyLadder: []string{"352"}, SellArmed: true, SellHighest: "370"},
		MarketOpen:   true,
	}
	got := EvaluateStrategy(input)
	if got.Action != ActionNone {
		t.Fatalf("expected no sell when price gapped far below trigger, got %+v", got)
	}
	if !strings.Contains(got.Reason, "放弃本次卖出") {
		t.Fatalf("unexpected reason %q", got.Reason)
	}

	input.CurrentPrice = mustDecimal(t, "363.3")
	if got := EvaluateStrategy(input); got.Action != ActionSell {
		t.Fatalf("expected sell within gap floor, got %+v", got)
	}
}
