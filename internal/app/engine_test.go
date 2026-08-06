package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	lbquote "github.com/longbridge/openapi-go/quote"
	lbtrade "github.com/longbridge/openapi-go/trade"
	"github.com/shopspring/decimal"
)

func TestAvailableFundsUsedCash(t *testing.T) {
	funds := availableFunds{
		TotalCash:     mustDecimal(t, "1000"),
		AvailableCash: mustDecimal(t, "250"),
	}

	if got := funds.UsedCash(); !got.Equal(mustDecimal(t, "750")) {
		t.Fatalf("expected used cash 750, got %s", got)
	}
}

func TestAvailableFundsUsedCashPrefersDeployedCash(t *testing.T) {
	funds := availableFunds{
		TotalCash:     mustDecimal(t, "1000"),
		AvailableCash: mustDecimal(t, "997.97"),
		DeployedCash:  mustDecimal(t, "7800"),
	}

	if got := funds.UsedCash(); !got.Equal(mustDecimal(t, "7800")) {
		t.Fatalf("expected deployed cash 7800, got %s", got)
	}
}

func TestStrategyDeployedCashIncludesPositionsAndPendingBuys(t *testing.T) {
	state, err := NewStateStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("new state store: %v", err)
	}
	if err := state.Update("NVDA.US", func(s *SymbolState) {
		s.Pending = &PendingOrderState{
			Side:           ActionBuy,
			SubmittedPrice: "200",
			SubmittedQty:   5,
			SubmittedAt:    time.Now().UTC(),
		}
	}); err != nil {
		t.Fatalf("update state: %v", err)
	}

	engine := &Engine{
		cfg: &Config{
			Stocks: []StockConfig{
				{Symbol: "TSLA.US", Enabled: true},
				{Symbol: "SPCX.US", Enabled: true},
				{Symbol: "NVDA.US", Enabled: true},
				{Symbol: "AMD.US", Enabled: false},
			},
		},
		state: state,
	}
	positions := map[string]PositionSnapshot{
		"TSLA.US": {Quantity: 10, CostPrice: mustDecimal(t, "390"), HasCost: true},
		"SPCX.US": {Quantity: 20, CostPrice: mustDecimal(t, "190"), HasCost: true},
		"AMD.US":  {Quantity: 5, CostPrice: mustDecimal(t, "210"), HasCost: true},
	}

	got := engine.strategyDeployedCash(positions, nil, MarketPhaseNormal)
	want := mustDecimal(t, "8700")
	if !got.Equal(want) {
		t.Fatalf("expected deployed cash %s, got %s", want, got)
	}
}

func TestEnabledSymbolsSkipsDisabledStocks(t *testing.T) {
	engine := &Engine{
		cfg: &Config{
			Stocks: []StockConfig{
				{Symbol: "AAPL.US", Enabled: true},
				{Symbol: "MSFT.US", Enabled: false},
				{Symbol: "NVDA.US", Enabled: true},
			},
		},
	}

	got := engine.enabledSymbols()
	want := []string{"AAPL.US", "NVDA.US"}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, got)
		}
	}
}

func TestApplyGlobalBuyConstraintsStopsBuyWhenCashLimitReached(t *testing.T) {
	decision := Decision{
		Action: ActionBuy,
		Reason: "准备买入",
	}
	funds := availableFunds{
		TotalCash:     mustDecimal(t, "1000"),
		AvailableCash: mustDecimal(t, "200"),
	}

	got := applyGlobalBuyConstraints(decision, funds, nil, mustDecimal(t, "800"), CashLimitModeUsed, decimal.Zero)
	if got.Action != ActionNone {
		t.Fatalf("expected buy to be blocked, got %s", got.Action)
	}
	if !strings.Contains(got.Reason, "已达到全局上限") {
		t.Fatalf("expected limit reason, got %q", got.Reason)
	}
}

