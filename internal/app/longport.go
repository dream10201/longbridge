package app

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	openapi "github.com/longbridge/openapi-go"
	lbconfig "github.com/longbridge/openapi-go/config"
	lbquote "github.com/longbridge/openapi-go/quote"
	lbtrade "github.com/longbridge/openapi-go/trade"
)

type LongPortClient struct {
	Quote        *lbquote.QuoteContext
	Trade        *lbtrade.TradeContext
	tradeLimiter *TradeLimiter
	sessionErr   chan struct{}
}

func NewLongPortClientWithLimiterAndLogger(cfg *Config, limiter *TradeLimiter, logger *log.Logger) (*LongPortClient, error) {
	sdkCfg, err := lbconfig.New()
	if err != nil {
		return nil, fmt.Errorf("初始化 LongPort SDK 配置失败: %w", err)
	}
	sdkCfg.Client = newProxyAwareHTTPClient(sdkCfg.HTTPTimeout)
	sessionErr := make(chan struct{}, 1)
	if logger != nil {
		sdkCfg.SetLogger(newLongPortSDKLogger(logger, sessionErr))
	}

	qctx, err := lbquote.NewFromCfg(sdkCfg)
	if err != nil {
		return nil, fmt.Errorf("初始化行情上下文失败: %w", err)
	}
	tctx, err := lbtrade.NewFromCfg(sdkCfg)
	if err != nil {
		_ = qctx.Close()
		return nil, fmt.Errorf("初始化交易上下文失败: %w", err)
	}
	if limiter == nil {
		limiter = NewTradeLimiter(cfg.Engine.TradeAPIWindow, cfg.Engine.TradeAPIMaxCalls, cfg.Engine.TradeAPIMinGap)
	}

	return &LongPortClient{
		Quote:        qctx,
		Trade:        tctx,
		tradeLimiter: limiter,
		sessionErr:   sessionErr,
	}, nil
}

