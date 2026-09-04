package app

import (
	"context"
	"fmt"
	"github.com/shopspring/decimal"
	"strconv"
	"strings"
)

func (e *Engine) normalizeStateWithPosition(stock StockConfig, position PositionSnapshot, state SymbolState) SymbolState {
	currentLots := lotsFromQuantity(position.Quantity, stock.OrderQuantity)
	changed := false

	if currentLots == 0 {
		if len(state.BuyLadder) > 0 {
			state.BuyLadder = nil
			state.LastBuyPrice = ""
			changed = true
			if clearTrailingBuyState(&state) {
				changed = true
			}
		}
		if clearTrailingSellState(&state) {
			changed = true
		}
	} else if int64(len(state.BuyLadder)) != currentLots {
		rebuildPrice := ""
		if position.HasCost {
			rebuildPrice = position.CostPrice.String()
		} else if state.LastBuyPrice != "" {
			rebuildPrice = state.LastBuyPrice
		}
		if rebuildPrice != "" {
			state.BuyLadder = make([]string, currentLots)
			for i := range currentLots {
				state.BuyLadder[i] = rebuildPrice
			}
			state.LastBuyPrice = rebuildPrice
		} else {
			state.BuyLadder = nil
			state.LastBuyPrice = ""
		}
		if len(state.SellAnchors) > 0 {
			state.SellAnchors = nil
			state.LastSellPrice = ""
		}
		if clearTrailingBuyState(&state) {
			changed = true
		}
		if clearTrailingSellState(&state) {
			changed = true
		}
		state.LastDecision = fmt.Sprintf("检测到持仓与本地网格状态不一致，已按当前持仓 %d 笔重建买入阶梯", currentLots)
		changed = true
	}

	if len(state.BuyLadder) > 0 {
		last := lastString(state.BuyLadder)
		if state.LastBuyPrice != last {
			state.LastBuyPrice = last
			changed = true
		}
	}
	if len(state.BuyLadder) == 0 && state.LastBuyPrice != "" && currentLots == 0 {
		state.LastBuyPrice = ""
		changed = true
	}

	lastSell := lastString(state.SellAnchors)
	if state.LastSellPrice != lastSell {
		state.LastSellPrice = lastSell
		changed = true
	}

	if changed {
		e.state.Update(stock.Symbol, func(s *SymbolState) {
			*s = state
		})
		return e.state.Get(stock.Symbol)
	}
	return state
}

func (e *Engine) reconcileStrategyConfig(ctx context.Context, stock StockConfig, position PositionSnapshot, costPrice *decimal.Decimal, state SymbolState) (SymbolState, bool) {
	signature := strategyConfigSignature(stock)
	if signature == "" {
		return state, false
	}

	signatureChanged := state.ConfigSignature != "" && state.ConfigSignature != signature
	needsInvalidClear := state.ConfigSignature == "" && trailingStateInvalidForConfig(stock, position, costPrice, state)
	if !signatureChanged && !needsInvalidClear {
		if state.ConfigSignature == signature {
			return state, false
		}
		e.state.Update(stock.Symbol, func(s *SymbolState) {
			s.ConfigSignature = signature
		})
		return e.state.Get(stock.Symbol), false
	}

	reason := "检测到策略配置变化，已重置追踪状态"
	if needsInvalidClear && !signatureChanged {
		reason = "检测到追踪状态与当前策略配置不一致，已重置追踪状态"
	}

	if state.Pending != nil {
		if err := e.client.CancelOrder(ctx, state.Pending.OrderID); err != nil {
			e.state.Update(stock.Symbol, func(s *SymbolState) {
				s.ConfigSignature = signature
				s.LastError = fmt.Sprintf("策略配置变化后撤销旧挂单失败: %v", err)
				s.LastDecision = "检测到策略配置变化，但旧挂单撤销失败，暂停本轮下单"
			})
			return e.state.Get(stock.Symbol), true
		}
		e.logger.Printf("%s 策略配置变化，已撤销旧挂单: %s", stock.Symbol, state.Pending.OrderID)
		e.state.Update(stock.Symbol, func(s *SymbolState) {
			s.ConfigSignature = signature
			clearTrailingBuyState(s)
			clearTrailingSellState(s)
			s.LastOrderMsg = "策略配置变化，已请求撤销旧挂单"
			s.LastDecision = "检测到策略配置变化，已撤销旧挂单并重置追踪状态"
			s.LastError = ""
		})
		return e.state.Get(stock.Symbol), true
	}

	e.state.Update(stock.Symbol, func(s *SymbolState) {
		s.ConfigSignature = signature
		clearTrailingBuyState(s)
		clearTrailingSellState(s)
		s.LastDecision = reason
		s.LastError = ""
	})
	return e.state.Get(stock.Symbol), true
}

func strategyConfigSignature(stock StockConfig) string {
	return strings.Join([]string{
		"v1",
		stock.InitialPrice.String(),
		stock.SellPercent.String(),
		stock.TrailPercent.String(),
		stock.BuyPercent.String(),
		stock.MinProfit.String(),
		strconv.FormatInt(stock.MaxLots, 10),
		strconv.FormatInt(stock.OrderQuantity, 10),
	}, "|")
}

