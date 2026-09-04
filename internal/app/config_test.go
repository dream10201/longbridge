package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shopspring/decimal"
)

func TestLoadConfigResolvesAccessTokenEnvFileRelativeToConfig(t *testing.T) {
	tempDir := t.TempDir()
	configDir := filepath.Join(tempDir, "configs")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}

	configPath := filepath.Join(configDir, "config.toml")
	content := `
[server]
host = "127.0.0.1"
port = 8080

[engine]
dry_run = true
access_token_env_file = "../secrets/.env.paper"

[[stocks]]
symbol = "AAPL.US"
initial_price = "100"
sell_percent = "1"
buy_percent = "1"
min_profit = "0"
use_margin = false
max_lots = 1
order_quantity = 1
remark = "longbridge-aapl"
enabled = true
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}

	want := filepath.Clean(filepath.Join(configDir, "../secrets/.env.paper"))
	if cfg.Engine.AccessTokenEnvFile != want {
		t.Fatalf("expected env path %q, got %q", want, cfg.Engine.AccessTokenEnvFile)
	}
}

func TestLoadConfigResolvesStateFileRelativeToConfig(t *testing.T) {
	tempDir := t.TempDir()
	configDir := filepath.Join(tempDir, "configs")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}

	configPath := filepath.Join(configDir, "config.toml")
	content := `
[server]
host = "127.0.0.1"
port = 8080

[engine]
dry_run = true
state_file = "../state/longbridge-state.json"

[[stocks]]
symbol = "AAPL.US"
initial_price = "100"
sell_percent = "1"
buy_percent = "1"
min_profit = "0"
use_margin = false
max_lots = 1
order_quantity = 1
remark = "longbridge-aapl"
enabled = true
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}

	want := filepath.Clean(filepath.Join(configDir, "../state/longbridge-state.json"))
	if cfg.Engine.StateFile != want {
		t.Fatalf("expected state path %q, got %q", want, cfg.Engine.StateFile)
	}
}

func TestLoadConfigParsesMaxExposure(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.toml")
	content := `
[server]
host = "127.0.0.1"
port = 8080

[engine]
dry_run = true
max_exposure = "1234.56"

[[stocks]]
symbol = "AAPL.US"
initial_price = "100"
sell_percent = "1"
buy_percent = "1"
min_profit = "0"
use_margin = false
max_lots = 1
order_quantity = 1
remark = "longbridge-aapl"
enabled = true
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}

	want := decimal.RequireFromString("1234.56")
	if !cfg.Engine.MaxExposure.Equal(want) {
		t.Fatalf("expected max exposure %s, got %s", want, cfg.Engine.MaxExposure)
	}
	if len(cfg.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", cfg.Warnings)
	}
}

func TestLoadConfigAcceptsLegacyCashLimitWithWarnings(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	content := `
[server]
port = 8080

[engine]
cash_limit = "500"
cash_limit_mode = "used"
unknown_key = 1

[[stocks]]
symbol = "AAPL.US"
initial_price = "100"
sell_percent = "1"
buy_percent = "1"
min_profit = "0"
max_margin = "0"
max_lots = 1
order_quantity = 1
remark = "longbridge-aapl"
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	if !cfg.Engine.MaxExposure.Equal(decimal.RequireFromString("500")) {
		t.Fatalf("expected legacy cash_limit to map to max_exposure, got %s", cfg.Engine.MaxExposure)
	}
	if len(cfg.Warnings) != 4 {
		t.Fatalf("expected 4 warnings, got %v", cfg.Warnings)
	}
}

func TestConfigValidateRejectsNegativeMaxExposure(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Host: "127.0.0.1",
			Port: 8080,
		},
		Engine: EngineConfig{
			PollInterval:             5,
			OrderTimeout:             5,
			AccessTokenRefreshBefore: 5,
			TradeAPIWindow:           5,
			TradeAPIMaxCalls:         1,
			TradeAPIMinGap:           1,
			MaxExposure:              decimal.RequireFromString("-1"),
		},
		Stocks: []StockConfig{{
			Symbol:        "AAPL.US",
			InitialPrice:  decimal.RequireFromString("100"),
			SellPercent:   decimal.RequireFromString("1"),
			BuyPercent:    decimal.RequireFromString("1"),
			MinProfit:     decimal.Zero,
			MaxLots:       1,
			OrderQuantity: 1,
			Remark:        "longbridge-aapl",
			Enabled:       true,
		}},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validate error for negative max exposure")
	}
}

func TestConfigValidateRejectsEnabledStockWithoutRemark(t *testing.T) {
	cfg := validTestConfig()
	cfg.Stocks[0].Remark = ""

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validate error for empty remark")
	}
}

func TestConfigValidateRejectsDuplicateEnabledRemark(t *testing.T) {
	cfg := validTestConfig()
	cfg.Stocks = append(cfg.Stocks, cfg.Stocks[0])
	cfg.Stocks[1].Symbol = "MSFT.US"

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validate error for duplicate remark")
	}
}

func validTestConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Host: "127.0.0.1",
			Port: 8080,
		},
		Engine: EngineConfig{
			PollInterval:             5,
			OrderTimeout:             5,
			AccessTokenRefreshBefore: 5,
			TradeAPIWindow:           5,
			TradeAPIMaxCalls:         1,
			TradeAPIMinGap:           1,
		},
		Stocks: []StockConfig{{
			Symbol:        "AAPL.US",
			InitialPrice:  decimal.RequireFromString("100"),
			SellPercent:   decimal.RequireFromString("1"),
			TrailPercent:  decimal.RequireFromString("1"),
			BuyPercent:    decimal.RequireFromString("1"),
			MinProfit:     decimal.Zero,
			MaxLots:       1,
			OrderQuantity: 1,
			Remark:        "longbridge-aapl",
			Enabled:       true,
		}},
	}
}
