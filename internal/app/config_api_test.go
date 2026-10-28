package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const configAPITestTOML = `
[server]
host = "127.0.0.1"
port = 20017

[engine]
dry_run = true
max_exposure = "1000"

[[stocks]]
symbol = "AAPL.US"
initial_price = "170.00"
sell_percent = "1.50"
trail_percent = "1.00"
buy_percent = "1.00"
min_profit = "5.00"
max_lots = 3
order_quantity = 10
remark = "snap-aapl"
`

func newConfigAPIEngine(t *testing.T) (*Engine, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(configAPITestTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	return &Engine{configPath: path, reloadRequests: make(chan struct{}, 1)}, path
}

func TestConfigAPIGetReturnsEffectiveValues(t *testing.T) {
	engine, path := newConfigAPIEngine(t)
	handler := NewHTTPHandler(engine, ServerConfig{})
	req := httptest.NewRequest(http.MethodGet, "/proxy/1/api/config", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var resp configResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Path != path {
		t.Fatalf("path = %q, want %q", resp.Path, path)
	}
	if resp.Config.Engine.PollInterval != "5s" || resp.Config.Engine.OrderTimeout != "1m30s" {
		t.Fatalf("defaults not filled: %+v", resp.Config.Engine)
	}
	if resp.Config.Stocks[0].Enabled == nil || !*resp.Config.Stocks[0].Enabled {
		t.Fatalf("enabled default not filled")
	}
}

func putConfig(t *testing.T, handler http.Handler, req configUpdateRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	httpReq := httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httpReq)
	return rec
}

func loadRaw(t *testing.T, handler http.Handler) rawConfig {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	var resp configResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp.Config
}

func TestConfigAPIPutWritesAndApplies(t *testing.T) {
	engine, path := newConfigAPIEngine(t)
	handler := NewHTTPHandler(engine, ServerConfig{})

	raw := loadRaw(t, handler)
	raw.Stocks[0].SellPercent = "2.5"
	raw.Engine.MaxExposure = "5000"
	rec := putConfig(t, handler, configUpdateRequest{Config: raw})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	select {
	case <-engine.reloadRequests:
	default:
		t.Fatal("expected a reload request to be queued")
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Stocks[0].SellPercent.String() != "2.5" || cfg.Engine.MaxExposure.String() != "5000" {
		t.Fatalf("file not updated: %+v", cfg.Stocks[0])
	}
	if len(cfg.Warnings) != 0 {
		t.Fatalf("rendered config produced warnings: %v", cfg.Warnings)
	}
}

func TestConfigAPIPutRejectsWithoutWriting(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(raw *rawConfig, req *configUpdateRequest)
		wantMsg string
	}{
		{"演练切真实需确认", func(raw *rawConfig, _ *configUpdateRequest) { raw.Engine.DryRun = false }, "明确确认"},
		{"追踪回撤不能超过卖出阈值", func(raw *rawConfig, _ *configUpdateRequest) { raw.Stocks[0].TrailPercent = "2" }, "trail_percent"},
		{"轮询不能过快", func(raw *rawConfig, _ *configUpdateRequest) { raw.Engine.PollInterval = "200ms" }, "poll_interval"},
		{"基础校验仍生效", func(raw *rawConfig, _ *configUpdateRequest) { raw.Stocks[0].Remark = "" }, "remark"},
		{"费用不能为负", func(raw *rawConfig, _ *configUpdateRequest) { raw.Fees.PlatformMin = "-1" }, "fees.platform_min"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			engine, path := newConfigAPIEngine(t)
			handler := NewHTTPHandler(engine, ServerConfig{})
			raw := loadRaw(t, handler)
			req := configUpdateRequest{Config: raw}
			tc.mutate(&req.Config, &req)
			rec := putConfig(t, handler, req)
			if rec.Code < 400 {
				t.Fatalf("expected rejection, got %d %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantMsg) {
				t.Fatalf("body %q does not mention %q", rec.Body.String(), tc.wantMsg)
			}
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != configAPITestTOML {
				t.Fatalf("config file modified after rejection:\n%s", body)
			}
		})
	}
}

func TestConfigAPIPutLiveWithConfirm(t *testing.T) {
	engine, path := newConfigAPIEngine(t)
	handler := NewHTTPHandler(engine, ServerConfig{})
	raw := loadRaw(t, handler)
	raw.Engine.DryRun = false
	rec := putConfig(t, handler, configUpdateRequest{Config: raw, ConfirmLive: true})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Engine.DryRun {
		t.Fatal("dry_run should be false after confirmed switch")
	}
}
