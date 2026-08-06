package app

import (
	"fmt"

	"github.com/shopspring/decimal"
)

var decimalHundred = decimal.NewFromInt(100)

type MarketPhase string

const (
	MarketPhaseClosed    MarketPhase = "closed"
	MarketPhasePre       MarketPhase = "pre"
	MarketPhaseNormal    MarketPhase = "normal"
	MarketPhasePost      MarketPhase = "post"
	MarketPhaseOvernight MarketPhase = "overnight"
)

type StrategyInput struct {
	Config       StockConfig
	CurrentPrice decimal.Decimal
	PositionQty  int64
	AvailableQty int64
	CostPrice    *decimal.Decimal
	State        SymbolState
	MarketOpen   bool
}

type Decision struct {
	Action         ActionType
	TriggerPrice   decimal.Decimal
	ReferencePrice decimal.Decimal
	ExpectedProfit decimal.Decimal
	Reason         string
}

type StrategyPreview struct {
	NextBuyPrice      decimal.Decimal
	NextBuyReference  decimal.Decimal
	NextBuyPercent    decimal.Decimal
	HasNextBuy        bool
	NextBuyReason     string
	NextSellPrice     decimal.Decimal
	NextSellReference decimal.Decimal
	NextSellProfit    decimal.Decimal
	HasNextSell       bool
	NextSellReason    string
}

type buyPlan struct {
	BaseTarget decimal.Decimal
	Reference  decimal.Decimal
	BuyPercent decimal.Decimal
	Reason     string
}

func EvaluateStrategy(input StrategyInput) Decision {
	if !input.MarketOpen {
		return Decision{Reason: "当前不在可交易时段"}
	}

	currentLots := lotsFromQuantity(input.PositionQty, input.Config.OrderQuantity)
	maxLots := input.Config.MaxLots
	if maxLots <= 0 {
		return Decision{Reason: "最大持仓笔数配置无效"}
	}

	lastBuy := decimalStringPtr(lastString(input.State.BuyLadder))
	if currentLots > 0 && lastBuy == nil && input.CostPrice != nil {
		costCopy := *input.CostPrice
		lastBuy = &costCopy
	}

	canSellOne := input.AvailableQty >= input.Config.OrderQuantity

	var buyWait *Decision
	if plan, ok := resolveBuyPlan(input); ok {
		lowest, buyArmed := validBuyTrailingLowest(input.State, plan)
		if !buyArmed {
			if input.CurrentPrice.LessThanOrEqual(plan.BaseTarget) {
				buyWait = &Decision{
					TriggerPrice:   plan.BaseTarget,
					ReferencePrice: plan.Reference,
					Reason:         fmt.Sprintf("基础买价 %.2f 已到，等待反弹 %.2f%% 买入", plan.BaseTarget.InexactFloat64(), input.Config.TrailPercent.InexactFloat64()),
				}
			} else {
				buyWait = &Decision{
					TriggerPrice:   plan.BaseTarget,
					ReferencePrice: plan.Reference,
					Reason:         fmt.Sprintf("等待基础买价 %.2f", plan.BaseTarget.InexactFloat64()),
				}
			}
		} else {
			if input.CurrentPrice.LessThan(*lowest) {
				lowest = &input.CurrentPrice
			}
			trailingTarget := calcUpTarget(*lowest, input.Config.TrailPercent)
			target := decimalMin(plan.BaseTarget, trailingTarget)
			if input.CurrentPrice.GreaterThan(*lowest) && input.CurrentPrice.GreaterThanOrEqual(target) {
				return Decision{
					Action:         ActionBuy,
					TriggerPrice:   target,
					ReferencePrice: plan.Reference,
					Reason:         fmt.Sprintf("追踪买入触发，最低 %.2f，反弹到 %.2f 买入", lowest.InexactFloat64(), target.InexactFloat64()),
				}
			}
			buyWait = &Decision{
				TriggerPrice:   target,
				ReferencePrice: plan.Reference,
				Reason:         fmt.Sprintf("追踪买入中，最低 %.2f，反弹到 %.2f 买入", lowest.InexactFloat64(), target.InexactFloat64()),
			}
		}
	}

	// 卖出侧：追踪止盈触发时立即下单返回；否则归集为一条卖出等待文案。
	sellWait := resolveSellWait(input, currentLots, canSellOne, lastBuy)
	if sellWait != nil && sellWait.Action == ActionSell {
		return *sellWait
	}

	// 没有触发实际买卖时统一在此决定优先级：先展示买入等待，其次卖出等待。
	// 引擎本身是“买入优先”的结构（追踪买入会先于卖出逻辑返回），等待文案与之保持一致。
	if buyWait != nil {
		return *buyWait
	}
	if sellWait != nil {
		return *sellWait
	}
	return Decision{Reason: "暂无买入计划"}
}

