package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"sync"
	"time"

	lbquote "github.com/longbridge/openapi-go/quote"
	lbtrade "github.com/longbridge/openapi-go/trade"
	"github.com/shopspring/decimal"
)

type SessionWindow struct {
	Name  string    `json:"name"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type MarketClock struct {
	Date          string          `json:"date"`
	TimeZone      string          `json:"time_zone"`
	TradingDay    bool            `json:"trading_day"`
	CurrentPhase  MarketPhase     `json:"current_phase"`
	Open          bool            `json:"open"`
	OpenTime      *time.Time      `json:"open_time,omitempty"`
	CloseTime     *time.Time      `json:"close_time,omitempty"`
	Windows       []SessionWindow `json:"windows"`
	LastRefreshed time.Time       `json:"last_refreshed"`
}

type PositionSnapshot struct {
	Quantity  int64           `json:"quantity"`
	Available int64           `json:"available"`
	CostPrice decimal.Decimal `json:"cost_price"`
	HasCost   bool            `json:"has_cost"`
}

type SymbolSnapshot struct {
	Symbol                 string        `json:"symbol"`
	Enabled                bool          `json:"enabled"`
	UseMargin              bool          `json:"use_margin"`
	MaxMargin              string        `json:"max_margin,omitempty"`
	MaxLots                int64         `json:"max_lots"`
	OrderQuantity          int64         `json:"order_quantity"`
	Price                  string        `json:"price"`
	PriceChange            string        `json:"price_change,omitempty"`
	PriceChangePct         string        `json:"price_change_pct,omitempty"`
	PriceTrend             string        `json:"price_trend,omitempty"`
	PositionQty            int64         `json:"position_qty"`
	AvailableQty           int64         `json:"available_qty"`
	PositionNotional       string        `json:"position_notional,omitempty"`
	MaxPositionNotional    string        `json:"max_position_notional,omitempty"`
	CostPrice              string        `json:"cost_price,omitempty"`
	LastAction             ActionType    `json:"last_action"`
	LastBuyPrice           string        `json:"last_buy_price,omitempty"`
	LastSellPrice          string        `json:"last_sell_price,omitempty"`
	LastFilledAtText       string        `json:"last_filled_at_text,omitempty"`
	LastFilledPrice        string        `json:"last_filled_price,omitempty"`
	PendingOrderID         string        `json:"pending_order_id,omitempty"`
	PendingSide            ActionType    `json:"pending_side,omitempty"`
	PendingPrice           string        `json:"pending_price,omitempty"`
	PendingAt              *time.Time    `json:"pending_at,omitempty"`
	PendingTimeoutLeft     string        `json:"pending_timeout_left,omitempty"`
	Decision               string        `json:"decision"`
	LastOrderStatus        string        `json:"last_order_status,omitempty"`
	LastOrderMsg           string        `json:"last_order_msg,omitempty"`
	LastError              string        `json:"last_error,omitempty"`
	TriggerPrice           string        `json:"trigger_price,omitempty"`
	ExpectedProfit         string        `json:"expected_profit,omitempty"`
	NextBuyPrice           string        `json:"next_buy_price,omitempty"`
	BaseBuyPrice           string        `json:"base_buy_price,omitempty"`
	NextBuyPercent         string        `json:"next_buy_percent,omitempty"`
	NextBuyRef             string        `json:"next_buy_ref,omitempty"`
	NextBuyReason          string        `json:"next_buy_reason,omitempty"`
	BuyTrailArmed          bool          `json:"buy_trail_armed,omitempty"`
	BuyTrailLowest         string        `json:"buy_trail_lowest,omitempty"`
	BuyTrailTrigger        string        `json:"buy_trail_trigger,omitempty"`
	BuyTrailPercent        string        `json:"buy_trail_percent,omitempty"`
	RemainingBuyLots       int64         `json:"remaining_buy_lots,omitempty"`
	NextBuyCashNeeded      string        `json:"next_buy_cash_needed,omitempty"`
	NextBuyMarginUse       string        `json:"next_buy_margin_use,omitempty"`
	BuyCapacityReason      string        `json:"buy_capacity_reason,omitempty"`
	BuyCapacityDetails     []string      `json:"buy_capacity_details,omitempty"`
	BuyCapacityBottlenecks []string      `json:"buy_capacity_bottlenecks,omitempty"`
	BuyCapacityDetailTags  []CapacityTag `json:"buy_capacity_detail_tags,omitempty"`
	BuyCapacityLimitTags   []CapacityTag `json:"buy_capacity_limit_tags,omitempty"`
	NextSellPrice          string        `json:"next_sell_price,omitempty"`
	BaseSellPrice          string        `json:"base_sell_price,omitempty"`
	NextSellRef            string        `json:"next_sell_ref,omitempty"`
	NextSellProfit         string        `json:"next_sell_profit,omitempty"`
	NextSellReason         string        `json:"next_sell_reason,omitempty"`
	SellTrailArmed         bool          `json:"sell_trail_armed,omitempty"`
	SellTrailHighest       string        `json:"sell_trail_highest,omitempty"`
	SellTrailTrigger       string        `json:"sell_trail_trigger,omitempty"`
	SellTrailPercent       string        `json:"sell_trail_percent,omitempty"`
	Remark                 string        `json:"remark,omitempty"`
}

type CapacityTag struct {
	Label string `json:"label"`
	Kind  string `json:"kind"`
}

type DashboardSnapshot struct {
	StartedAt        time.Time                `json:"started_at"`
	LastUpdatedAt    time.Time                `json:"last_updated_at"`
	ConfigPath       string                   `json:"config_path,omitempty"`
	ConfigReloadedAt *time.Time               `json:"config_reloaded_at,omitempty"`
	DryRun           bool                     `json:"dry_run"`
	ListenAddress    string                   `json:"listen_address"`
	AccessToken      AccessTokenStatus        `json:"access_token"`
	Market           MarketClock              `json:"market"`
	CashLimit        CashLimitSnapshot        `json:"cash_limit"`
	Summary          DashboardSummary         `json:"summary"`
	AccountBalances  []AccountBalanceSnapshot `json:"account_balances,omitempty"`
	AccountError     string                   `json:"account_error,omitempty"`
	Symbols          []SymbolSnapshot         `json:"symbols"`
}

type CashLimitSnapshot struct {
	Enabled   bool   `json:"enabled"`
	Mode      string `json:"mode,omitempty"`
	Limit     string `json:"limit,omitempty"`
	Used      string `json:"used,omitempty"`
	Remaining string `json:"remaining,omitempty"`
	Reached   bool   `json:"reached"`
	Message   string `json:"message,omitempty"`
}

type DashboardSummary struct {
	PendingBuySymbols  []string `json:"pending_buy_symbols,omitempty"`
	PendingSellSymbols []string `json:"pending_sell_symbols,omitempty"`
	LimitedSymbols     []string `json:"limited_symbols,omitempty"`
}

type AccountBalanceSnapshot struct {
	Currency               string             `json:"currency"`
	TotalCash              string             `json:"total_cash,omitempty"`
	MaxFinanceAmount       string             `json:"max_finance_amount,omitempty"`
	RemainingFinanceAmount string             `json:"remaining_finance_amount,omitempty"`
	NetAssets              string             `json:"net_assets,omitempty"`
	InitMargin             string             `json:"init_margin,omitempty"`
	MaintenanceMargin      string             `json:"maintenance_margin,omitempty"`
	MarginCall             string             `json:"margin_call,omitempty"`
	RiskLevel              string             `json:"risk_level,omitempty"`
	CashInfos              []CashInfoSnapshot `json:"cash_infos,omitempty"`
}

type CashInfoSnapshot struct {
	Currency      string `json:"currency"`
	WithdrawCash  string `json:"withdraw_cash,omitempty"`
	AvailableCash string `json:"available_cash,omitempty"`
	FrozenCash    string `json:"frozen_cash,omitempty"`
	SettlingCash  string `json:"settling_cash,omitempty"`
}

type Engine struct {
	cfg        *Config
	cfgModTime time.Time
	logger     *log.Logger
	client     *LongPortClient
	auth       *AccessTokenManager
	state      *StateStore
	startedAt  time.Time

	marketTZ  *time.Location
	displayTZ *time.Location
	static    map[string]*lbquote.StaticInfo

	marketMu         sync.RWMutex
	sessionTemplates []sessionTemplate
	tradingDayByDate map[string]bool

	statusMu sync.RWMutex
	status   DashboardSnapshot

	cycleCount uint64
}

type sessionTemplate struct {
	Name  string
	Begin int32
	End   int32
}

const (
	offHoursPollFallback     = 15 * time.Minute
	pendingOrderPollInterval = 30 * time.Second
	minAdaptivePollDelay     = time.Second
	nextTradingSearchDays    = 14
	// stateFlushEveryCycles 是状态文件的周期性落盘间隔(按轮询周期计)。
	// 平时只在内存累积,达到该周期数时兜底落盘一次,将硬崩溃(kill -9/断电)
	// 最多丢失的状态控制在最近这么多个周期内。优雅退出与崩溃恢复仍会即时落盘。
	stateFlushEveryCycles = 30
)

func NewEngine(cfg *Config, logger *log.Logger) (*Engine, error) {
	authManager, err := NewAccessTokenManager(cfg, logger)
	if err != nil {
		return nil, err
	}
	if err := authManager.ValidateConfiguredToken(); err != nil {
		return nil, fmt.Errorf("自动刷新 Access Token 配置无效: %w", err)
	}
	refreshCtx, refreshCancel := context.WithTimeout(context.Background(), cfg.Engine.QuoteRequestTimeout)
	defer refreshCancel()
	if _, err := authManager.EnsureValid(refreshCtx); err != nil {
		return nil, fmt.Errorf("启动前刷新 Access Token 失败: %w", err)
	}

	client, err := NewLongPortClientWithLimiterAndLogger(cfg, nil, logger)
	if err != nil {
		return nil, err
	}
	stateStore, err := NewStateStore(cfg.Engine.StateFile)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("加载美股时区失败: %w", err)
	}
	displayLoc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("加载北京时间时区失败: %w", err)
	}

	engine := &Engine{
		cfg:              cfg,
		logger:           logger,
		client:           client,
		auth:             authManager,
		state:            stateStore,
		startedAt:        time.Now().UTC(),
		marketTZ:         loc,
		displayTZ:        displayLoc,
		static:           make(map[string]*lbquote.StaticInfo),
		tradingDayByDate: make(map[string]bool),
		status: DashboardSnapshot{
			StartedAt:     time.Now().UTC(),
			DryRun:        cfg.Engine.DryRun,
			ListenAddress: cfg.Server.Address(),
			ConfigPath:    cfg.FilePath,
			AccessToken:   authManager.Snapshot(),
		},
	}
	if stat, err := os.Stat(cfg.FilePath); err == nil {
		engine.cfgModTime = stat.ModTime()
	}

	if err := engine.bootstrap(context.Background()); err != nil {
		_ = client.Close()
		return nil, err
	}
	return engine, nil
}

func (e *Engine) Close() error {
	var firstErr error
	if e.state != nil {
		if err := e.state.Flush(); err != nil {
			firstErr = err
			if e.logger != nil {
				e.logger.Printf("退出前写入状态文件失败: %v", err)
			}
		}
	}
	if e.client != nil {
		if err := e.client.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (e *Engine) Snapshot() DashboardSnapshot {
	e.statusMu.RLock()
	defer e.statusMu.RUnlock()
	return e.status
}

func (e *Engine) bootstrap(ctx context.Context) error {
	symbols := e.enabledSymbols()
	staticInfo, err := e.client.StaticInfo(ctx, symbols)
	if err != nil {
		return fmt.Errorf("读取股票静态信息失败: %w", err)
	}
	for _, item := range staticInfo {
		e.static[item.Symbol] = item
	}
	for _, stock := range e.cfg.Stocks {
		if !stock.Enabled {
			continue
		}
		info := e.static[stock.Symbol]
		if info == nil {
			return fmt.Errorf("未获取到 %s 的静态信息", stock.Symbol)
		}
		if info.LotSize > 0 && stock.OrderQuantity%int64(info.LotSize) != 0 {
			return fmt.Errorf("%s: 每笔数量 %d 不是 lot size %d 的整数倍", stock.Symbol, stock.OrderQuantity, info.LotSize)
		}
	}

	marketClock, err := e.loadMarketClock(ctx, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("读取交易时段失败: %w", err)
	}
	e.statusMu.Lock()
	e.status.Market = marketClock
	e.statusMu.Unlock()
	return nil
}

func (e *Engine) Run(ctx context.Context) error {
	for {
		if err := e.safeCycle(ctx); err != nil {
			e.logger.Printf("轮询失败: %v", err)
		}

		e.cycleCount++
		if e.cycleCount%stateFlushEveryCycles == 0 && e.state != nil {
			if err := e.state.Flush(); err != nil {
				e.logger.Printf("周期性写入状态文件失败: %v", err)
			}
		}

		delay := e.nextRunDelay(ctx)
		timer := time.NewTimer(delay)
		sessionAuthFailure := e.client.SessionAuthFailure()
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-sessionAuthFailure:
			if !timer.Stop() {
				<-timer.C
			}
			if err := e.rebuildClientAfterSessionAuthFailure(); err != nil {
				e.logger.Printf("LongPort 会话失效后重建失败: %v", err)
			}
		case <-timer.C:
		}
	}
}

func (e *Engine) safeCycle(ctx context.Context) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("轮询崩溃: %v", r)
			if e.state != nil {
				if flushErr := e.state.Flush(); flushErr != nil && e.logger != nil {
					e.logger.Printf("轮询崩溃后写入状态文件失败: %v", flushErr)
				}
			}
			if rebuildErr := e.rebuildClientAfterSessionAuthFailure(); rebuildErr != nil && e.logger != nil {
				e.logger.Printf("轮询崩溃后重建 LongPort 客户端失败: %v", rebuildErr)
			}
		}
	}()
	return e.cycle(ctx)
}

func (e *Engine) nextRunDelay(ctx context.Context) time.Duration {
	snapshot := e.Snapshot()
	now := time.Now().UTC()

	nextResumeAt, err := e.nextMarketResumeTime(ctx, snapshot.Market, now)
	if err != nil && e.logger != nil {
		e.logger.Printf("计算下一次交易窗口失败，回退固定轮询: %v", err)
	}

	var refreshDueAt *time.Time
	if snapshot.AccessToken.RefreshDueAt != nil {
		dueAt := snapshot.AccessToken.RefreshDueAt.UTC()
		refreshDueAt = &dueAt
	}

	return computeAdaptivePollDelay(
		now,
		snapshot.Market,
		e.cfg.Engine.PollInterval,
		e.hasPendingOrders(),
		refreshDueAt,
		nextResumeAt,
	)
}

func (e *Engine) cycle(ctx context.Context) error {
	e.reloadConfigIfChanged(ctx)

	refreshCtx, refreshCancel := context.WithTimeout(ctx, e.cfg.Engine.QuoteRequestTimeout)
	if err := e.refreshAccessTokenIfNeeded(refreshCtx); err != nil {
		refreshCancel()
		return err
	}
	refreshCancel()

	if err := e.rebuildClientIfSessionAuthFailed(); err != nil {
		return err
	}

	reqCtx, cancel := context.WithTimeout(ctx, e.cfg.Engine.QuoteRequestTimeout)
	defer cancel()

	now := time.Now().UTC()
	marketClock, err := e.loadMarketClock(reqCtx, now)
	if err != nil {
		return err
	}

	symbols := e.enabledSymbols()
	quotes, err := e.client.QuoteList(reqCtx, symbols)
	if err != nil {
		return fmt.Errorf("获取行情失败: %w", err)
	}
	quoteBySymbol := make(map[string]*lbquote.SecurityQuote, len(quotes))
	for _, item := range quotes {
		quoteBySymbol[item.Symbol] = item
	}

	positionChannels, err := e.client.StockPositions(reqCtx, symbols)
	if err != nil {
		return fmt.Errorf("获取持仓失败: %w", err)
	}
	positions := aggregatePositions(positionChannels)
	accountBalances, accountErr := e.client.AccountBalance(reqCtx, &lbtrade.GetAccountBalance{Currency: lbtrade.CurrencyUSD})
	accountSnapshots := make([]AccountBalanceSnapshot, 0, len(accountBalances))
	funds := extractUSDFunds(accountBalances)
	brokerPendingBySymbol, brokerPendingErr := e.loadBrokerPendingOrders(reqCtx)
	if brokerPendingErr != nil {
		e.logger.Printf("读取券商活跃挂单失败: %v", brokerPendingErr)
	}
	funds.DeployedCash = e.strategyDeployedCash(positions, quoteBySymbol, marketClock.CurrentPhase)
	cashLimitSnapshot := buildCashLimitSnapshot(e.cfg.Engine.CashLimit, e.cfg.Engine.CashLimitMode, funds, accountErr)
	if accountErr == nil {
		accountSnapshots = buildAccountSnapshots(accountBalances)
	}

	snapshots := make([]SymbolSnapshot, 0, len(e.cfg.Stocks))
	summary := DashboardSummary{}
	for _, stock := range e.cfg.Stocks {
		state := e.state.Get(stock.Symbol)
		if state.Pending == nil {
			if brokerOrder := brokerPendingBySymbol[stock.Symbol]; brokerOrder != nil {
				if err := e.recoverPendingOrder(stock, brokerOrder); err != nil {
					e.logger.Printf("%s 恢复券商挂单失败: %v", stock.Symbol, err)
				} else {
					state = e.state.Get(stock.Symbol)
				}
			}
		}
		if state.Pending != nil {
			switch state.Pending.Side {
			case ActionBuy:
				summary.PendingBuySymbols = append(summary.PendingBuySymbols, stock.Symbol)
			case ActionSell:
				summary.PendingSellSymbols = append(summary.PendingSellSymbols, stock.Symbol)
			}
			terminal, syncErr := e.syncPendingOrder(reqCtx, stock, state)
			if syncErr != nil {
				e.logger.Printf("%s 同步挂单失败: %v", stock.Symbol, syncErr)
				_ = e.state.Update(stock.Symbol, func(s *SymbolState) {
					s.LastError = syncErr.Error()
				})
				state = e.state.Get(stock.Symbol)
			} else if terminal {
				state = e.state.Get(stock.Symbol)
				positionChannels, err = e.client.StockPositions(reqCtx, symbols)
				if err == nil {
					positions = aggregatePositions(positionChannels)
				}
			}
		}

		quoteItem := quoteBySymbol[stock.Symbol]
		price, hasPrice := currentPriceFromQuote(quoteItem, marketClock.CurrentPhase)
		position := positions[stock.Symbol]
		state = e.normalizeStateWithPosition(stock, position, state)
		var costPtr *decimal.Decimal
		if position.HasCost {
			costCopy := position.CostPrice
			costPtr = &costCopy
		}
		var configChanged bool
		state, configChanged = e.reconcileStrategyConfig(reqCtx, stock, position, costPtr, state)

		snapshot := SymbolSnapshot{
			Symbol:          stock.Symbol,
			Enabled:         stock.Enabled,
			UseMargin:       stock.UseMargin,
			MaxMargin:       stock.MaxMargin.StringFixed(4),
			MaxLots:         stock.MaxLots,
			OrderQuantity:   stock.OrderQuantity,
			PositionQty:     position.Quantity,
			AvailableQty:    position.Available,
			LastAction:      state.LastAction,
			LastBuyPrice:    state.LastBuyPrice,
			LastSellPrice:   state.LastSellPrice,
			LastOrderStatus: state.LastOrderStatus,
			LastOrderMsg:    state.LastOrderMsg,
			LastError:       state.LastError,
			Remark:          stock.Remark,
			Decision:        state.LastDecision,
		}
		if state.LastFilledAt != nil && !state.LastFilledAt.IsZero() {
			snapshot.LastFilledAtText = formatDisplayTime(*state.LastFilledAt, e.displayTZ)
			switch state.LastAction {
			case ActionBuy:
				snapshot.LastFilledPrice = state.LastBuyPrice
			case ActionSell:
				snapshot.LastFilledPrice = state.LastSellPrice
			}
		}
		if hasPrice {
			snapshot.Price = price.StringFixed(2)
			snapshot.PositionNotional = price.Mul(decimal.NewFromInt(position.Quantity)).StringFixed(4)
			snapshot.MaxPositionNotional = price.Mul(decimal.NewFromInt(stock.MaxLots * stock.OrderQuantity)).StringFixed(4)
			change, changePct, trend, ok := quoteChange(quoteItem, marketClock.CurrentPhase)
			if ok {
				snapshot.PriceChange = change.StringFixed(2)
				snapshot.PriceChangePct = changePct.StringFixed(2)
				snapshot.PriceTrend = trend
			}
		}
		if position.HasCost {
			snapshot.CostPrice = position.CostPrice.StringFixed(4)
		}
		if state.Pending != nil {
			snapshot.PendingOrderID = state.Pending.OrderID
			snapshot.PendingSide = state.Pending.Side
			snapshot.PendingPrice = state.Pending.SubmittedPrice
			snapshot.PendingAt = &state.Pending.SubmittedAt
			snapshot.PendingTimeoutLeft = formatTimeoutLeft(time.Until(state.Pending.SubmittedAt.Add(e.cfg.Engine.OrderTimeout)))
			snapshot.Decision = pendingOrderDecisionText(state)
			snapshots = append(snapshots, snapshot)
			continue
		}
		if configChanged {
			snapshot.Decision = state.LastDecision
			snapshot.LastOrderStatus = state.LastOrderStatus
			snapshot.LastOrderMsg = state.LastOrderMsg
			snapshot.LastError = state.LastError
			snapshots = append(snapshots, snapshot)
			continue
		}
		if !stock.Enabled {
			snapshot.Decision = "当前股票已禁用"
			snapshots = append(snapshots, snapshot)
			continue
		}
		if !hasPrice {
			snapshot.Decision = "当前无有效行情价格"
			snapshots = append(snapshots, snapshot)
			continue
		}

		state = e.updateTrailingBuyState(stock, position, price, costPtr, state)
		state = e.updateTrailingSellState(stock, position, price, costPtr, state)

		decision := EvaluateStrategy(StrategyInput{
			Config:       stock,
			CurrentPrice: price,
			PositionQty:  position.Quantity,
			AvailableQty: position.Available,
			CostPrice:    costPtr,
			State:        state,
			MarketOpen:   marketClock.Open,
		})
		preview := PreviewStrategy(StrategyInput{
			Config:       stock,
			CurrentPrice: price,
			PositionQty:  position.Quantity,
			AvailableQty: position.Available,
			CostPrice:    costPtr,
			State:        state,
			MarketOpen:   marketClock.Open,
		})
		nextOrderNotional := decimal.Zero
		if decision.Action == ActionBuy {
			nextOrderNotional = resolveOrderPrice(decision, price).Mul(decimal.NewFromInt(stock.OrderQuantity))
		}
		decision = applyGlobalBuyConstraints(decision, funds, accountErr, e.cfg.Engine.CashLimit, e.cfg.Engine.CashLimitMode, nextOrderNotional)
		snapshot.LastBuyPrice = state.LastBuyPrice
		snapshot.LastSellPrice = state.LastSellPrice
		snapshot.Decision = decision.Reason
		if !decision.TriggerPrice.IsZero() {
			snapshot.TriggerPrice = decision.TriggerPrice.StringFixed(4)
		}
		if !decision.ExpectedProfit.IsZero() {
			snapshot.ExpectedProfit = decision.ExpectedProfit.StringFixed(4)
		}
		if preview.HasNextBuy {
			baseTarget := preview.NextBuyPrice
			if preview.NextBuyPercent.IsPositive() {
				baseTarget = calcDownTarget(preview.NextBuyReference, preview.NextBuyPercent)
			}
			snapshot.NextBuyPrice = preview.NextBuyPrice.StringFixed(4)
			snapshot.BaseBuyPrice = baseTarget.StringFixed(4)
			snapshot.NextBuyRef = preview.NextBuyReference.StringFixed(4)
			snapshot.NextBuyReason = preview.NextBuyReason
			snapshot.BuyTrailPercent = stock.TrailPercent.StringFixed(2)
			if preview.NextBuyPercent.IsPositive() {
				snapshot.NextBuyPercent = preview.NextBuyPercent.StringFixed(2)
			}
			if state.BuyArmed {
				snapshot.BuyTrailArmed = true
				lowest := decimalStringPtr(state.BuyLowest)
				if lowest != nil {
					trailingTarget := calcUpTarget(*lowest, stock.TrailPercent)
					snapshot.BuyTrailLowest = lowest.StringFixed(4)
					snapshot.BuyTrailTrigger = decimalMin(baseTarget, trailingTarget).StringFixed(4)
				}
			}
			if accountErr != nil {
				if e.cfg.Engine.CashLimit.IsPositive() {
					snapshot.BuyCapacityReason = "账户资金读取失败，无法校验现金使用上限，已禁止买入"
					snapshot.BuyCapacityBottlenecks = []string{"现金使用上限"}
					snapshot.BuyCapacityLimitTags = []CapacityTag{{Label: "现金使用上限", Kind: "funds"}}
				} else {
					snapshot.BuyCapacityReason = "账户资金读取失败，无法计算还能买几笔"
				}
			} else {
				capacity := estimateBuyCapacity(stock, position, preview.NextBuyPrice, funds, e.cfg.Engine.CashLimit, e.cfg.Engine.CashLimitMode)
				snapshot.RemainingBuyLots = capacity.RemainingLots
				snapshot.NextBuyCashNeeded = capacity.CashNeeded.StringFixed(4)
				if capacity.MarginUse.IsPositive() {
					snapshot.NextBuyMarginUse = capacity.MarginUse.StringFixed(4)
				} else {
					snapshot.NextBuyMarginUse = "0.0000"
				}
				snapshot.BuyCapacityReason = capacity.Reason
				snapshot.BuyCapacityDetails = capacity.Details
				snapshot.BuyCapacityBottlenecks = capacity.Bottlenecks
				snapshot.BuyCapacityDetailTags = capacity.DetailTags
				snapshot.BuyCapacityLimitTags = capacity.LimitTags
			}
		}
		if preview.HasNextSell {
			baseTarget := calcUpTarget(preview.NextSellReference, stock.SellPercent)
			snapshot.NextSellPrice = preview.NextSellPrice.StringFixed(4)
			snapshot.BaseSellPrice = baseTarget.StringFixed(4)
			snapshot.NextSellRef = preview.NextSellReference.StringFixed(4)
			snapshot.NextSellProfit = preview.NextSellProfit.StringFixed(4)
			snapshot.NextSellReason = preview.NextSellReason
			snapshot.SellTrailPercent = stock.TrailPercent.StringFixed(2)
			if state.SellArmed {
				snapshot.SellTrailArmed = true
				highest := decimalStringPtr(state.SellHighest)
				if highest != nil {
					trailingTarget := calcDownTarget(*highest, stock.TrailPercent)
					snapshot.SellTrailHighest = highest.StringFixed(4)
					snapshot.SellTrailTrigger = decimalMax(baseTarget, trailingTarget).StringFixed(4)
				}
			}
		}

		_ = e.state.Update(stock.Symbol, func(s *SymbolState) {
			s.LastDecision = decision.Reason
		})

		if stock.Enabled {
			if preview.HasNextBuy && snapshot.RemainingBuyLots == 0 {
				summary.LimitedSymbols = append(summary.LimitedSymbols, stock.Symbol)
			}
		}

		if decision.Action == ActionNone {
			snapshots = append(snapshots, snapshot)
			continue
		}

		if err := e.placeOrder(reqCtx, stock, position, price, decision, funds, accountErr); err != nil {
			e.logger.Printf("%s 下单失败: %v", stock.Symbol, err)
			_ = e.state.Update(stock.Symbol, func(s *SymbolState) {
				s.LastError = err.Error()
				s.LastDecision = decision.Reason
			})
			state = e.state.Get(stock.Symbol)
			snapshot.LastError = state.LastError
			snapshot.LastOrderStatus = state.LastOrderStatus
			snapshot.LastOrderMsg = state.LastOrderMsg
		} else {
			state = e.state.Get(stock.Symbol)
			if state.Pending != nil {
				snapshot.PendingOrderID = state.Pending.OrderID
				snapshot.PendingSide = state.Pending.Side
				snapshot.PendingPrice = state.Pending.SubmittedPrice
				snapshot.PendingAt = &state.Pending.SubmittedAt
				snapshot.PendingTimeoutLeft = formatTimeoutLeft(time.Until(state.Pending.SubmittedAt.Add(e.cfg.Engine.OrderTimeout)))
			}
			snapshot.LastOrderStatus = state.LastOrderStatus
			snapshot.LastOrderMsg = state.LastOrderMsg
			snapshot.Decision = state.LastDecision
		}

		snapshots = append(snapshots, snapshot)
	}

	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].Symbol < snapshots[j].Symbol
	})

	e.statusMu.Lock()
	e.status.LastUpdatedAt = time.Now().UTC()
	e.status.ConfigPath = e.cfg.FilePath
	e.status.AccessToken = e.auth.Snapshot()
	e.status.Market = marketClock
	e.status.CashLimit = cashLimitSnapshot
	e.status.Summary = summary
	e.status.AccountBalances = accountSnapshots
	if accountErr != nil {
		e.status.AccountError = accountErr.Error()
	} else {
		e.status.AccountError = ""
	}
	e.status.Symbols = snapshots
	e.statusMu.Unlock()
	return nil
}

func (e *Engine) refreshAccessTokenIfNeeded(ctx context.Context) error {
	if e.auth == nil {
		return nil
	}

	refreshed, err := e.auth.EnsureValid(ctx)
	if err != nil {
		return fmt.Errorf("自动刷新 Access Token 失败: %w", err)
	}
	if !refreshed {
		return nil
	}

	var limiter *TradeLimiter
	if e.client != nil {
		limiter = e.client.tradeLimiter
	}
	client, err := NewLongPortClientWithLimiterAndLogger(e.cfg, limiter, e.logger)
	if err != nil {
		return fmt.Errorf("Access Token 刷新后重建客户端失败: %w", err)
	}

	oldClient := e.client
	e.client = client
	if oldClient != nil {
		if err := oldClient.Close(); err != nil {
			e.logger.Printf("关闭旧 LongPort 客户端失败: %v", err)
		}
	}
	return nil
}

func (e *Engine) rebuildClientIfSessionAuthFailed() error {
	if e == nil || e.client == nil || !e.client.HasSessionAuthFailure() {
		return nil
	}
	return e.rebuildClientAfterSessionAuthFailure()
}

func (e *Engine) rebuildClientAfterSessionAuthFailure() error {
	var limiter *TradeLimiter
	if e.client != nil {
		limiter = e.client.tradeLimiter
	}
	client, err := NewLongPortClientWithLimiterAndLogger(e.cfg, limiter, e.logger)
	if err != nil {
		return fmt.Errorf("LongPort 会话失效后重建客户端失败: %w", err)
	}

	oldClient := e.client
	e.client = client
	if oldClient != nil {
		if err := oldClient.Close(); err != nil {
			e.logger.Printf("关闭失效 LongPort 客户端失败: %v", err)
		}
	}
	e.logger.Printf("LongPort 会话失效，已重建客户端")
	return nil
}

func (e *Engine) reloadConfigIfChanged(ctx context.Context) {
	if e == nil || e.cfg == nil || e.cfg.FilePath == "" {
		return
	}
	stat, err := os.Stat(e.cfg.FilePath)
	if err != nil {
		e.logger.Printf("检查配置文件失败，继续使用当前配置: %v", err)
		return
	}
	if !e.cfgModTime.IsZero() && !stat.ModTime().After(e.cfgModTime) {
		return
	}

	nextCfg, err := LoadConfig(e.cfg.FilePath)
	if err != nil {
		e.logger.Printf("重新加载配置失败，继续使用当前配置: %v", err)
		return
	}
	if nextCfg.Engine.StateFile != e.cfg.Engine.StateFile {
		e.logger.Printf("检测到 engine.state_file 变化，运行中不切换状态文件，继续使用当前配置")
		e.cfgModTime = stat.ModTime()
		return
	}
	if nextCfg.Server.Address() != e.cfg.Server.Address() {
		e.logger.Printf("检测到 server 地址变化，当前进程不会热切换监听地址: %s -> %s", e.cfg.Server.Address(), nextCfg.Server.Address())
	}
	if err := e.ensureStaticInfo(ctx, nextCfg); err != nil {
		e.logger.Printf("配置已读取但静态信息校验失败，继续使用当前配置: %v", err)
		return
	}

	oldCfg := e.cfg
	e.cfg = nextCfg
	e.cfgModTime = stat.ModTime()
	e.statusMu.Lock()
	e.status.DryRun = nextCfg.Engine.DryRun
	e.status.ListenAddress = oldCfg.Server.Address()
	e.status.ConfigPath = nextCfg.FilePath
	reloadedAt := time.Now().UTC()
	e.status.ConfigReloadedAt = &reloadedAt
	e.statusMu.Unlock()
	e.logger.Printf("检测到配置文件变化，已热加载: %s", nextCfg.FilePath)

	if tradeLimiterNeedsReload(oldCfg.Engine, nextCfg.Engine) {
		e.client.tradeLimiter = NewTradeLimiter(nextCfg.Engine.TradeAPIWindow, nextCfg.Engine.TradeAPIMaxCalls, nextCfg.Engine.TradeAPIMinGap)
	}
}

func (e *Engine) ensureStaticInfo(ctx context.Context, cfg *Config) error {
	missing := make([]string, 0)
	for _, stock := range cfg.Stocks {
		if !stock.Enabled {
			continue
		}
		if e.static[stock.Symbol] == nil {
			missing = append(missing, stock.Symbol)
		}
	}
	if len(missing) > 0 {
		items, err := e.client.StaticInfo(ctx, missing)
		if err != nil {
			return fmt.Errorf("读取新增股票静态信息失败: %w", err)
		}
		for _, item := range items {
			if item != nil {
				e.static[item.Symbol] = item
			}
		}
	}
	for _, stock := range cfg.Stocks {
		if !stock.Enabled {
			continue
		}
		info := e.static[stock.Symbol]
		if info == nil {
			return fmt.Errorf("未获取到 %s 的静态信息", stock.Symbol)
		}
		if info.LotSize > 0 && stock.OrderQuantity%int64(info.LotSize) != 0 {
			return fmt.Errorf("%s: 每笔数量 %d 不是 lot size %d 的整数倍", stock.Symbol, stock.OrderQuantity, info.LotSize)
		}
	}
	return nil
}

func tradeLimiterNeedsReload(oldCfg, newCfg EngineConfig) bool {
	return oldCfg.TradeAPIWindow != newCfg.TradeAPIWindow ||
		oldCfg.TradeAPIMaxCalls != newCfg.TradeAPIMaxCalls ||
		oldCfg.TradeAPIMinGap != newCfg.TradeAPIMinGap
}

func formatTimeoutLeft(d time.Duration) string {
	if d <= 0 {
		return "已超时"
	}
	totalSeconds := int64(d.Round(time.Second).Seconds())
	minutes := totalSeconds / 60
	seconds := totalSeconds % 60
	if minutes > 0 {
		return fmt.Sprintf("%dm%02ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

func isActiveOrderStatus(status lbtrade.OrderStatus) bool {
	switch status {
	case lbtrade.OrderNotReported,
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
		lbtrade.OrderPendingCancelStatus:
		return true
	default:
		return false
	}
}

func formatDisplayTime(ts time.Time, loc *time.Location) string {
	if loc == nil {
		return ts.UTC().Format("2006-01-02 15:04:05 UTC")
	}
	return ts.In(loc).Format("2006-01-02 15:04:05")
}
