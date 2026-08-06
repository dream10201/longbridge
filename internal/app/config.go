package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/shopspring/decimal"
)

type Config struct {
	FilePath string
	BaseDir  string
	Server   ServerConfig
	Engine   EngineConfig
	Stocks   []StockConfig
}

type ServerConfig struct {
	Host       string
	Port       int
	AuthSecret string
}

func (c ServerConfig) Address() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

type EngineConfig struct {
	PollInterval             time.Duration
	OrderTimeout             time.Duration
	StateFile                string
	DryRun                   bool
	CashLimit                decimal.Decimal
	CashLimitMode            string
	AccessTokenAutoRefresh   bool
	AccessTokenRefreshBefore time.Duration
	AccessTokenEnvFile       string
	TradeAPIWindow           time.Duration
	TradeAPIMaxCalls         int
	TradeAPIMinGap           time.Duration
	QuoteRequestTimeout      time.Duration
}

type StockConfig struct {
	Symbol        string
	InitialPrice  decimal.Decimal
	SellPercent   decimal.Decimal
	TrailPercent  decimal.Decimal
	BuyPercent    decimal.Decimal
	MinProfit     decimal.Decimal
	UseMargin     bool
	MaxMargin     decimal.Decimal
	MaxLots       int64
	OrderQuantity int64
	Remark        string
	Enabled       bool
}

type rawConfig struct {
	Server rawServerConfig  `toml:"server"`
	Engine rawEngineConfig  `toml:"engine"`
	Stocks []rawStockConfig `toml:"stocks"`
}

type rawServerConfig struct {
	Host       string `toml:"host"`
	Port       int    `toml:"port"`
	AuthSecret string `toml:"auth_secret"`
}

type rawEngineConfig struct {
	PollInterval             string `toml:"poll_interval"`
	OrderTimeout             string `toml:"order_timeout"`
	StateFile                string `toml:"state_file"`
	DryRun                   bool   `toml:"dry_run"`
	CashLimit                string `toml:"cash_limit"`
	CashLimitMode            string `toml:"cash_limit_mode"`
	AccessTokenAutoRefresh   *bool  `toml:"access_token_auto_refresh"`
	AccessTokenRefreshBefore string `toml:"access_token_refresh_before"`
	AccessTokenEnvFile       string `toml:"access_token_env_file"`
	TradeAPIWindow           string `toml:"trade_api_window"`
	TradeAPIMaxCalls         int    `toml:"trade_api_max_calls"`
	TradeAPIMinGap           string `toml:"trade_api_min_gap"`
	QuoteRequestTimeout      string `toml:"quote_request_timeout"`
}

type rawStockConfig struct {
	Symbol        string `toml:"symbol"`
	InitialPrice  string `toml:"initial_price"`
	SellPercent   string `toml:"sell_percent"`
	TrailPercent  string `toml:"trail_percent"`
	BuyPercent    string `toml:"buy_percent"`
	MinProfit     string `toml:"min_profit"`
	UseMargin     bool   `toml:"use_margin"`
	MaxMargin     string `toml:"max_margin"`
	MaxLots       int64  `toml:"max_lots"`
	OrderQuantity int64  `toml:"order_quantity"`
	Remark        string `toml:"remark"`
	Enabled       *bool  `toml:"enabled"`
}