func TestLongPortSessionAuthFailureDetection(t *testing.T) {
	msg := "reconnect failed, err: reconnect request: longbridge protocol api error, status:5 code:401 message:token is not exists"
	if !isLongPortSessionAuthFailure(msg) {
		t.Fatalf("expected reconnect 401 to be detected")
	}
	if isLongPortSessionAuthFailure("获取行情失败: temporary network error") {
		t.Fatalf("expected unrelated error to be ignored")
	}
}

func TestLongPortSDKLoggerSignalsSessionAuthFailure(t *testing.T) {
	ch := make(chan struct{}, 1)
	logger := newLongPortSDKLogger(nil, ch)

	logger.Error("reconnect failed, err: reconnect request: longbridge protocol api error, status:5 code:401 message:token is not exists")

	select {
	case <-ch:
	default:
		t.Fatalf("expected session auth failure signal")
	}
}

func TestApplyGlobalBuyConstraintsBlocksBuyOnAccountError(t *testing.T) {
	decision := Decision{
		Action: ActionBuy,
		Reason: "准备买入",
	}

	got := applyGlobalBuyConstraints(decision, availableFunds{}, errors.New("balance unavailable"), mustDecimal(t, "1"), CashLimitModeUsed, decimal.Zero)
	if got.Action != ActionNone {
		t.Fatalf("expected buy to be blocked, got %s", got.Action)
	}
	if !strings.Contains(got.Reason, "无法校验现金使用上限") {
		t.Fatalf("expected account error reason, got %q", got.Reason)
	}
}

func TestApplyGlobalBuyConstraintsProjectedCashLimit(t *testing.T) {
	decision := Decision{
		Action: ActionBuy,
		Reason: "准备买入",
	}
	funds := availableFunds{
		TotalCash:     mustDecimal(t, "1000"),
		AvailableCash: mustDecimal(t, "300"),
	}

	got := applyGlobalBuyConstraints(decision, funds, nil, mustDecimal(t, "800"), CashLimitModeProjected, mustDecimal(t, "120"))
	if got.Action != ActionNone {
		t.Fatalf("expected buy to be blocked, got %s", got.Action)
	}
	if !strings.Contains(got.Reason, "本次买入后") {
		t.Fatalf("expected projected limit reason, got %q", got.Reason)
	}
}

func TestEstimateBuyCapacityStopsWhenCashLimitReached(t *testing.T) {
	stock := StockConfig{
		Symbol:        "AAPL.US",
		MaxLots:       5,
		OrderQuantity: 10,
	}
	position := PositionSnapshot{}
	funds := availableFunds{
		TotalCash:     mustDecimal(t, "1000"),
		AvailableCash: mustDecimal(t, "150"),
	}

	capacity := estimateBuyCapacity(stock, position, mustDecimal(t, "10"), funds, mustDecimal(t, "850"), CashLimitModeUsed)
	remainingLots, reason, bottlenecks, limitTags := capacity.RemainingLots, capacity.Reason, capacity.Bottlenecks, capacity.LimitTags
	if remainingLots != 0 {
		t.Fatalf("expected remaining lots 0, got %d", remainingLots)
	}
	if !strings.Contains(reason, "已用现金") {
		t.Fatalf("expected used cash reason, got %q", reason)
	}
	if len(bottlenecks) == 0 || bottlenecks[0] != "现金使用上限" {
		t.Fatalf("expected cash limit bottleneck, got %#v", bottlenecks)
	}
	if len(limitTags) == 0 || limitTags[0].Label != "现金使用上限" {
		t.Fatalf("expected cash limit tag, got %#v", limitTags)
	}
}

