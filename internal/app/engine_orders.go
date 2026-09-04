package app

import (
	"context"
	"fmt"

	"strconv"
	"strings"
	"time"

	openapi "github.com/longbridge/openapi-go"
	lbtrade "github.com/longbridge/openapi-go/trade"
	"github.com/shopspring/decimal"
)

func (e *Engine) placeOrder(ctx context.Context, stock StockConfig, currentPrice decimal.Decimal, decision Decision, funds availableFunds, accountErr error) error {
	price := resolveOrderPrice(decision, currentPrice)
	if !price.IsPositive() {
		return fmt.Errorf("订单价格无效")
	}

	if decision.Action == ActionBuy {
		if err := e.ensureBuyCapacity(ctx, stock, price, funds, accountErr); err != nil {
			return err
		}
	}

	if e.cfg.Engine.DryRun {
		e.state.Update(stock.Symbol, func(s *SymbolState) {
			s.LastDecision = fmt.Sprintf("[dry-run] 将以 %.2f 提交%s单，原因: %s", price.InexactFloat64(), strings.ToUpper(string(decision.Action)), decision.Reason)
			s.LastOrderStatus = displayOrderStatus("DRY_RUN")
			s.LastOrderMsg = "未实际提交订单"
			s.LastError = ""
		})
		return nil
	}

	params := &lbtrade.SubmitOrder{
		Symbol:            stock.Symbol,
		OrderType:         lbtrade.OrderTypeLO,
		SubmittedPrice:    price,
		SubmittedQuantity: uint64(stock.OrderQuantity),
		TimeInForce:       lbtrade.TimeTypeDay,
		OutsideRTH:        lbtrade.OutsideRTHAny,
		Remark:            stock.Remark,
	}
	if decision.Action == ActionBuy {
		params.Side = lbtrade.OrderSideBuy
	} else {
		params.Side = lbtrade.OrderSideSell
	}

	orderID, err := e.client.SubmitOrder(ctx, params)
	if err != nil {
		return err
	}

	e.logger.Printf("%s 提交%s单成功: order_id=%s price=%s qty=%d", stock.Symbol, decision.Action, orderID, price.StringFixed(2), stock.OrderQuantity)

	e.state.Update(stock.Symbol, func(s *SymbolState) {
		s.Pending = &PendingOrderState{
			OrderID:        orderID,
			Side:           decision.Action,
			SubmittedPrice: price.StringFixed(2),
			SubmittedQty:   stock.OrderQuantity,
			SubmittedAt:    time.Now().UTC(),
		}
		s.LastDecision = fmt.Sprintf("已提交%s单，价格 %.2f，原因: %s", strings.ToUpper(string(decision.Action)), price.InexactFloat64(), decision.Reason)
		s.LastOrderStatus = displayOrderStatus("SUBMITTED")
		s.LastOrderMsg = ""
		s.LastError = ""
	})
	e.flushState()
	return nil
}

// flushState 在挂单/成交等关键状态变化后立即落盘,避免硬崩溃丢失网格锚点。
func (e *Engine) flushState() {
	if e.state == nil {
		return
	}
	if err := e.state.Flush(); err != nil && e.logger != nil {
		e.logger.Printf("写入状态文件失败: %v", err)
	}
}

func resolveOrderPrice(decision Decision, currentPrice decimal.Decimal) decimal.Decimal {
	price := roundForOrder(decision.Action, currentPrice)
	if decision.Action != ActionSell || !decision.TriggerPrice.IsPositive() {
		return price
	}

	// 卖单触发价可能带有 3 位以上小数；提交订单时至少要守住触发价按分进位后的保护价。
	triggerFloor := roundForOrder(ActionBuy, decision.TriggerPrice)
	if price.LessThan(triggerFloor) {
		return triggerFloor
	}
	return price
}

func (e *Engine) ensureBuyCapacity(ctx context.Context, stock StockConfig, price decimal.Decimal, funds availableFunds, accountErr error) error {
	if e.cfg.Engine.MaxExposure.IsPositive() {
		if accountErr != nil {
			return fmt.Errorf("账户资金读取失败，无法校验最大投入金额: %w", accountErr)
		}
		orderNotional := price.Mul(decimal.NewFromInt(stock.OrderQuantity))
		if blocked, reason := exposureBlockReason(funds, e.cfg.Engine.MaxExposure, orderNotional); blocked {
			return fmt.Errorf("%s", reason)
		}
	}

	resp, err := e.client.EstimateMaxPurchaseQuantity(ctx, &lbtrade.GetEstimateMaxPurchaseQuantity{
		Symbol:    stock.Symbol,
		OrderType: lbtrade.OrderTypeLO,
		Price:     price,
		Currency:  "USD",
		Side:      lbtrade.OrderSideBuy,
	})
	if err != nil {
		return fmt.Errorf("预估最大可买数量失败: %w", err)
	}

	allowed := resp.CashMaxQty
	if stock.UseMargin && resp.MarginMaxQty > allowed {
		allowed = resp.MarginMaxQty
	}
	if int64(allowed) < stock.OrderQuantity {
		return fmt.Errorf("%s", buildBuyCapacityError(stock.UseMargin, stock.OrderQuantity, resp.CashMaxQty, resp.MarginMaxQty))
	}
	return nil
}

