package app

import "github.com/shopspring/decimal"

// FeeConfig 描述每笔订单的费用模型:按股计费项各带每单最低值,卖出另有监管费。
type FeeConfig struct {
	CommissionPerShare decimal.Decimal
	CommissionMin      decimal.Decimal
	PlatformPerShare   decimal.Decimal
	PlatformMin        decimal.Decimal
	SettlementPerShare decimal.Decimal
	SettlementMin      decimal.Decimal
	SECRate            decimal.Decimal
	FINRAPerShare      decimal.Decimal
}

var (
	secFeeMin   = decimal.RequireFromString("0.01")
	finraFeeMax = decimal.RequireFromString("8.30")
)

func defaultFeeConfig() FeeConfig {
	return FeeConfig{
		CommissionPerShare: decimal.Zero,
		CommissionMin:      decimal.Zero,
		PlatformPerShare:   decimal.RequireFromString("0.005"),
		PlatformMin:        decimal.NewFromInt(1),
		SettlementPerShare: decimal.RequireFromString("0.003"),
		SettlementMin:      secFeeMin,
		SECRate:            decimal.RequireFromString("0.0000051"),
		FINRAPerShare:      decimal.RequireFromString("0.000166"),
	}
}

func perShareWithMin(perShare, minimum decimal.Decimal, qty decimal.Decimal) decimal.Decimal {
	return decimalMax(perShare.Mul(qty), minimum)
}

// Estimate 估算单笔订单费用;price 为成交价,qty 为股数。
func (f FeeConfig) Estimate(side ActionType, price decimal.Decimal, qty int64) decimal.Decimal {
	if qty <= 0 {
		return decimal.Zero
	}
	shares := decimal.NewFromInt(qty)
	total := perShareWithMin(f.CommissionPerShare, f.CommissionMin, shares).
		Add(perShareWithMin(f.PlatformPerShare, f.PlatformMin, shares)).
		Add(perShareWithMin(f.SettlementPerShare, f.SettlementMin, shares))
	if side == ActionSell {
		if f.SECRate.IsPositive() {
			total = total.Add(decimalMax(price.Mul(shares).Mul(f.SECRate), secFeeMin))
		}
		if f.FINRAPerShare.IsPositive() {
			total = total.Add(decimalMin(f.FINRAPerShare.Mul(shares), finraFeeMax))
		}
	}
	return total
}

// RoundTrip 估算一买一卖的总费用。
func (f FeeConfig) RoundTrip(buyPrice, sellPrice decimal.Decimal, qty int64) decimal.Decimal {
	return f.Estimate(ActionBuy, buyPrice, qty).Add(f.Estimate(ActionSell, sellPrice, qty))
}

// netProfit 返回扣除买卖费用后的单笔净利。
func netProfit(f FeeConfig, buyPrice, sellPrice decimal.Decimal, qty int64) decimal.Decimal {
	return sellPrice.Sub(buyPrice).Mul(decimal.NewFromInt(qty)).Sub(f.RoundTrip(buyPrice, sellPrice, qty))
}

func (e *Engine) fees() FeeConfig {
	if e == nil || e.cfg == nil {
		return FeeConfig{}
	}
	return e.cfg.Fees
}