func TestEstimateBuyCapacityProjectedCashLimit(t *testing.T) {
	stock := StockConfig{
		Symbol:        "AAPL.US",
		MaxLots:       5,
		OrderQuantity: 10,
	}
	position := PositionSnapshot{}
	funds := availableFunds{
		TotalCash:     mustDecimal(t, "1000"),
		AvailableCash: mustDecimal(t, "300"),
	}

	capacity := estimateBuyCapacity(stock, position, mustDecimal(t, "10"), funds, mustDecimal(t, "850"), CashLimitModeProjected)
	remainingLots, reason, bottlenecks, limitTags := capacity.RemainingLots, capacity.Reason, capacity.Bottlenecks, capacity.LimitTags
	if remainingLots != 1 {
		t.Fatalf("expected remaining lots 1, got %d", remainingLots)
	}
	if !strings.Contains(reason, "现金上限剩余额度还能支持 1 笔") {
		t.Fatalf("expected projected cash limit reason, got %q", reason)
	}
	if len(bottlenecks) == 0 || bottlenecks[0] != "现金使用上限" {
		t.Fatalf("expected cash limit bottleneck, got %#v", bottlenecks)
	}
	if len(limitTags) == 0 || limitTags[0].Label != "现金使用上限" {
		t.Fatalf("expected cash limit tag, got %#v", limitTags)
	}
}

func TestEstimateBuyCapacityShowsCashAndMarginLots(t *testing.T) {
	stock := StockConfig{
		Symbol:        "AAPL.US",
		UseMargin:     true,
		MaxLots:       5,
		OrderQuantity: 10,
	}
	position := PositionSnapshot{}
	funds := availableFunds{
		AvailableCash:   mustDecimal(t, "150"),
		RemainingMargin: mustDecimal(t, "250"),
	}

	capacity := estimateBuyCapacity(stock, position, mustDecimal(t, "10"), funds, decimal.Zero, CashLimitModeUsed)
	reason, details, detailTags := capacity.Reason, capacity.Details, capacity.DetailTags
	if !strings.Contains(reason, "现金可买 1 笔") {
		t.Fatalf("expected cash lots in reason, got %q", reason)
	}
	if !strings.Contains(reason, "融资可买 2 笔") {
		t.Fatalf("expected margin lots in reason, got %q", reason)
	}
	joinedDetails := strings.Join(details, " | ")
	if !strings.Contains(joinedDetails, "现金可买 1 笔") || !strings.Contains(joinedDetails, "融资可买 2 笔") {
		t.Fatalf("expected cash and margin details, got %q", joinedDetails)
	}
	if len(detailTags) == 0 {
		t.Fatal("expected detail tags")
	}
}

func TestEstimateBuyCapacityClampsNegativeLotsToZero(t *testing.T) {
	stock := StockConfig{
		Symbol:        "NVDA.US",
		UseMargin:     true,
		MaxLots:       10,
		OrderQuantity: 5,
	}
	position := PositionSnapshot{
		Quantity: 40,
	}
	funds := availableFunds{
		AvailableCash:   mustDecimal(t, "-10000"),
		RemainingMargin: mustDecimal(t, "-5000"),
	}

	capacity := estimateBuyCapacity(stock, position, mustDecimal(t, "127.233"), funds, decimal.Zero, CashLimitModeUsed)
	remainingLots, reason, details, detailTags := capacity.RemainingLots, capacity.Reason, capacity.Details, capacity.DetailTags
	if remainingLots != 0 {
		t.Fatalf("expected remaining lots 0, got %d", remainingLots)
	}
	if strings.Contains(reason, "现金可买 -") || strings.Contains(reason, "融资可买 -") {
		t.Fatalf("expected non-negative lots in reason, got %q", reason)
	}
	joinedDetails := strings.Join(details, " | ")
	if strings.Contains(joinedDetails, "现金可买 -") || strings.Contains(joinedDetails, "融资可买 -") {
		t.Fatalf("expected non-negative lots in details, got %q", joinedDetails)
	}
	for _, tag := range detailTags {
		if strings.Contains(tag.Label, "可买 -") {
			t.Fatalf("expected non-negative lots in tags, got %#v", detailTags)
		}
	}
}