func applyGlobalBuyConstraints(decision Decision, funds availableFunds, accountErr error, maxExposure decimal.Decimal, orderNotional decimal.Decimal) Decision {
	if decision.Action != ActionBuy || !maxExposure.IsPositive() {
		return decision
	}
	if accountErr != nil {
		decision.Action = ActionNone
		decision.Reason = "账户资金读取失败，无法校验最大投入金额，已禁止买入"
		return decision
	}

	if blocked, reason := exposureBlockReason(funds, maxExposure, orderNotional); blocked {
		decision.Action = ActionNone
		decision.Reason = reason + "，停止买入"
	}
	return decision
}

// exposureBlockReason 判断本次买入后总投入是否会超过上限。
func exposureBlockReason(funds availableFunds, limit decimal.Decimal, orderNotional decimal.Decimal) (bool, string) {
	deployed := funds.Deployed
	if !deployed.LessThan(limit) {
		return true, fmt.Sprintf("当前已投入 %.4f 已达到最大投入金额 %.4f", deployed.InexactFloat64(), limit.InexactFloat64())
	}
	if !orderNotional.IsPositive() {
		return false, ""
	}
	projected := deployed.Add(orderNotional)
	if projected.GreaterThan(limit) {
		return true, fmt.Sprintf("本次买入后总投入 %.4f 将超过最大投入金额 %.4f", projected.InexactFloat64(), limit.InexactFloat64())
	}
	return false, ""
}

func (e *Engine) loadBrokerPendingOrders(ctx context.Context) (map[string]*lbtrade.Order, error) {
	if e.client == nil {
		return nil, nil
	}

	orders, err := e.client.TodayOrders(ctx, &lbtrade.GetTodayOrders{
		Market: openapi.MarketUS,
		Status: activeOrderStatuses(),
	})
	if err != nil {
		return nil, err
	}

	stockBySymbol := make(map[string]StockConfig, len(e.cfg.Stocks))
	for _, stock := range e.cfg.Stocks {
		stockBySymbol[stock.Symbol] = stock
	}

	result := make(map[string]*lbtrade.Order)
	for _, order := range orders {
		if order == nil {
			continue
		}
		stock, ok := stockBySymbol[order.Symbol]
		if !ok || !isManagedActiveOrder(stock, order) {
			continue
		}
		if existing := result[order.Symbol]; existing == nil || orderSubmittedAtKey(order) > orderSubmittedAtKey(existing) {
			result[order.Symbol] = order
		}
	}
	return result, nil
}

func (e *Engine) recoverPendingOrder(stock StockConfig, order *lbtrade.Order) error {
	if order == nil {
		return nil
	}

	side, ok := actionTypeFromOrderSide(order.Side)
	if !ok {
		return fmt.Errorf("未知订单方向: %s", order.Side)
	}
	submittedQty, err := strconv.ParseInt(order.Quantity, 10, 64)
	if err != nil || submittedQty <= 0 {
		submittedQty = stock.OrderQuantity
	}

	submittedAt := time.Now().UTC()
	if parsed, ok := parseBrokerTime(order.SubmittedAt); ok {
		submittedAt = parsed
	}

	e.state.Update(stock.Symbol, func(s *SymbolState) {
		if s.Pending != nil && s.Pending.OrderID == order.OrderId {
			return
		}
		s.Pending = &PendingOrderState{
			OrderID:        order.OrderId,
			Side:           side,
			SubmittedPrice: decimalToString(order.Price, 2),
			SubmittedQty:   submittedQty,
			SubmittedAt:    submittedAt,
		}
		s.LastOrderStatus = displayOrderStatus(string(order.Status))
		s.LastOrderMsg = order.Msg
		s.LastError = ""
		s.LastDecision = fmt.Sprintf("检测到券商侧仍有活跃挂单，已恢复跟踪: %s", order.OrderId)
	})
	e.flushState()
	return nil
}