func (c *LongPortClient) Close() error {
	var firstErr error
	if c.Quote != nil {
		firstErr = c.Quote.Close()
	}
	if c.Trade != nil {
		if err := c.Trade.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (c *LongPortClient) HasSessionAuthFailure() bool {
	if c == nil || c.sessionErr == nil {
		return false
	}
	select {
	case <-c.sessionErr:
		return true
	default:
		return false
	}
}

func (c *LongPortClient) SessionAuthFailure() <-chan struct{} {
	if c == nil {
		return nil
	}
	return c.sessionErr
}

func (c *LongPortClient) TradingSession(ctx context.Context) ([]*lbquote.MarketTradingSession, error) {
	return c.Quote.TradingSession(ctx)
}

func (c *LongPortClient) TradingDays(ctx context.Context, market openapi.Market, begin *time.Time, end *time.Time) (*lbquote.MarketTradingDay, error) {
	return c.Quote.TradingDays(ctx, market, begin, end)
}

func (c *LongPortClient) StaticInfo(ctx context.Context, symbols []string) ([]*lbquote.StaticInfo, error) {
	return c.Quote.StaticInfo(ctx, symbols)
}

func (c *LongPortClient) QuoteList(ctx context.Context, symbols []string) ([]*lbquote.SecurityQuote, error) {
	return c.Quote.Quote(ctx, symbols)
}

func (c *LongPortClient) StockPositions(ctx context.Context, symbols []string) ([]*lbtrade.StockPositionChannel, error) {
	if err := c.tradeLimiter.Wait(ctx); err != nil {
		return nil, err
	}
	return c.Trade.StockPositions(ctx, symbols)
}

func (c *LongPortClient) TodayOrders(ctx context.Context, params *lbtrade.GetTodayOrders) ([]*lbtrade.Order, error) {
	if err := c.tradeLimiter.Wait(ctx); err != nil {
		return nil, err
	}
	return c.Trade.TodayOrders(ctx, params)
}

func (c *LongPortClient) OrderDetail(ctx context.Context, orderID string) (lbtrade.OrderDetail, error) {
	if err := c.tradeLimiter.Wait(ctx); err != nil {
		return lbtrade.OrderDetail{}, err
	}
	return c.Trade.OrderDetail(ctx, orderID)
}

func (c *LongPortClient) CancelOrder(ctx context.Context, orderID string) error {
	if err := c.tradeLimiter.Wait(ctx); err != nil {
		return err
	}
	return c.Trade.CancelOrder(ctx, orderID)
}

func (c *LongPortClient) EstimateMaxPurchaseQuantity(ctx context.Context, params *lbtrade.GetEstimateMaxPurchaseQuantity) (lbtrade.EstimateMaxPurchaseQuantityResponse, error) {
	if err := c.tradeLimiter.Wait(ctx); err != nil {
		return lbtrade.EstimateMaxPurchaseQuantityResponse{}, err
	}
	return c.Trade.EstimateMaxPurchaseQuantity(ctx, params)
}

func (c *LongPortClient) AccountBalance(ctx context.Context, params *lbtrade.GetAccountBalance) ([]*lbtrade.AccountBalance, error) {
	if err := c.tradeLimiter.Wait(ctx); err != nil {
		return nil, err
	}
	return c.Trade.AccountBalance(ctx, params)
}

func (c *LongPortClient) SubmitOrder(ctx context.Context, params *lbtrade.SubmitOrder) (string, error) {
	if err := c.tradeLimiter.Wait(ctx); err != nil {
		return "", err
	}
	return c.Trade.SubmitOrder(ctx, params)
}

type TradeLimiter struct {
	mu       sync.Mutex
	window   time.Duration
	maxCalls int
	minGap   time.Duration
	last     time.Time
	calls    []time.Time
}

func NewTradeLimiter(window time.Duration, maxCalls int, minGap time.Duration) *TradeLimiter {
	return &TradeLimiter{
		window:   window,
		maxCalls: maxCalls,
		minGap:   minGap,
		calls:    make([]time.Time, 0, maxCalls),
	}
}

func (l *TradeLimiter) Wait(ctx context.Context) error {
	for {
		waitFor, ok := l.reserve()
		if ok {
			return nil
		}
		timer := time.NewTimer(waitFor)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (l *TradeLimiter) reserve() (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-l.window)
	kept := l.calls[:0]
	for _, ts := range l.calls {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	l.calls = kept

	if !l.last.IsZero() {
		nextGapAt := l.last.Add(l.minGap)
		if now.Before(nextGapAt) {
			return time.Until(nextGapAt), false
		}
	}

	if len(l.calls) >= l.maxCalls {
		nextAt := l.calls[0].Add(l.window)
		if now.Before(nextAt) {
			return time.Until(nextAt), false
		}
	}

	l.last = now
	l.calls = append(l.calls, now)
	return 0, true
}

type longPortSDKLogger struct {
	logger     *log.Logger
	sessionErr chan<- struct{}
	level      string
	mu         sync.Mutex
	last       map[string]time.Time
}

func newLongPortSDKLogger(logger *log.Logger, sessionErr chan<- struct{}) *longPortSDKLogger {
	return &longPortSDKLogger{
		logger:     logger,
		sessionErr: sessionErr,
		last:       make(map[string]time.Time),
	}
}

func (l *longPortSDKLogger) SetLevel(level string) {
	l.level = strings.ToLower(strings.TrimSpace(level))
}

func (l *longPortSDKLogger) Info(msg string) {
	if !l.enabled("info") {
		return
	}
	l.log("INFO", msg)
}

func (l *longPortSDKLogger) Error(msg string) {
	if !l.enabled("error") {
		return
	}
	l.log("ERR", msg)
}

func (l *longPortSDKLogger) Warn(msg string) {
	if !l.enabled("warn") {
		return
	}
	l.log("WARN", msg)
}

func (l *longPortSDKLogger) Debug(msg string) {
	if !l.enabled("debug") {
		return
	}
	l.log("DEBUG", msg)
}

func (l *longPortSDKLogger) Infof(format string, args ...any) {
	l.Info(fmt.Sprintf(format, args...))
}

func (l *longPortSDKLogger) Errorf(format string, args ...any) {
	l.Error(fmt.Sprintf(format, args...))
}

func (l *longPortSDKLogger) Warnf(format string, args ...any) {
	l.Warn(fmt.Sprintf(format, args...))
}

func (l *longPortSDKLogger) Debugf(format string, args ...any) {
	l.Debug(fmt.Sprintf(format, args...))
}

func (l *longPortSDKLogger) enabled(level string) bool {
	switch l.level {
	case "debug":
		return true
	case "warn":
		return level == "warn" || level == "error"
	case "error":
		return level == "error"
	default:
		return level != "debug"
	}
}

func (l *longPortSDKLogger) log(level string, msg string) {
	if isLongPortSessionAuthFailure(msg) {
		l.notifySessionAuthFailure()
	}
	if l.shouldSuppress(msg) {
		return
	}
	if l.logger != nil {
		l.logger.Printf("LongPort SDK [%s] %s", level, msg)
	}
}

func (l *longPortSDKLogger) notifySessionAuthFailure() {
	if l.sessionErr == nil {
		return
	}
	select {
	case l.sessionErr <- struct{}{}:
	default:
	}
}

func (l *longPortSDKLogger) shouldSuppress(msg string) bool {
	key, ok := longPortNoisyReconnectKey(msg)
	if !ok {
		return false
	}

	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	last, seen := l.last[key]
	if seen && now.Sub(last) < 5*time.Minute {
		return true
	}
	l.last[key] = now
	return false
}

func longPortNoisyReconnectKey(msg string) (string, bool) {
	switch {
	case strings.Contains(msg, "start reconnecting"):
		return "reconnect-start", true
	case strings.Contains(msg, "close old conn for reconnect"):
		return "reconnect-close-old", true
	case strings.Contains(msg, "reconnect failed"):
		return "reconnect-failed", true
	default:
		return "", false
	}
}

func isLongPortSessionAuthFailure(msg string) bool {
	lower := strings.ToLower(msg)
	if !strings.Contains(lower, "reconnect") {
		return false
	}
	return strings.Contains(lower, "status:5 code:401") ||
		strings.Contains(lower, "token is not exists") ||
		strings.Contains(lower, "unauthenticated")
}