// resolveSellWait 计算当前持仓下的卖出侧结论：
//   - currentLots == 0 时返回 nil（空仓无卖出场景）；
//   - 追踪止盈触发时返回 Action 为 ActionSell 的决策（调用方应立即下单）；
//   - 其余情况返回 Action 为空的卖出等待文案（含“无法卖出”的原因说明）。
func resolveSellWait(input StrategyInput, currentLots int64, canSellOne bool, lastBuy *decimal.Decimal) *Decision {
	if currentLots == 0 {
		return nil
	}
	if !canSellOne {
		return &Decision{Reason: fmt.Sprintf("可卖数量不足一笔，至少需要 %d 股", input.Config.OrderQuantity)}
	}
	if lastBuy == nil {
		return &Decision{Reason: "缺少买入参考价"}
	}

	baseTarget := calcUpTarget(*lastBuy, input.Config.SellPercent)
	expectedProfit := input.CurrentPrice.Sub(*lastBuy).Mul(decimal.NewFromInt(input.Config.OrderQuantity))
	if expectedProfit.LessThan(input.Config.MinProfit) {
		return &Decision{
			TriggerPrice:   baseTarget,
			ReferencePrice: *lastBuy,
			ExpectedProfit: expectedProfit,
			Reason:         fmt.Sprintf("预估盈利 %.2f 低于最低要求 %.2f，暂不卖出", expectedProfit.InexactFloat64(), input.Config.MinProfit.InexactFloat64()),
		}
	}

	if !input.State.SellArmed {
		reason := fmt.Sprintf("等待基础卖价 %.2f", baseTarget.InexactFloat64())
		if input.CurrentPrice.GreaterThanOrEqual(baseTarget) {
			reason = fmt.Sprintf("基础卖价 %.2f 已到，等待回撤 %.2f%% 卖出", baseTarget.InexactFloat64(), input.Config.TrailPercent.InexactFloat64())
		}
		return &Decision{
			TriggerPrice:   baseTarget,
			ReferencePrice: *lastBuy,
			ExpectedProfit: expectedProfit,
			Reason:         reason,
		}
	}

	highest := decimalStringPtr(input.State.SellHighest)
	if highest == nil || input.CurrentPrice.GreaterThan(*highest) {
		highest = &input.CurrentPrice
	}
	trailingTarget := calcDownTarget(*highest, input.Config.TrailPercent)
	target := decimalMax(baseTarget, trailingTarget)
	if input.CurrentPrice.LessThan(*highest) && input.CurrentPrice.LessThanOrEqual(target) {
		return &Decision{
			Action:         ActionSell,
			TriggerPrice:   target,
			ReferencePrice: *lastBuy,
			ExpectedProfit: expectedProfit,
			Reason:         fmt.Sprintf("追踪止盈触发，最高 %.2f，回撤到 %.2f 卖出", highest.InexactFloat64(), target.InexactFloat64()),
		}
	}
	return &Decision{
		TriggerPrice:   target,
		ReferencePrice: *lastBuy,
		ExpectedProfit: expectedProfit,
		Reason:         fmt.Sprintf("追踪止盈中，最高 %.2f，回撤到 %.2f 卖出", highest.InexactFloat64(), target.InexactFloat64()),
	}
}

func calcUpTarget(base, percent decimal.Decimal) decimal.Decimal {
	return base.Mul(decimal.NewFromInt(1).Add(percent.Div(decimalHundred)))
}

func calcDownTarget(base, percent decimal.Decimal) decimal.Decimal {
	return base.Mul(decimal.NewFromInt(1).Sub(percent.Div(decimalHundred)))
}

func decimalMax(a, b decimal.Decimal) decimal.Decimal {
	if a.GreaterThanOrEqual(b) {
		return a
	}
	return b
}

func decimalMin(a, b decimal.Decimal) decimal.Decimal {
	if a.LessThanOrEqual(b) {
		return a
	}
	return b
}

func roundForOrder(side ActionType, price decimal.Decimal) decimal.Decimal {
	scaled := price.Mul(decimalHundred)
	if side == ActionBuy {
		return scaled.Ceil().Div(decimalHundred)
	}
	return scaled.Floor().Div(decimalHundred)
}

