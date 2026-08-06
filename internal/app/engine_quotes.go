package app

import (
	lbquote "github.com/longbridge/openapi-go/quote"
	"github.com/shopspring/decimal"
)

func quoteChange(quote *lbquote.SecurityQuote, phase MarketPhase) (decimal.Decimal, decimal.Decimal, string, bool) {
	point, ok := resolveQuotePricePoint(quote, phase)
	if !ok || point.prevClose == nil || !point.prevClose.IsPositive() {
		return decimal.Zero, decimal.Zero, "flat", false
	}
	change := point.price.Sub(*point.prevClose)
	changePct := change.Div(*point.prevClose).Mul(decimal.NewFromInt(100))
	trend := "flat"
	switch {
	case change.GreaterThan(decimal.Zero):
		trend = "up"
	case change.LessThan(decimal.Zero):
		trend = "down"
	}
	return change, changePct, trend, true
}

type quotePricePoint struct {
	price     decimal.Decimal
	prevClose *decimal.Decimal
	timestamp int64
}

func currentPriceFromQuote(quote *lbquote.SecurityQuote, phase MarketPhase) (decimal.Decimal, bool) {
	point, ok := resolveQuotePricePoint(quote, phase)
	if !ok {
		return decimal.Zero, false
	}
	return point.price, true
}

func resolveQuotePricePoint(quote *lbquote.SecurityQuote, phase MarketPhase) (quotePricePoint, bool) {
	if quote == nil {
		return quotePricePoint{}, false
	}

	candidates := make([]quotePricePoint, 0, 4)
	if quote.LastDone != nil {
		candidates = append(candidates, quotePricePoint{
			price:     *quote.LastDone,
			prevClose: quote.PrevClose,
			timestamp: quote.Timestamp,
		})
	}
	appendPrePost := func(item *lbquote.PrePostQuote) {
		if item == nil || item.LastDone == nil {
			return
		}
		prevClose := item.PrevClose
		if prevClose == nil {
			prevClose = quote.PrevClose
		}
		candidates = append(candidates, quotePricePoint{
			price:     *item.LastDone,
			prevClose: prevClose,
			timestamp: item.Timestamp,
		})
	}
	appendPrePost(quote.PreMarketQuote)
	appendPrePost(quote.PostMarketQuote)
	appendPrePost(quote.OverNightQuote)
	if len(candidates) == 0 {
		return quotePricePoint{}, false
	}

	var latest quotePricePoint
	hasTimestamp := false
	for _, candidate := range candidates {
		if candidate.timestamp <= 0 {
			continue
		}
		if !hasTimestamp || candidate.timestamp > latest.timestamp {
			latest = candidate
			hasTimestamp = true
		}
	}
	if hasTimestamp {
		return latest, true
	}

	switch phase {
	case MarketPhasePre:
		if quote.PreMarketQuote != nil && quote.PreMarketQuote.LastDone != nil {
			return quotePricePoint{price: *quote.PreMarketQuote.LastDone, prevClose: quote.PreMarketQuote.PrevClose, timestamp: quote.PreMarketQuote.Timestamp}, true
		}
	case MarketPhasePost:
		if quote.PostMarketQuote != nil && quote.PostMarketQuote.LastDone != nil {
			return quotePricePoint{price: *quote.PostMarketQuote.LastDone, prevClose: quote.PostMarketQuote.PrevClose, timestamp: quote.PostMarketQuote.Timestamp}, true
		}
	case MarketPhaseOvernight:
		if quote.OverNightQuote != nil && quote.OverNightQuote.LastDone != nil {
			return quotePricePoint{price: *quote.OverNightQuote.LastDone, prevClose: quote.OverNightQuote.PrevClose, timestamp: quote.OverNightQuote.Timestamp}, true
		}
	}
	if quote.LastDone != nil {
		return quotePricePoint{price: *quote.LastDone, prevClose: quote.PrevClose, timestamp: quote.Timestamp}, true
	}
	if quote.PreMarketQuote != nil && quote.PreMarketQuote.LastDone != nil {
		return quotePricePoint{price: *quote.PreMarketQuote.LastDone, prevClose: quote.PreMarketQuote.PrevClose, timestamp: quote.PreMarketQuote.Timestamp}, true
	}
	if quote.PostMarketQuote != nil && quote.PostMarketQuote.LastDone != nil {
		return quotePricePoint{price: *quote.PostMarketQuote.LastDone, prevClose: quote.PostMarketQuote.PrevClose, timestamp: quote.PostMarketQuote.Timestamp}, true
	}
	if quote.OverNightQuote != nil && quote.OverNightQuote.LastDone != nil {
		return quotePricePoint{price: *quote.OverNightQuote.LastDone, prevClose: quote.OverNightQuote.PrevClose, timestamp: quote.OverNightQuote.Timestamp}, true
	}
	return quotePricePoint{}, false
}