func (e *Engine) syncPendingOrder(ctx context.Context, stock StockConfig, state SymbolState) (bool, error) {
	if state.Pending == nil {
		return false, nil
	}

	detail, err := e.client.OrderDetail(ctx, state.Pending.OrderID)
	if err != nil {
		return false, err
	}

	if isActiveOrderStatus(detail.Status) {
		if time.Since(state.Pending.SubmittedAt) > e.cfg.Engine.OrderTimeout {
			if err := e.client.CancelOrder(ctx, state.Pending.OrderID); err != nil {
				return false, fmt.Errorf("撤单失败: %w", err)
			}
			e.logger.Printf("%s 挂单超时，已撤单: %s", stock.Symbol, state.Pending.OrderID)
			e.state.Update(stock.Symbol, func(s *SymbolState) {
				s.LastOrderStatus = displayOrderStatus(string(detail.Status))
				s.LastOrderMsg = "挂单超时，已请求撤单"
				s.LastDecision = "挂单超时，等待撤单结果"
			})
			return true, nil
		}
		e.state.Update(stock.Symbol, func(s *SymbolState) {
			s.LastOrderStatus = displayOrderStatus(string(detail.Status))
			s.LastOrderMsg = detail.Msg
			s.LastError = ""
		})
		return false, nil
	}

	e.state.Update(stock.Symbol, func(s *SymbolState) {
		s.LastOrderStatus = displayOrderStatus(string(detail.Status))
		s.LastOrderMsg = detail.Msg
		s.LastError = ""
		if detail.ExecutedPrice != nil && detail.ExecutedQuantity > 0 {
			price := detail.ExecutedPrice.String()
			now := time.Now().UTC()
			s.LastFilledAt = &now
			s.LastActionAt = &now
			s.LastAction = state.Pending.Side
			clearTrailingBuyState(s)
			clearTrailingSellState(s)
			fullLots, partialQty := applyExecutedLots(s, state.Pending, detail.ExecutedQuantity, price)
			s.LastBuyPrice = lastString(s.BuyLadder)
			s.LastSellPrice = lastString(s.SellAnchors)
			s.LastDecision = buildExecutionDecisionText(state.Pending.Side, detail.ExecutedPrice.InexactFloat64(), detail.ExecutedQuantity, state.Pending.SubmittedQty, fullLots, partialQty)
			if e.logger != nil {
				e.logger.Printf(
					"%s 挂单已终态: order_id=%s side=%s status=%s submitted_price=%s submitted_qty=%d executed_price=%s executed_qty=%d full_lots=%d partial_qty=%d，已清除pending",
					stock.Symbol,
					state.Pending.OrderID,
					state.Pending.Side,
					detail.Status,
					state.Pending.SubmittedPrice,
					state.Pending.SubmittedQty,
					detail.ExecutedPrice.StringFixed(2),
					detail.ExecutedQuantity,
					fullLots,
					partialQty,
				)
			}
		} else {
			s.LastDecision = fmt.Sprintf("订单已结束，状态=%s", detail.Status)
			if e.logger != nil {
				e.logger.Printf(
					"%s 挂单已终态: order_id=%s side=%s status=%s submitted_price=%s submitted_qty=%d executed_price=- executed_qty=0 msg=%q，已清除pending",
					stock.Symbol,
					state.Pending.OrderID,
					state.Pending.Side,
					detail.Status,
					state.Pending.SubmittedPrice,
					state.Pending.SubmittedQty,
					detail.Msg,
				)
			}
		}
		s.Pending = nil
	})
	e.flushState()
	return true, nil
}

func activeOrderStatuses() []lbtrade.OrderStatus {
	return []lbtrade.OrderStatus{
		lbtrade.OrderNotReported,
		lbtrade.OrderReplacedNotReported,
		lbtrade.OrderProtectedNotReported,
		lbtrade.OrderVarietiesNotReported,
		lbtrade.OrderWaitToNew,
		lbtrade.OrderNewStatus,
		lbtrade.OrderWaitToReplace,
		lbtrade.OrderPendingReplaceStatus,
		lbtrade.OrderReplacedStatus,
		lbtrade.OrderPartialFilledStatus,
		lbtrade.OrderWaitToCancel,
		lbtrade.OrderPendingCancelStatus,
	}
}

func isManagedActiveOrder(stock StockConfig, order *lbtrade.Order) bool {
	if order == nil || order.Symbol != stock.Symbol || !isActiveOrderStatus(order.Status) {
		return false
	}
	if strings.TrimSpace(stock.Remark) == "" {
		return false
	}
	return order.Remark == stock.Remark
}

func orderSubmittedAtKey(order *lbtrade.Order) string {
	if order == nil {
		return ""
	}
	return order.SubmittedAt + "|" + order.OrderId
}

func actionTypeFromOrderSide(side lbtrade.OrderSide) (ActionType, bool) {
	switch side {
	case lbtrade.OrderSideBuy:
		return ActionBuy, true
	case lbtrade.OrderSideSell:
		return ActionSell, true
	default:
		return ActionNone, false
	}
}

func parseBrokerTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.000Z07:00",
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if ts, err := time.Parse(layout, raw); err == nil {
			return ts.UTC(), true
		}
	}
	return time.Time{}, false
}

func applyExecutedLots(state *SymbolState, pending *PendingOrderState, executedQty int64, price string) (int64, int64) {
	if state == nil || pending == nil || executedQty <= 0 {
		return 0, 0
	}

	lotQty := pending.SubmittedQty
	if lotQty <= 0 {
		lotQty = executedQty
	}
	fullLots := executedQty / lotQty
	partialQty := executedQty % lotQty

	for range fullLots {
		switch pending.Side {
		case ActionBuy:
			if len(state.SellAnchors) > 0 {
				state.SellAnchors = popLast(state.SellAnchors)
			}
			state.BuyLadder = append(state.BuyLadder, price)
			state.LastBuyPrice = price
			clearTrailingBuyState(state)
		case ActionSell:
			if len(state.BuyLadder) > 0 {
				state.BuyLadder = popLast(state.BuyLadder)
			}
			state.SellAnchors = append(state.SellAnchors, price)
			state.LastSellPrice = price
			clearTrailingSellState(state)
		}
	}
	return fullLots, partialQty
}

func buildExecutionDecisionText(side ActionType, executedPrice float64, executedQty int64, lotQty int64, fullLots int64, partialQty int64) string {
	base := fmt.Sprintf("订单已成交: %s %.2f x %d", strings.ToUpper(string(side)), executedPrice, executedQty)
	if partialQty > 0 {
		return fmt.Sprintf("%s；其中完整策略笔数 %d，剩余 %d 股不足一笔，等待持仓同步", base, fullLots, partialQty)
	}
	if lotQty > 0 && fullLots != 1 {
		return fmt.Sprintf("%s；本次按 %d 笔策略仓位更新", base, fullLots)
	}
	return base
}

func pendingOrderDecisionText(state SymbolState) string {
	if state.Pending == nil {
		return state.LastDecision
	}
	lastDecision := strings.TrimSpace(state.LastDecision)
	lastOrderMsg := strings.TrimSpace(state.LastOrderMsg)
	if isCancelDecisionText(lastDecision) || isCancelDecisionText(lastOrderMsg) {
		if lastDecision != "" {
			return lastDecision
		}
		return fmt.Sprintf("挂单已请求撤单，等待券商确认: %s", state.Pending.OrderID)
	}
	return fmt.Sprintf("等待挂单成交: %s", state.Pending.OrderID)
}

func isCancelDecisionText(text string) bool {
	return strings.Contains(text, "撤单") || strings.Contains(text, "撤销")
}

func displayOrderStatus(status string) string {
	switch status {
	case "DRY_RUN":
		return "演练模式"
	case "SUBMITTED":
		return "已提交"
	case string(lbtrade.OrderNotReported):
		return "未上报"
	case string(lbtrade.OrderReplacedNotReported):
		return "改单未上报"
	case string(lbtrade.OrderProtectedNotReported):
		return "保护单未上报"
	case string(lbtrade.OrderVarietiesNotReported):
		return "条件单未上报"
	case string(lbtrade.OrderWaitToNew):
		return "等待报单"
	case string(lbtrade.OrderNewStatus):
		return "挂单中"
	case string(lbtrade.OrderWaitToReplace):
		return "等待改单"
	case string(lbtrade.OrderPendingReplaceStatus):
		return "改单处理中"
	case string(lbtrade.OrderReplacedStatus):
		return "已改单"
	case string(lbtrade.OrderPartialFilledStatus):
		return "部分成交"
	case string(lbtrade.OrderWaitToCancel):
		return "等待撤单"
	case string(lbtrade.OrderPendingCancelStatus):
		return "撤单处理中"
	case string(lbtrade.OrderFilledStatus):
		return "已成交"
	case string(lbtrade.OrderRejectedStatus):
		return "已拒单"
	case string(lbtrade.OrderCanceledStatus):
		return "已撤单"
	case string(lbtrade.OrderExpiredStatus):
		return "已过期"
	case string(lbtrade.OrderPartialWithdrawal):
		return "部分撤单"
	default:
		if strings.TrimSpace(status) == "" {
			return ""
		}
		return status
	}
}

func buildBuyCapacityError(useMargin bool, orderQuantity int64, cashMaxQty int64, marginMaxQty int64) string {
	if useMargin {
		return fmt.Sprintf("购买力不足：现金账户最多可买 %d 股，融资账户最多可买 %d 股；当前每笔需要 %d 股", cashMaxQty, marginMaxQty, orderQuantity)
	}
	return fmt.Sprintf("现金购买力不足：当前每笔需要 %d 股，现金账户最多可买 %d 股", orderQuantity, cashMaxQty)
}
