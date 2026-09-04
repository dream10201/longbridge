package app

import (
	"fmt"
	"strconv"
	"strings"

	lbquote "github.com/longbridge/openapi-go/quote"
	lbtrade "github.com/longbridge/openapi-go/trade"
	"github.com/shopspring/decimal"
)

func (e *Engine) enabledSymbols() []string {
	symbols := make([]string, 0, len(e.cfg.Stocks))
	for _, stock := range e.cfg.Stocks {
		if !stock.Enabled {
			continue
		}
		symbols = append(symbols, stock.Symbol)
	}
	return symbols
}

func aggregatePositions(channels []*lbtrade.StockPositionChannel) map[string]PositionSnapshot {
	result := make(map[string]PositionSnapshot)
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		for _, position := range channel.Positions {
			if position == nil {
				continue
			}
			qty, _ := strconv.ParseInt(position.Quantity, 10, 64)
			available, _ := strconv.ParseInt(position.AvailableQuantity, 10, 64)
			current := result[position.Symbol]
			current.Quantity += qty
			current.Available += available
			if position.CostPrice != nil && qty > 0 {
				notional := position.CostPrice.Mul(decimal.NewFromInt(qty))
				if current.HasCost && current.Quantity-qty > 0 {
					existingNotional := current.CostPrice.Mul(decimal.NewFromInt(current.Quantity - qty))
					current.CostPrice = existingNotional.Add(notional).Div(decimal.NewFromInt(current.Quantity))
				} else {
					current.CostPrice = *position.CostPrice
				}
				current.HasCost = true
			}
			result[position.Symbol] = current
		}
	}
	return result
}

