package app

import (
	"fmt"

	lbquote "github.com/longbridge/openapi-go/quote"
	lbtrade "github.com/longbridge/openapi-go/trade"
	"github.com/shopspring/decimal"

	"strconv"
	"strings"
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
	// DeployedCash 是策略实际已经花出去的现金:在持仓上按建仓成本(成本价 × 数量)计,
	// 在挂单上按买单冻结的金额计。它衡量"真金白银已投入多少",不随股价波动。
	DeployedCash decimal.Decimal
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

// UsedCash 返回策略已用现金:优先用按成本价计的实际投入(DeployedCash),
// 仅在没有持仓/挂单数据时退化为账户层面的"总现金 - 可用现金"。
func (f availableFunds) UsedCash() decimal.Decimal {
	if f.DeployedCash.IsPositive() {
		return f.DeployedCash
	}
	usedCash := f.TotalCash.Sub(f.AvailableCash)
	if usedCash.IsNegative() {
		return decimal.Zero
	}
	return usedCash
}

// strategyDeployedCash 统计策略已投入的现金:持仓按建仓成本(成本价 × 数量)计,
// 买入挂单按冻结金额(提交价 × 数量)计。成本价缺失时退化为当前价估算,避免漏算。
func (e *Engine) strategyDeployedCash(positions map[string]PositionSnapshot, quotes map[string]*lbquote.SecurityQuote, phase MarketPhase) decimal.Decimal {
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

func buildCashLimitSnapshot(limit decimal.Decimal, mode string, funds availableFunds, accountErr error) CashLimitSnapshot {
	if !limit.IsPositive() {
		return CashLimitSnapshot{
			Enabled: false,
			Message: "未启用",
		}
	}

	mode = normalizeCashLimitMode(mode)
	snapshot := CashLimitSnapshot{
		Enabled: true,
		Mode:    mode,
		Limit:   limit.StringFixed(4),
	}
	if accountErr != nil {
		snapshot.Message = "账户资金读取失败，无法校验"
		return snapshot
	}

	usedCash := funds.UsedCash()
	remaining := limit.Sub(usedCash)
	if remaining.IsNegative() {
		remaining = decimal.Zero
	}
	snapshot.Used = usedCash.StringFixed(4)
	snapshot.Remaining = remaining.StringFixed(4)
	snapshot.Reached = !usedCash.LessThan(limit)
	if snapshot.Reached {
		snapshot.Message = "已触发，停止买入"
	} else if mode == CashLimitModeProjected {
		snapshot.Message = "未触发；下一笔买入后超过上限时会停止买入"
	} else {
		snapshot.Message = "未触发"
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

func estimateBuyCapacity(stock StockConfig, position PositionSnapshot, nextBuyPrice decimal.Decimal, funds availableFunds, cashLimit decimal.Decimal, cashLimitMode string) buyCapacity {
	if !nextBuyPrice.IsPositive() || stock.OrderQuantity <= 0 {
		return buyCapacity{Reason: "下一笔价格无效"}
	}

	orderNotional := nextBuyPrice.Mul(decimal.NewFromInt(stock.OrderQuantity))
	if !orderNotional.IsPositive() {
		return buyCapacity{Reason: "下一笔资金需求无效"}
	}

	currentLots := lotsFromQuantity(position.Quantity, stock.OrderQuantity)
	slotRemaining := max(stock.MaxLots-currentLots, 0)
	usedCash := funds.UsedCash()

	marginUse := decimal.Zero
	if stock.UseMargin && funds.AvailableCash.LessThan(orderNotional) {
		marginUse = orderNotional.Sub(funds.AvailableCash)
		if marginUse.IsNegative() {
			marginUse = decimal.Zero
		}
	}

	cashLimitedLots := funds.AvailableCash.Div(orderNotional).Floor().IntPart()
	marginLimitedLotsByFunds := int64(0)
	totalFundsLimitedLots := cashLimitedLots
	if stock.UseMargin {
		marginLimitedLotsByFunds = funds.RemainingMargin.Div(orderNotional).Floor().IntPart()
		totalFundsLimitedLots = funds.AvailableCash.Add(funds.RemainingMargin).Div(orderNotional).Floor().IntPart()
	}
	cashLimitedLots = maxInt64(cashLimitedLots, 0)
	marginLimitedLotsByFunds = maxInt64(marginLimitedLotsByFunds, 0)
	totalFundsLimitedLots = maxInt64(totalFundsLimitedLots, 0)

	maxLotsAllowed := slotRemaining
	reasons := []string{fmt.Sprintf("最大笔数剩余 %d 笔", slotRemaining)}
	detailTags := []CapacityTag{{Label: fmt.Sprintf("最大笔数剩余 %d 笔", slotRemaining), Kind: "config"}}
	bottlenecks := make([]string, 0, 3)
	limitTags := make([]CapacityTag, 0, 3)
	if cashLimit.IsPositive() {
		label := fmt.Sprintf("已用现金 %.4f / 上限 %.4f", usedCash.InexactFloat64(), cashLimit.InexactFloat64())
		reasons = append(reasons, label)
		detailTags = append(detailTags, CapacityTag{Label: label, Kind: "funds"})
		if !usedCash.LessThan(cashLimit) {
			maxLotsAllowed = 0
		}
		if normalizeCashLimitMode(cashLimitMode) == CashLimitModeProjected && usedCash.LessThan(cashLimit) {
			remainingCashLimit := cashLimit.Sub(usedCash)
			cashLimitLots := remainingCashLimit.Div(orderNotional).Floor().IntPart()
			cashLimitLots = maxInt64(cashLimitLots, 0)
			projectedLabel := fmt.Sprintf("现金上限剩余额度还能支持 %d 笔", cashLimitLots)
			reasons = append(reasons, projectedLabel)
			detailTags = append(detailTags, CapacityTag{Label: projectedLabel, Kind: "funds"})
			if cashLimitLots < maxLotsAllowed {
				maxLotsAllowed = cashLimitLots
			}
		}
	}
	if stock.UseMargin && stock.MaxMargin.IsPositive() {
		currentNotional := nextBuyPrice.Mul(decimal.NewFromInt(position.Quantity))
		headroom := stock.MaxMargin.Sub(currentNotional)
		if headroom.LessThanOrEqual(decimal.Zero) {
			maxLotsAllowed = 0
			label := fmt.Sprintf("融资名义持仓上限已无剩余额度(当前名义持仓 %.4f / 上限 %.4f)", currentNotional.InexactFloat64(), stock.MaxMargin.InexactFloat64())
			reasons = append(reasons, label)
			detailTags = append(detailTags, CapacityTag{Label: label, Kind: "margin"})
		} else {
			marginLimitedLots := headroom.Div(orderNotional).Floor().IntPart()
			label := fmt.Sprintf("融资名义持仓上限还能支持 %d 笔", marginLimitedLots)
			reasons = append(reasons, label)
			detailTags = append(detailTags, CapacityTag{Label: label, Kind: "margin"})
			if marginLimitedLots < maxLotsAllowed {
				maxLotsAllowed = marginLimitedLots
			}
		}
	}
	if !stock.UseMargin {
		label := fmt.Sprintf("现金可买 %d 笔", cashLimitedLots)
		reasons = append(reasons, label)
		detailTags = append(detailTags, CapacityTag{Label: label, Kind: "funds"})
	} else {
		cashLabel := fmt.Sprintf("现金可买 %d 笔", cashLimitedLots)
		marginLabel := fmt.Sprintf("融资可买 %d 笔", marginLimitedLotsByFunds)
		totalLabel := fmt.Sprintf("现金+融资合计可买 %d 笔 (现金 %.4f + 剩余融资 %.4f)", totalFundsLimitedLots, funds.AvailableCash.InexactFloat64(), funds.RemainingMargin.InexactFloat64())
		reasons = append(reasons, cashLabel, marginLabel, totalLabel)
		detailTags = append(detailTags,
			CapacityTag{Label: cashLabel, Kind: "funds"},
			CapacityTag{Label: marginLabel, Kind: "margin"},
			CapacityTag{Label: totalLabel, Kind: "funds"},
		)
	}
	if totalFundsLimitedLots < maxLotsAllowed {
		maxLotsAllowed = totalFundsLimitedLots
	}
	if maxLotsAllowed < 0 {
		maxLotsAllowed = 0
	}

	if slotRemaining == maxLotsAllowed {
		bottlenecks = append(bottlenecks, "最大笔数")
		limitTags = append(limitTags, CapacityTag{Label: "最大笔数", Kind: "config"})
	}
	if totalFundsLimitedLots == maxLotsAllowed {
		if stock.UseMargin {
			bottlenecks = append(bottlenecks, "现金/融资购买力")
			limitTags = append(limitTags, CapacityTag{Label: "现金/融资购买力", Kind: "funds"})
		} else {
			bottlenecks = append(bottlenecks, "现金购买力")
			limitTags = append(limitTags, CapacityTag{Label: "现金购买力", Kind: "funds"})
		}
	}
	if cashLimit.IsPositive() && !usedCash.LessThan(cashLimit) {
		bottlenecks = append(bottlenecks, "现金使用上限")
		limitTags = append(limitTags, CapacityTag{Label: "现金使用上限", Kind: "funds"})
	}
	if cashLimit.IsPositive() && normalizeCashLimitMode(cashLimitMode) == CashLimitModeProjected && usedCash.LessThan(cashLimit) {
		remainingCashLimit := cashLimit.Sub(usedCash)
		cashLimitLots := remainingCashLimit.Div(orderNotional).Floor().IntPart()
		cashLimitLots = maxInt64(cashLimitLots, 0)
		if cashLimitLots == maxLotsAllowed {
			bottlenecks = append(bottlenecks, "现金使用上限")
			limitTags = append(limitTags, CapacityTag{Label: "现金使用上限", Kind: "funds"})
		}
	}
	if stock.UseMargin && stock.MaxMargin.IsPositive() {
		currentNotional := nextBuyPrice.Mul(decimal.NewFromInt(position.Quantity))
		headroom := stock.MaxMargin.Sub(currentNotional)
		marginLimitedLots := int64(0)
		if headroom.IsPositive() {
			marginLimitedLots = headroom.Div(orderNotional).Floor().IntPart()
		}
		if marginLimitedLots == maxLotsAllowed {
			bottlenecks = append(bottlenecks, "融资名义持仓上限")
			limitTags = append(limitTags, CapacityTag{Label: "融资名义持仓上限", Kind: "margin"})
		}
	}

	reason := strings.Join(reasons, "；")
	if len(bottlenecks) > 0 {
		reason += fmt.Sprintf("；当前最终受 %s 限制", strings.Join(uniqueStrings(bottlenecks), "、"))
	}
	return buyCapacity{
		RemainingLots: maxLotsAllowed,
		CashNeeded:    orderNotional,
		MarginUse:     marginUse,
		Reason:        reason,
		Details:       reasons,
		Bottlenecks:   uniqueStrings(bottlenecks),
		DetailTags:    detailTags,
		LimitTags:     uniqueCapacityTags(limitTags),
	}
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func uniqueCapacityTags(values []CapacityTag) []CapacityTag {
	seen := make(map[string]struct{}, len(values))
	out := make([]CapacityTag, 0, len(values))
	for _, value := range values {
		key := value.Kind + ":" + value.Label
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