func TestEnsureBuyCapacityStopsWhenCashLimitReached(t *testing.T) {
	engine := &Engine{
		cfg: &Config{
			Engine: EngineConfig{
				CashLimit: decimal.RequireFromString("800"),
			},
		},
	}

	err := engine.ensureBuyCapacity(context.Background(), StockConfig{OrderQuantity: 10}, PositionSnapshot{}, mustDecimal(t, "10"), availableFunds{
		TotalCash:     mustDecimal(t, "1000"),
		AvailableCash: mustDecimal(t, "200"),
	}, nil)
	if err == nil {
		t.Fatal("expected cash limit error")
	}
	if !strings.Contains(err.Error(), "已达到全局上限") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildCashLimitSnapshotReached(t *testing.T) {
	snapshot := buildCashLimitSnapshot(mustDecimal(t, "800"), CashLimitModeUsed, availableFunds{
		TotalCash:     mustDecimal(t, "1000"),
		AvailableCash: mustDecimal(t, "200"),
	}, nil)

	if !snapshot.Enabled {
		t.Fatal("expected cash limit enabled")
	}
	if !snapshot.Reached {
		t.Fatal("expected cash limit reached")
	}
	if snapshot.Used != "800.0000" {
		t.Fatalf("expected used cash 800.0000, got %q", snapshot.Used)
	}
	if snapshot.Message != "已触发，停止买入" {
		t.Fatalf("unexpected message: %q", snapshot.Message)
	}
}

func TestBuildCashLimitSnapshotProjectedMode(t *testing.T) {
	snapshot := buildCashLimitSnapshot(mustDecimal(t, "900"), CashLimitModeProjected, availableFunds{
		TotalCash:     mustDecimal(t, "1000"),
		AvailableCash: mustDecimal(t, "200"),
	}, nil)

	if snapshot.Mode != CashLimitModeProjected {
		t.Fatalf("expected projected mode, got %q", snapshot.Mode)
	}
	if !strings.Contains(snapshot.Message, "下一笔买入后") {
		t.Fatalf("unexpected message: %q", snapshot.Message)
	}
}

func TestBuildCashLimitSnapshotDisabled(t *testing.T) {
	snapshot := buildCashLimitSnapshot(decimal.Zero, CashLimitModeUsed, availableFunds{}, nil)

	if snapshot.Enabled {
		t.Fatal("expected cash limit disabled")
	}
	if snapshot.Message != "未启用" {
		t.Fatalf("unexpected message: %q", snapshot.Message)
	}
}

func TestIsManagedActiveOrderRequiresMatchingRemark(t *testing.T) {
	stock := StockConfig{
		Symbol: "AAPL.US",
		Remark: "longbridge-aapl",
	}
	order := &lbtrade.Order{
		Symbol: "AAPL.US",
		Remark: "longbridge-aapl",
		Status: lbtrade.OrderNewStatus,
	}

	if !isManagedActiveOrder(stock, order) {
		t.Fatal("expected order to be recognized as managed active order")
	}

	order.Remark = "manual"
	if isManagedActiveOrder(stock, order) {
		t.Fatal("expected mismatched remark to be rejected")
	}
}

func TestApplyExecutedLotsIgnoresSubLotPartialFill(t *testing.T) {
	state := &SymbolState{
		BuyLadder: []string{"100"},
		BuyArmed:  true,
		BuyLowest: "95",
	}
	pending := &PendingOrderState{
		Side:         ActionBuy,
		SubmittedQty: 10,
	}

	fullLots, partialQty := applyExecutedLots(state, pending, 4, "95")
	if fullLots != 0 || partialQty != 4 {
		t.Fatalf("unexpected lot split: full=%d partial=%d", fullLots, partialQty)
	}
	if len(state.BuyLadder) != 1 || state.BuyLadder[0] != "100" {
		t.Fatalf("expected buy ladder unchanged, got %#v", state.BuyLadder)
	}
	if !state.BuyArmed || state.BuyLowest != "95" {
		t.Fatalf("expected trailing buy state preserved for partial fill, got %+v", state)
	}
}

func TestApplyExecutedLotsClearsTrailingStateAfterFullFill(t *testing.T) {
	state := &SymbolState{
		BuyArmed:    true,
		BuyLowest:   "95",
		SellArmed:   true,
		SellHighest: "105",
	}

	fullLots, partialQty := applyExecutedLots(state, &PendingOrderState{
		Side:         ActionBuy,
		SubmittedQty: 10,
	}, 10, "96")
	if fullLots != 1 || partialQty != 0 {
		t.Fatalf("unexpected buy lot split: full=%d partial=%d", fullLots, partialQty)
	}
	if state.BuyArmed || state.BuyLowest != "" {
		t.Fatalf("expected buy trailing state cleared after buy fill, got %+v", state)
	}

	fullLots, partialQty = applyExecutedLots(state, &PendingOrderState{
		Side:         ActionSell,
		SubmittedQty: 10,
	}, 10, "105")
	if fullLots != 1 || partialQty != 0 {
		t.Fatalf("unexpected sell lot split: full=%d partial=%d", fullLots, partialQty)
	}
	if state.SellArmed || state.SellHighest != "" {
		t.Fatalf("expected sell trailing state cleared after sell fill, got %+v", state)
	}
}

func TestApplyExecutedLotsHandlesMultipleFullLots(t *testing.T) {
	state := &SymbolState{
		BuyLadder: []string{"100", "97", "94"},
	}
	pending := &PendingOrderState{
		Side:         ActionSell,
		SubmittedQty: 5,
	}

	fullLots, partialQty := applyExecutedLots(state, pending, 10, "105")
	if fullLots != 2 || partialQty != 0 {
		t.Fatalf("unexpected lot split: full=%d partial=%d", fullLots, partialQty)
	}
	if len(state.BuyLadder) != 1 || state.BuyLadder[0] != "100" {
		t.Fatalf("expected two buy ladder levels popped, got %#v", state.BuyLadder)
	}
	if len(state.SellAnchors) != 2 {
		t.Fatalf("expected two sell anchors appended, got %#v", state.SellAnchors)
	}
}

func TestBuildExecutionDecisionTextMarksPartialLot(t *testing.T) {
	text := buildExecutionDecisionText(ActionBuy, 95.12, 4, 10, 0, 4)
	if !strings.Contains(text, "不足一笔") {
		t.Fatalf("expected partial lot note, got %q", text)
	}
}

func TestResolveOrderPriceProtectsSellTriggerAfterCentRounding(t *testing.T) {
	decision := Decision{
		Action:       ActionSell,
		TriggerPrice: mustDecimal(t, "189.95154"),
	}

	got := resolveOrderPrice(decision, mustDecimal(t, "189.95"))
	if !got.Equal(mustDecimal(t, "189.96")) {
		t.Fatalf("expected protected sell price 189.96, got %s", got)
	}
}

func TestResolveOrderPriceKeepsSellCurrentPriceWhenAboveTrigger(t *testing.T) {
	decision := Decision{
		Action:       ActionSell,
		TriggerPrice: mustDecimal(t, "189.95154"),
	}

	got := resolveOrderPrice(decision, mustDecimal(t, "190.21"))
	if !got.Equal(mustDecimal(t, "190.21")) {
		t.Fatalf("expected current sell price 190.21, got %s", got)
	}
}

func TestClearTrailingSellState(t *testing.T) {
	state := &SymbolState{
		SellArmed:   true,
		SellHighest: "105",
	}
	if !clearTrailingSellState(state) {
		t.Fatal("expected trailing state to be cleared")
	}
	if state.SellArmed || state.SellHighest != "" {
		t.Fatalf("expected trailing state reset, got %+v", state)
	}
}

func TestClearTrailingBuyState(t *testing.T) {
	state := &SymbolState{
		BuyArmed:  true,
		BuyLowest: "95",
	}
	if !clearTrailingBuyState(state) {
		t.Fatal("expected trailing buy state to be cleared")
	}
	if state.BuyArmed || state.BuyLowest != "" {
		t.Fatalf("expected trailing buy state reset, got %+v", state)
	}
}

func TestUpdateTrailingBuyStateArmsAndTracksLow(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	engine := &Engine{
		state: &StateStore{
			path: statePath,
			state: PersistentState{
				Symbols: map[string]*SymbolState{
					"AAPL.US": {},
				},
			},
		},
	}
	stock := StockConfig{
		Symbol:        "AAPL.US",
		InitialPrice:  mustDecimal(t, "100"),
		BuyPercent:    mustDecimal(t, "1"),
		TrailPercent:  mustDecimal(t, "1"),
		MaxLots:       3,
		OrderQuantity: 10,
	}

	got := engine.updateTrailingBuyState(stock, PositionSnapshot{}, mustDecimal(t, "99.5"), nil, SymbolState{})
	if !got.BuyArmed {
		t.Fatal("expected trailing buy to arm")
	}
	if got.BuyLowest != "99.5" && got.BuyLowest != "99.50" {
		t.Fatalf("expected buy lowest around 99.5, got %q", got.BuyLowest)
	}
}

func TestUpdateTrailingBuyStateClearsStaleLowAboveBaseTarget(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	engine := &Engine{
		state: &StateStore{
			path: statePath,
			state: PersistentState{
				Symbols: map[string]*SymbolState{
					"AAPL.US": {},
				},
			},
		},
	}
	stock := StockConfig{
		Symbol:        "AAPL.US",
		InitialPrice:  mustDecimal(t, "100"),
		BuyPercent:    mustDecimal(t, "10"),
		TrailPercent:  mustDecimal(t, "1"),
		MaxLots:       3,
		OrderQuantity: 10,
	}
	state := SymbolState{
		BuyLadder: []string{"100"},
		BuyArmed:  true,
		BuyLowest: "98",
	}

	got := engine.updateTrailingBuyState(stock, PositionSnapshot{
		Quantity: 10,
	}, mustDecimal(t, "98"), nil, state)
	if got.BuyArmed || got.BuyLowest != "" {
		t.Fatalf("expected stale buy trailing state cleared, got %+v", got)
	}
}

func TestUpdateTrailingSellStateArmsAndTracksHigh(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	engine := &Engine{
		state: &StateStore{
			path: statePath,
			state: PersistentState{
				Symbols: map[string]*SymbolState{
					"AAPL.US": {
						BuyLadder: []string{"100"},
					},
				},
			},
		},
	}
	stock := StockConfig{
		Symbol:        "AAPL.US",
		SellPercent:   mustDecimal(t, "2"),
		TrailPercent:  mustDecimal(t, "1"),
		MinProfit:     mustDecimal(t, "5"),
		OrderQuantity: 10,
	}

	state := SymbolState{
		BuyLadder: []string{"100"},
	}
	got := engine.updateTrailingSellState(stock, PositionSnapshot{Quantity: 10, Available: 10}, mustDecimal(t, "102.80"), new(mustDecimal(t, "100")), state)
	if !got.SellArmed {
		t.Fatal("expected trailing sell to arm")
	}
	if got.SellHighest != "102.8" && got.SellHighest != "102.80" {
		t.Fatalf("expected sell highest around 102.8, got %q", got.SellHighest)
	}
}

func TestReconcileStrategyConfigClearsChangedTrailingState(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	engine := &Engine{
		state: &StateStore{
			path: statePath,
			state: PersistentState{
				Symbols: map[string]*SymbolState{
					"AAPL.US": {
						ConfigSignature: "old",
						BuyLadder:       []string{"100"},
						SellArmed:       true,
						SellHighest:     "105",
					},
				},
			},
		},
	}
	stock := StockConfig{
		Symbol:        "AAPL.US",
		InitialPrice:  mustDecimal(t, "90"),
		SellPercent:   mustDecimal(t, "4.5"),
		TrailPercent:  mustDecimal(t, "1"),
		BuyPercent:    mustDecimal(t, "2"),
		MinProfit:     mustDecimal(t, "0"),
		MaxLots:       5,
		OrderQuantity: 10,
	}

	got, changed := engine.reconcileStrategyConfig(context.Background(), stock, PositionSnapshot{Quantity: 10, Available: 10}, new(mustDecimal(t, "100")), engine.state.Get("AAPL.US"))
	if !changed {
		t.Fatal("expected config change to be reconciled")
	}
	if got.SellArmed || got.SellHighest != "" {
		t.Fatalf("expected trailing sell cleared, got %+v", got)
	}
	if len(got.BuyLadder) != 1 || got.BuyLadder[0] != "100" {
		t.Fatalf("expected buy ladder preserved, got %#v", got.BuyLadder)
	}
	if got.ConfigSignature != strategyConfigSignature(stock) {
		t.Fatalf("expected new config signature, got %q", got.ConfigSignature)
	}
}

func TestReconcileStrategyConfigClearsInvalidLegacyTrailingState(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	engine := &Engine{
		state: &StateStore{
			path: statePath,
			state: PersistentState{
				Symbols: map[string]*SymbolState{
					"TSLA.US": {
						BuyLadder:   []string{"412.24"},
						SellArmed:   true,
						SellHighest: "428.52",
					},
				},
			},
		},
	}
	stock := StockConfig{
		Symbol:        "TSLA.US",
		InitialPrice:  mustDecimal(t, "350"),
		SellPercent:   mustDecimal(t, "4.5"),
		TrailPercent:  mustDecimal(t, "1.5"),
		BuyPercent:    mustDecimal(t, "8"),
		MinProfit:     mustDecimal(t, "10"),
		MaxLots:       5,
		OrderQuantity: 5,
	}

	got, changed := engine.reconcileStrategyConfig(context.Background(), stock, PositionSnapshot{Quantity: 15, Available: 15}, new(mustDecimal(t, "412.24")), engine.state.Get("TSLA.US"))
	if !changed {
		t.Fatal("expected invalid legacy trailing state to be reconciled")
	}
	if got.SellArmed || got.SellHighest != "" {
		t.Fatalf("expected invalid sell trail cleared, got %+v", got)
	}
	if got.ConfigSignature == "" {
		t.Fatal("expected config signature to be written")
	}
}

func TestPendingOrderDecisionTextKeepsCancelDecision(t *testing.T) {
	state := SymbolState{
		LastDecision: "检测到策略配置变化，已撤销旧挂单并重置追踪状态",
		LastOrderMsg: "策略配置变化，已请求撤销旧挂单",
		Pending: &PendingOrderState{
			OrderID: "order-1",
		},
	}

	got := pendingOrderDecisionText(state)
	if got != state.LastDecision {
		t.Fatalf("expected cancel decision to be preserved, got %q", got)
	}
}

func TestPendingOrderDecisionTextDefaultsToWaitingFill(t *testing.T) {
	state := SymbolState{
		LastDecision: "已提交BUY单",
		Pending: &PendingOrderState{
			OrderID: "order-2",
		},
	}

	got := pendingOrderDecisionText(state)
	if got != "等待挂单成交: order-2" {
		t.Fatalf("expected waiting-fill text, got %q", got)
	}
}

func TestDisplayOrderStatus(t *testing.T) {
	if got := displayOrderStatus(string(lbtrade.OrderFilledStatus)); got != "已成交" {
		t.Fatalf("expected filled status label, got %q", got)
	}
	if got := displayOrderStatus("SUBMITTED"); got != "已提交" {
		t.Fatalf("expected submitted label, got %q", got)
	}
}

func TestBuildBuyCapacityError(t *testing.T) {
	got := buildBuyCapacityError(false, 10, 0, 26)
	if !strings.Contains(got, "现金账户最多可买 0 股") {
		t.Fatalf("unexpected cash-only capacity error: %q", got)
	}

	got = buildBuyCapacityError(true, 10, 0, 26)
	if !strings.Contains(got, "融资账户最多可买 26 股") {
		t.Fatalf("unexpected margin capacity error: %q", got)
	}
}

func TestComputeAdaptivePollDelayWhenMarketOpen(t *testing.T) {
	now := time.Date(2026, 4, 15, 14, 0, 0, 0, time.UTC)
	delay := computeAdaptivePollDelay(now, MarketClock{Open: true}, 5*time.Second, false, nil, nil)
	if delay != 5*time.Second {
		t.Fatalf("expected active poll delay, got %s", delay)
	}
}

func TestComputeAdaptivePollDelayWhenMarketClosedSleepsUntilNextWindow(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	nextOpen := time.Date(2026, 4, 15, 9, 30, 0, 0, loc)
	now := nextOpen.Add(-30 * time.Second).UTC()

	delay := computeAdaptivePollDelay(now, MarketClock{Open: false}, 5*time.Second, false, nil, &nextOpen)
	want := nextOpen.Add(-5 * time.Second).Sub(now)
	diff := delay - want
	if diff < 0 {
		diff = -diff
	}
	if diff > time.Second {
		t.Fatalf("expected delay close to next open minus lead, got %s want %s", delay, want)
	}
}

func TestComputeAdaptivePollDelayCapsLongClosedMarketSleep(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	nextOpen := time.Date(2026, 4, 15, 9, 30, 0, 0, loc)
	now := nextOpen.Add(-2 * time.Hour).UTC()

	delay := computeAdaptivePollDelay(now, MarketClock{Open: false}, 5*time.Second, false, nil, &nextOpen)
	if delay != offHoursPollFallback {
		t.Fatalf("expected off-hours delay cap %s, got %s", offHoursPollFallback, delay)
	}
}

func TestComputeAdaptivePollDelayUsesPendingOrderInterval(t *testing.T) {
	now := time.Date(2026, 4, 15, 14, 0, 0, 0, time.UTC)
	delay := computeAdaptivePollDelay(now, MarketClock{Open: false}, 5*time.Second, true, nil, nil)
	if delay != pendingOrderPollInterval {
		t.Fatalf("expected pending order poll delay %s, got %s", pendingOrderPollInterval, delay)
	}
}

func TestComputeAdaptivePollDelayUsesRefreshWindowIfSooner(t *testing.T) {
	now := time.Date(2026, 4, 15, 14, 0, 0, 0, time.UTC)
	refreshDue := now.Add(45 * time.Second)
	delay := computeAdaptivePollDelay(now, MarketClock{Open: false}, 5*time.Second, false, &refreshDue, nil)
	if delay != 45*time.Second {
		t.Fatalf("expected refresh-based delay 45s, got %s", delay)
	}
}

func TestCurrentPriceFromQuotePrefersNewestExtendedSessionPrice(t *testing.T) {
	prevClose := mustDecimal(t, "100")
	regular := mustDecimal(t, "100")
	post := mustDecimal(t, "104.25")

	quote := &lbquote.SecurityQuote{
		LastDone:  &regular,
		PrevClose: &prevClose,
		Timestamp: time.Date(2026, 4, 17, 20, 0, 0, 0, time.UTC).Unix(),
		PostMarketQuote: &lbquote.PrePostQuote{
			LastDone:  &post,
			PrevClose: &prevClose,
			Timestamp: time.Date(2026, 4, 17, 22, 15, 0, 0, time.UTC).Unix(),
		},
	}

	got, ok := currentPriceFromQuote(quote, MarketPhaseClosed)
	if !ok {
		t.Fatal("expected price")
	}
	if !got.Equal(post) {
		t.Fatalf("expected newest post-market price %s, got %s", post, got)
	}
}

func TestQuoteChangeUsesSameLatestPriceSource(t *testing.T) {
	prevClose := mustDecimal(t, "100")
	regular := mustDecimal(t, "100")
	post := mustDecimal(t, "104.25")

	quote := &lbquote.SecurityQuote{
		LastDone:  &regular,
		PrevClose: &prevClose,
		Timestamp: time.Date(2026, 4, 17, 20, 0, 0, 0, time.UTC).Unix(),
		PostMarketQuote: &lbquote.PrePostQuote{
			LastDone:  &post,
			PrevClose: &prevClose,
			Timestamp: time.Date(2026, 4, 17, 22, 15, 0, 0, time.UTC).Unix(),
		},
	}

	change, changePct, trend, ok := quoteChange(quote, MarketPhaseClosed)
	if !ok {
		t.Fatal("expected quote change")
	}
	if !change.Equal(mustDecimal(t, "4.25")) {
		t.Fatalf("expected change 4.25, got %s", change)
	}
	if !changePct.Equal(mustDecimal(t, "4.25")) {
		t.Fatalf("expected change pct 4.25, got %s", changePct)
	}
	if trend != "up" {
		t.Fatalf("expected up trend, got %q", trend)
	}
}