func PreviewStrategy(input StrategyInput) StrategyPreview {
	currentLots := lotsFromQuantity(input.PositionQty, input.Config.OrderQuantity)
	lastBuy := decimalStringPtr(lastString(input.State.BuyLadder))
	if currentLots > 0 && lastBuy == nil && input.CostPrice != nil {
		costCopy := *input.CostPrice
		lastBuy = &costCopy
	}

	preview := StrategyPreview{}
	canSellOne := input.AvailableQty >= input.Config.OrderQuantity

	if plan, ok := resolveBuyPlan(input); ok {
		preview.NextBuyPercent = plan.BuyPercent
		preview.NextBuyReference = plan.Reference
		preview.NextBuyPrice = plan.BaseTarget
		preview.HasNextBuy = true
		if lowest, ok := validBuyTrailingLowest(input.State, plan); ok {
			if input.CurrentPrice.LessThan(*lowest) {
				lowest = &input.CurrentPrice
			}
			trailingTarget := calcUpTarget(*lowest, input.Config.TrailPercent)
			preview.NextBuyPrice = decimalMin(plan.BaseTarget, trailingTarget)
			preview.NextBuyReason = fmt.Sprintf("追踪买入已激活，最低 %.2f，反弹 %.2f%% 触发", lowest.InexactFloat64(), input.Config.TrailPercent.InexactFloat64())
		} else {
			preview.NextBuyReason = fmt.Sprintf("到基础买价 %.2f 后启动追踪买入", plan.BaseTarget.InexactFloat64())
		}
	}

	if canSellOne && lastBuy != nil {
		preview.NextSellReference = *lastBuy
		baseTarget := calcUpTarget(*lastBuy, input.Config.SellPercent)
		preview.NextSellPrice = baseTarget
		preview.NextSellProfit = input.CurrentPrice.Sub(*lastBuy).Mul(decimal.NewFromInt(input.Config.OrderQuantity))
		preview.HasNextSell = true
		if input.State.SellArmed {
			highest := decimalStringPtr(input.State.SellHighest)
			if highest == nil || input.CurrentPrice.GreaterThan(*highest) {
				highest = &input.CurrentPrice
			}
			trailingTarget := calcDownTarget(*highest, input.Config.TrailPercent)
			preview.NextSellPrice = decimalMax(baseTarget, trailingTarget)
			preview.NextSellReason = fmt.Sprintf("追踪止盈已激活，最高 %.2f，回撤 %.2f%% 触发", highest.InexactFloat64(), input.Config.TrailPercent.InexactFloat64())
		} else {
			preview.NextSellReason = fmt.Sprintf("到基础卖价 %.2f 后启动追踪止盈", baseTarget.InexactFloat64())
		}
	}
	return preview
}

func validBuyTrailingLowest(state SymbolState, plan buyPlan) (*decimal.Decimal, bool) {
	if !state.BuyArmed {
		return nil, false
	}
	lowest := decimalStringPtr(state.BuyLowest)
	if lowest == nil || lowest.GreaterThan(plan.BaseTarget) {
		return nil, false
	}
	return lowest, true
}

func resolveBuyPlan(input StrategyInput) (buyPlan, bool) {
	currentLots := lotsFromQuantity(input.PositionQty, input.Config.OrderQuantity)
	if currentLots >= input.Config.MaxLots {
		return buyPlan{}, false
	}

	lastBuy := decimalStringPtr(lastString(input.State.BuyLadder))
	lastSell := decimalStringPtr(lastString(input.State.SellAnchors))
	if currentLots > 0 && lastBuy == nil && input.CostPrice != nil {
		costCopy := *input.CostPrice
		lastBuy = &costCopy
	}

	if currentLots == 0 {
		if input.State.LastAction == ActionSell && lastSell != nil {
			buyPercent := adaptiveBuyPercent(input.Config.BuyPercent, currentLots)
			return buyPlan{
				BaseTarget: calcDownTarget(*lastSell, buyPercent),
				Reference:  *lastSell,
				BuyPercent: buyPercent,
				Reason:     "参考最近一次卖出价回补",
			}, true
		}
		return buyPlan{
			BaseTarget: input.Config.InitialPrice,
			Reference:  input.Config.InitialPrice,
			Reason:     "参考初始建仓价",
		}, true
	}

	buyPercent := adaptiveBuyPercent(input.Config.BuyPercent, currentLots)
	if lastBuy != nil {
		return buyPlan{
			BaseTarget: calcDownTarget(*lastBuy, buyPercent),
			Reference:  *lastBuy,
			BuyPercent: buyPercent,
			Reason:     "参考最近一次买入价补仓",
		}, true
	}
	return buyPlan{}, false
}

func lotsFromQuantity(quantity, orderQuantity int64) int64 {
	if quantity <= 0 || orderQuantity <= 0 {
		return 0
	}
	return (quantity + orderQuantity - 1) / orderQuantity
}

func adaptiveBuyPercent(base decimal.Decimal, currentLots int64) decimal.Decimal {
	if currentLots < 1 {
		currentLots = 1
	}
	numerator := decimal.NewFromInt(fibonacci(int(currentLots) + 2))
	return base.Mul(numerator).Div(decimal.NewFromInt(2))
}

func fibonacci(n int) int64 {
	if n <= 0 {
		return 0
	}
	if n <= 2 {
		return 1
	}
	var prev, curr int64 = 1, 1
	for i := 3; i <= n; i++ {
		prev, curr = curr, prev+curr
	}
	return curr
}