func LoadConfig(path string) (*Config, error) {
	cleanPath := filepath.Clean(path)
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("解析配置文件路径失败: %w", err)
	}

	body, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var raw rawConfig
	if err := toml.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("解析 TOML 失败: %w", err)
	}

	cfg := &Config{
		FilePath: absPath,
		BaseDir:  filepath.Dir(absPath),
		Server: ServerConfig{
			Host:       firstNonEmpty(raw.Server.Host, "0.0.0.0"),
			Port:       raw.Server.Port,
			AuthSecret: strings.TrimSpace(raw.Server.AuthSecret),
		},
		Engine: EngineConfig{
			StateFile:          firstNonEmpty(raw.Engine.StateFile, "state.json"),
			DryRun:             raw.Engine.DryRun,
			CashLimitMode:      firstNonEmpty(raw.Engine.CashLimitMode, CashLimitModeUsed),
			AccessTokenEnvFile: firstNonEmpty(raw.Engine.AccessTokenEnvFile, ".env"),
		},
	}
	cfg.Engine.CashLimitMode = strings.ToLower(strings.TrimSpace(cfg.Engine.CashLimitMode))

	cfg.Engine.StateFile = resolvePathFromBase(cfg.BaseDir, cfg.Engine.StateFile)
	cfg.Engine.AccessTokenEnvFile = resolvePathFromBase(cfg.BaseDir, cfg.Engine.AccessTokenEnvFile)

	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}

	if cfg.Engine.PollInterval, err = parseDurationOrDefault(raw.Engine.PollInterval, 5*time.Second); err != nil {
		return nil, fmt.Errorf("engine.poll_interval 无效: %w", err)
	}
	if cfg.Engine.OrderTimeout, err = parseDurationOrDefault(raw.Engine.OrderTimeout, 90*time.Second); err != nil {
		return nil, fmt.Errorf("engine.order_timeout 无效: %w", err)
	}
	if cfg.Engine.AccessTokenRefreshBefore, err = parseDurationOrDefault(raw.Engine.AccessTokenRefreshBefore, time.Hour); err != nil {
		return nil, fmt.Errorf("engine.access_token_refresh_before 无效: %w", err)
	}
	if cfg.Engine.TradeAPIWindow, err = parseDurationOrDefault(raw.Engine.TradeAPIWindow, 30*time.Second); err != nil {
		return nil, fmt.Errorf("engine.trade_api_window 无效: %w", err)
	}
	if cfg.Engine.TradeAPIMinGap, err = parseDurationOrDefault(raw.Engine.TradeAPIMinGap, 25*time.Millisecond); err != nil {
		return nil, fmt.Errorf("engine.trade_api_min_gap 无效: %w", err)
	}
	if cfg.Engine.QuoteRequestTimeout, err = parseDurationOrDefault(raw.Engine.QuoteRequestTimeout, 15*time.Second); err != nil {
		return nil, fmt.Errorf("engine.quote_request_timeout 无效: %w", err)
	}
	if strings.TrimSpace(raw.Engine.CashLimit) != "" {
		if cfg.Engine.CashLimit, err = parseDecimal(raw.Engine.CashLimit, "cash_limit"); err != nil {
			return nil, fmt.Errorf("engine.cash_limit 无效: %w", err)
		}
	}
	cfg.Engine.TradeAPIMaxCalls = raw.Engine.TradeAPIMaxCalls
	if cfg.Engine.TradeAPIMaxCalls == 0 {
		cfg.Engine.TradeAPIMaxCalls = 30
	}
	cfg.Engine.AccessTokenAutoRefresh = true
	if raw.Engine.AccessTokenAutoRefresh != nil {
		cfg.Engine.AccessTokenAutoRefresh = *raw.Engine.AccessTokenAutoRefresh
	}

	if len(raw.Stocks) == 0 {
		return nil, fmt.Errorf("至少需要配置一只股票")
	}

	cfg.Stocks = make([]StockConfig, 0, len(raw.Stocks))
	seen := make(map[string]struct{}, len(raw.Stocks))
	for _, item := range raw.Stocks {
		symbol := strings.TrimSpace(strings.ToUpper(item.Symbol))
		if symbol == "" {
			return nil, fmt.Errorf("股票 symbol 不能为空")
		}
		if _, ok := seen[symbol]; ok {
			return nil, fmt.Errorf("重复股票配置: %s", symbol)
		}
		seen[symbol] = struct{}{}

		initialPrice, err := parseDecimal(item.InitialPrice, "initial_price")
		if err != nil {
			return nil, fmt.Errorf("%s: %w", symbol, err)
		}
		sellPercent, err := parseDecimal(item.SellPercent, "sell_percent")
		if err != nil {
			return nil, fmt.Errorf("%s: %w", symbol, err)
		}
		trailPercent := decimal.NewFromInt(1)
		if strings.TrimSpace(item.TrailPercent) != "" {
			trailPercent, err = parseDecimal(item.TrailPercent, "trail_percent")
			if err != nil {
				return nil, fmt.Errorf("%s: %w", symbol, err)
			}
		}
		buyPercent, err := parseDecimal(item.BuyPercent, "buy_percent")
		if err != nil {
			return nil, fmt.Errorf("%s: %w", symbol, err)
		}
		minProfit, err := parseDecimal(item.MinProfit, "min_profit")
		if err != nil {
			return nil, fmt.Errorf("%s: %w", symbol, err)
		}
		maxMargin := decimal.Zero
		if strings.TrimSpace(item.MaxMargin) != "" {
			maxMargin, err = parseDecimal(item.MaxMargin, "max_margin")
			if err != nil {
				return nil, fmt.Errorf("%s: %w", symbol, err)
			}
		}

		enabled := true
		if item.Enabled != nil {
			enabled = *item.Enabled
		}
		cfg.Stocks = append(cfg.Stocks, StockConfig{
			Symbol:        symbol,
			InitialPrice:  initialPrice,
			SellPercent:   sellPercent,
			TrailPercent:  trailPercent,
			BuyPercent:    buyPercent,
			MinProfit:     minProfit,
			UseMargin:     item.UseMargin,
			MaxMargin:     maxMargin,
			MaxLots:       item.MaxLots,
			OrderQuantity: item.OrderQuantity,
			Remark:        strings.TrimSpace(item.Remark),
			Enabled:       enabled,
		})
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) Validate() error {
	if c.Server.Host == "" {
		return fmt.Errorf("server.host 不能为空")
	}
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port 超出范围")
	}
	if c.Engine.PollInterval <= 0 {
		return fmt.Errorf("engine.poll_interval 必须大于 0")
	}
	if c.Engine.OrderTimeout <= 0 {
		return fmt.Errorf("engine.order_timeout 必须大于 0")
	}
	if c.Engine.AccessTokenRefreshBefore <= 0 {
		return fmt.Errorf("engine.access_token_refresh_before 必须大于 0")
	}
	if c.Engine.TradeAPIWindow <= 0 || c.Engine.TradeAPIMaxCalls <= 0 || c.Engine.TradeAPIMinGap <= 0 {
		return fmt.Errorf("trade API 限频参数必须大于 0")
	}
	if c.Engine.CashLimit.IsNegative() {
		return fmt.Errorf("engine.cash_limit 不能小于 0")
	}
	switch c.Engine.CashLimitMode {
	case "", CashLimitModeUsed, CashLimitModeProjected:
	default:
		return fmt.Errorf("engine.cash_limit_mode 只能是 %q 或 %q", CashLimitModeUsed, CashLimitModeProjected)
	}
	remarks := make(map[string]string, len(c.Stocks))
	for _, stock := range c.Stocks {
		if stock.MaxLots <= 0 {
			return fmt.Errorf("%s: max_lots 必须大于 0", stock.Symbol)
		}
		if stock.OrderQuantity <= 0 {
			return fmt.Errorf("%s: order_quantity 必须大于 0", stock.Symbol)
		}
		if !stock.InitialPrice.IsPositive() {
			return fmt.Errorf("%s: initial_price 必须大于 0", stock.Symbol)
		}
		if stock.SellPercent.LessThanOrEqual(decimal.Zero) {
			return fmt.Errorf("%s: sell_percent 必须大于 0", stock.Symbol)
		}
		if stock.TrailPercent.LessThanOrEqual(decimal.Zero) {
			return fmt.Errorf("%s: trail_percent 必须大于 0", stock.Symbol)
		}
		if stock.BuyPercent.LessThanOrEqual(decimal.Zero) {
			return fmt.Errorf("%s: buy_percent 必须大于 0", stock.Symbol)
		}
		if stock.MinProfit.IsNegative() {
			return fmt.Errorf("%s: min_profit 不能小于 0", stock.Symbol)
		}
		if stock.MaxMargin.IsNegative() {
			return fmt.Errorf("%s: max_margin 不能小于 0", stock.Symbol)
		}
		if !strings.HasSuffix(stock.Symbol, ".US") {
			return fmt.Errorf("%s: 当前仅支持美股代码，示例 AAPL.US", stock.Symbol)
		}
		if stock.Enabled {
			remark := strings.TrimSpace(stock.Remark)
			if remark == "" {
				return fmt.Errorf("%s: 启用股票必须配置唯一 remark，用于识别本程序提交的挂单", stock.Symbol)
			}
			if existing := remarks[remark]; existing != "" {
				return fmt.Errorf("%s 和 %s: 启用股票 remark 不能重复: %s", existing, stock.Symbol, remark)
			}
			remarks[remark] = stock.Symbol
		}
	}
	return nil
}

func parseDurationOrDefault(raw string, fallback time.Duration) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	return time.ParseDuration(raw)
}

func parseDecimal(raw string, field string) (decimal.Decimal, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return decimal.Zero, fmt.Errorf("%s 不能为空", field)
	}
	value, err := decimal.NewFromString(raw)
	if err != nil {
		return decimal.Zero, fmt.Errorf("%s 不是合法数字", field)
	}
	return value, nil
}

func resolvePathFromBase(baseDir string, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	if baseDir == "" {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(baseDir, path))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