func trailingStateInvalidForConfig(stock StockConfig, position PositionSnapshot, costPrice *decimal.Decimal, state SymbolState) bool {
	if state.BuyArmed {
		plan, ok := resolveBuyPlan(StrategyInput{
			Config:       stock,
			PositionQty:  position.Quantity,
			AvailableQty: position.Available,
			CostPrice:    costPrice,
			State:        state,
			MarketOpen:   true,
		})
		lowest := decimalStringPtr(state.BuyLowest)
		if !ok || lowest == nil || lowest.GreaterThan(plan.BaseTarget) {
			return true
		}
	}

	if state.SellArmed {
		lastBuy := decimalStringPtr(lastString(state.BuyLadder))
		if lastBuy == nil && costPrice != nil {
			costCopy := *costPrice
			lastBuy = &costCopy
		}
		highest := decimalStringPtr(state.SellHighest)
		if lastBuy == nil || highest == nil || position.Quantity <= 0 {
			return true
		}
		baseTarget := calcUpTarget(*lastBuy, stock.SellPercent)
		expectedProfitAtHigh := highest.Sub(*lastBuy).Mul(decimal.NewFromInt(stock.OrderQuantity))
		if highest.LessThan(baseTarget) || expectedProfitAtHigh.LessThan(stock.MinProfit) {
			return true
		}
	}

	return false
}

func (e *Engine) updateTrailingBuyState(stock StockConfig, position PositionSnapshot, currentPrice decimal.Decimal, costPrice *decimal.Decimal, state SymbolState) SymbolState {
	if !currentPrice.IsPositive() {
		return state
	}

	plan, ok := resolveBuyPlan(StrategyInput{
		Config:       stock,
		CurrentPrice: currentPrice,
		PositionQty:  position.Quantity,
		AvailableQty: position.Available,
		CostPrice:    costPrice,
		State:        state,
		MarketOpen:   true,
	})
	if !ok {
		if !clearTrailingBuyState(&state) {
			return state
		}
		e.state.Update(stock.Symbol, func(s *SymbolState) {
			*s = state
		})
		return e.state.Get(stock.Symbol)
	}

	changed := false
	if state.BuyArmed {
		lowest := decimalStringPtr(state.BuyLowest)
		if lowest == nil || lowest.GreaterThan(plan.BaseTarget) {
			if clearTrailingBuyState(&state) {
				changed = true
			}
		}
	}
	if currentPrice.LessThanOrEqual(plan.BaseTarget) {
		if !state.BuyArmed {
			state.BuyArmed = true
			changed = true
		}
		lowest := decimalStringPtr(state.BuyLowest)
		if lowest == nil || currentPrice.LessThan(*lowest) {
			state.BuyLowest = currentPrice.String()
			changed = true
		}
	}
	if !changed {
		return state
	}
	e.state.Update(stock.Symbol, func(s *SymbolState) {
		*s = state
	})
	return e.state.Get(stock.Symbol)
}

func (e *Engine) updateTrailingSellState(stock StockConfig, position PositionSnapshot, currentPrice decimal.Decimal, costPrice *decimal.Decimal, state SymbolState) SymbolState {
	if !currentPrice.IsPositive() {
		return state
	}

	lastBuy := decimalStringPtr(lastString(state.BuyLadder))
	if lastBuy == nil && costPrice != nil {
		costCopy := *costPrice
		lastBuy = &costCopy
	}
	if lastBuy == nil || position.Quantity <= 0 {
		if !clearTrailingSellState(&state) {
			return state
		}
		e.state.Update(stock.Symbol, func(s *SymbolState) {
			*s = state
		})
		return e.state.Get(stock.Symbol)
	}

	baseTarget := calcUpTarget(*lastBuy, stock.SellPercent)
	expectedProfit := currentPrice.Sub(*lastBuy).Mul(decimal.NewFromInt(stock.OrderQuantity))
	changed := false
	if currentPrice.GreaterThanOrEqual(baseTarget) && !expectedProfit.LessThan(stock.MinProfit) {
		if !state.SellArmed {
			state.SellArmed = true
			changed = true
		}
		highest := decimalStringPtr(state.SellHighest)
		if highest == nil || currentPrice.GreaterThan(*highest) {
			state.SellHighest = currentPrice.String()
			changed = true
		}
	}
	if !changed {
		return state
	}
	e.state.Update(stock.Symbol, func(s *SymbolState) {
		*s = state
	})
	return e.state.Get(stock.Symbol)
}

func clearTrailingSellState(state *SymbolState) bool {
	if state == nil {
		return false
	}
	changed := state.SellArmed || state.SellHighest != ""
	state.SellArmed = false
	state.SellHighest = ""
	return changed
}

func clearTrailingBuyState(state *SymbolState) bool {
	if state == nil {
		return false
	}
	changed := state.BuyArmed || state.BuyLowest != ""
	state.BuyArmed = false
	state.BuyLowest = ""
	return changed
}

func (e *Engine) hasPendingOrders() bool {
	for _, stock := range e.cfg.Stocks {
		if e.state.Get(stock.Symbol).Pending != nil {
			return true
		}
	}
	return false
}