func buildAccountSnapshots(accounts []*lbtrade.AccountBalance) []AccountBalanceSnapshot {
	snapshots := make([]AccountBalanceSnapshot, 0, len(accounts))
	for _, account := range accounts {
		if account == nil {
			continue
		}
		snapshot := AccountBalanceSnapshot{
			Currency:               account.Currency,
			TotalCash:              decimalToString(account.TotalCash, 4),
			MaxFinanceAmount:       decimalToString(account.MaxFinanceAmount, 4),
			RemainingFinanceAmount: decimalToString(account.RemainingFinanceAmount, 4),
			BuyPower:               decimalToString(account.BuyPower, 4),
			NetAssets:              decimalToString(account.NetAssets, 4),
			InitMargin:             decimalToString(account.InitMargin, 4),
			MaintenanceMargin:      decimalToString(account.MaintenanceMargin, 4),
			MarginCall:             decimalToString(account.MarginCall, 4),
			RiskLevel:              account.RiskLevel,
		}
		for _, cash := range account.CashInfos {
			if cash == nil {
				continue
			}
			snapshot.CashInfos = append(snapshot.CashInfos, CashInfoSnapshot{
				Currency:      cash.Currency,
				WithdrawCash:  decimalToString(cash.WithdrawCash, 4),
				AvailableCash: decimalToString(cash.AvailableCash, 4),
				FrozenCash:    decimalToString(cash.FrozenCash, 4),
				SettlingCash:  decimalToString(cash.SettlingCash, 4),
			})
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots
}

func decimalToString(value *decimal.Decimal, fixed int32) string {
	if value == nil {
		return ""
	}
	return value.StringFixed(fixed)
}

type availableFunds struct {
	TotalCash       decimal.Decimal
	AvailableCash   decimal.Decimal
	RemainingMargin decimal.Decimal
	BuyPower        decimal.Decimal
	// Deployed 是策略已投入的总金额(含融资部分):持仓按成本价 × 数量计,买入挂单按冻结金额计,不随股价波动。
	Deployed decimal.Decimal
}

func extractUSDFunds(accounts []*lbtrade.AccountBalance) availableFunds {
	funds := availableFunds{}
	for _, account := range accounts {
		if account == nil || account.Currency != string(lbtrade.CurrencyUSD) {
			continue
		}
		if account.TotalCash != nil {
			funds.TotalCash = *account.TotalCash
		}
		if account.RemainingFinanceAmount != nil {
			funds.RemainingMargin = *account.RemainingFinanceAmount
		}
		if account.BuyPower != nil {
			funds.BuyPower = *account.BuyPower
		}
		for _, cash := range account.CashInfos {
			if cash == nil || cash.Currency != string(lbtrade.CurrencyUSD) {
				continue
			}
			if cash.AvailableCash != nil {
				funds.AvailableCash = *cash.AvailableCash
			}
			break
		}
		// 处理完第一个 USD 账户即返回,避免被后续账户覆盖,也避免该账户无 USD 子项时漏读现金。
		return funds
	}
	return funds
}

// applyBuy 在同一轮内提交买单后同步扣减资金,后续股票按剩余额度校验。
func (f *availableFunds) applyBuy(notional decimal.Decimal) {
	if !notional.IsPositive() {
		return
	}
	f.Deployed = f.Deployed.Add(notional)
	f.BuyPower = f.BuyPower.Sub(notional)
	if f.AvailableCash.GreaterThanOrEqual(notional) {
		f.AvailableCash = f.AvailableCash.Sub(notional)
		return
	}
	f.RemainingMargin = f.RemainingMargin.Sub(notional.Sub(f.AvailableCash))
	f.AvailableCash = decimal.Zero
}

// strategyDeployed 统计策略已投入金额:持仓按成本价 × 数量计,买入挂单按提交价 × 数量计。成本价缺失时退化为当前价估算。
func (e *Engine) strategyDeployed(positions map[string]PositionSnapshot, quotes map[string]*lbquote.SecurityQuote, phase MarketPhase) decimal.Decimal {
	if e == nil || e.cfg == nil {
		return decimal.Zero
	}

	deployed := decimal.Zero
	for _, stock := range e.cfg.Stocks {
		if !stock.Enabled {
			continue
		}

		position := positions[stock.Symbol]
		if position.Quantity > 0 {
			price := position.CostPrice
			ok := position.HasCost
			if !ok {
				price, ok = currentPriceFromQuote(quotes[stock.Symbol], phase)
			}
			if ok && price.IsPositive() {
				deployed = deployed.Add(price.Mul(decimal.NewFromInt(position.Quantity)))
			}
		}

		state := e.state.Get(stock.Symbol)
		if state.Pending != nil && state.Pending.Side == ActionBuy {
			price := decimalStringPtr(state.Pending.SubmittedPrice)
			if price != nil && price.IsPositive() && state.Pending.SubmittedQty > 0 {
				deployed = deployed.Add(price.Mul(decimal.NewFromInt(state.Pending.SubmittedQty)))
			}
		}
	}
	return deployed
}

func buildExposureSnapshot(limit decimal.Decimal, funds availableFunds, accountErr error) ExposureSnapshot {
	if !limit.IsPositive() {
		return ExposureSnapshot{Message: "未启用"}
	}

	snapshot := ExposureSnapshot{
		Enabled: true,
		Limit:   limit.StringFixed(4),
	}
	if accountErr != nil {
		snapshot.Message = "账户资金读取失败，无法校验"
		return snapshot
	}

	remaining := limit.Sub(funds.Deployed)
	if remaining.IsNegative() {
		remaining = decimal.Zero
	}
	snapshot.Used = funds.Deployed.StringFixed(4)
	snapshot.Remaining = remaining.StringFixed(4)
	snapshot.Reached = !funds.Deployed.LessThan(limit)
	if snapshot.Reached {
		snapshot.Message = "已触发，停止买入"
	} else {
		snapshot.Message = "未触发；下一笔买入后超过上限时会停止买入"
	}
	return snapshot
}

// buyCapacity 汇总"下一笔买入还能买几笔"的估算结果及其依据。
type buyCapacity struct {
	RemainingLots int64           // 综合所有约束后还能买的笔数
	CashNeeded    decimal.Decimal // 单笔所需名义资金
	MarginUse     decimal.Decimal // 单笔预计动用的融资额
	Reason        string          // 汇总后的可读说明
	Details       []string        // 各项约束的明细文案
	Bottlenecks   []string        // 当前真正卡住笔数的瓶颈
	DetailTags    []CapacityTag   // 明细对应的结构化标签
	LimitTags     []CapacityTag   // 瓶颈对应的结构化标签
}

// capacityConstraint 是一项买入约束:名称、可支持的笔数及明细文案。
type capacityConstraint struct {
	Name   string
	Kind   string
	Lots   int64
	Detail string
}

func lotsAffordable(amount, orderNotional decimal.Decimal) int64 {
	if !amount.IsPositive() {
		return 0
	}
	return amount.Div(orderNotional).Floor().IntPart()
}

func estimateBuyCapacity(stock StockConfig, position PositionSnapshot, nextBuyPrice decimal.Decimal, funds availableFunds, maxExposure decimal.Decimal) buyCapacity {
	if !nextBuyPrice.IsPositive() || stock.OrderQuantity <= 0 {
		return buyCapacity{Reason: "下一笔价格无效"}
	}

	orderNotional := nextBuyPrice.Mul(decimal.NewFromInt(stock.OrderQuantity))
	if !orderNotional.IsPositive() {
		return buyCapacity{Reason: "下一笔资金需求无效"}
	}

	currentLots := lotsFromQuantity(position.Quantity, stock.OrderQuantity)
	slotRemaining := max(stock.MaxLots-currentLots, 0)
	cashLots := lotsAffordable(funds.AvailableCash, orderNotional)

	constraints := []capacityConstraint{{
		Name:   "最大笔数",
		Kind:   "config",
		Lots:   slotRemaining,
		Detail: fmt.Sprintf("最大笔数剩余 %d 笔", slotRemaining),
	}}
	if maxExposure.IsPositive() {
		constraints = append(constraints, capacityConstraint{
			Name:   "最大投入金额",
			Kind:   "funds",
			Lots:   lotsAffordable(maxExposure.Sub(funds.Deployed), orderNotional),
			Detail: fmt.Sprintf("已投入 %.4f / 上限 %.4f", funds.Deployed.InexactFloat64(), maxExposure.InexactFloat64()),
		})
	}

	marginUse := decimal.Zero
	if stock.UseMargin {
		if funds.AvailableCash.LessThan(orderNotional) {
			marginUse = orderNotional.Sub(decimalMax(funds.AvailableCash, decimal.Zero))
		}
		marginLots := lotsAffordable(funds.RemainingMargin, orderNotional)
		totalLots := lotsAffordable(funds.AvailableCash.Add(funds.RemainingMargin), orderNotional)
		constraints = append(constraints,
			capacityConstraint{Kind: "funds", Lots: cashLots, Detail: fmt.Sprintf("现金可买 %d 笔", cashLots)},
			capacityConstraint{Kind: "margin", Lots: marginLots, Detail: fmt.Sprintf("融资可买 %d 笔", marginLots)},
			capacityConstraint{
				Name:   "现金/融资购买力",
				Kind:   "funds",
				Lots:   totalLots,
				Detail: fmt.Sprintf("现金+融资合计可买 %d 笔 (现金 %.4f + 剩余融资 %.4f)", totalLots, funds.AvailableCash.InexactFloat64(), funds.RemainingMargin.InexactFloat64()),
			},
		)
	} else {
		constraints = append(constraints, capacityConstraint{
			Name:   "现金购买力",
			Kind:   "funds",
			Lots:   cashLots,
			Detail: fmt.Sprintf("现金可买 %d 笔", cashLots),
		})
	}

	remaining := slotRemaining
	details := make([]string, 0, len(constraints))
	detailTags := make([]CapacityTag, 0, len(constraints))
	for _, c := range constraints {
		details = append(details, c.Detail)
		detailTags = append(detailTags, CapacityTag{Label: c.Detail, Kind: c.Kind})
		if c.Name != "" && c.Lots < remaining {
			remaining = c.Lots
		}
	}
	bottlenecks := make([]string, 0, 2)
	limitTags := make([]CapacityTag, 0, 2)
	for _, c := range constraints {
		if c.Name != "" && c.Lots == remaining {
			bottlenecks = append(bottlenecks, c.Name)
			limitTags = append(limitTags, CapacityTag{Label: c.Name, Kind: c.Kind})
		}
	}

	reason := strings.Join(details, "；")
	if len(bottlenecks) > 0 {
		reason += fmt.Sprintf("；当前最终受 %s 限制", strings.Join(bottlenecks, "、"))
	}
	return buyCapacity{
		RemainingLots: remaining,
		CashNeeded:    orderNotional,
		MarginUse:     marginUse,
		Reason:        reason,
		Details:       details,
		Bottlenecks:   bottlenecks,
		DetailTags:    detailTags,
		LimitTags:     limitTags,
	}
}
