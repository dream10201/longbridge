package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/shopspring/decimal"
)

const (
	configApplyTimeout = 30 * time.Second
	configBodyLimit    = 1 << 20
)

type configResponse struct {
	Path     string           `json:"path"`
	Config   rawConfig        `json:"config"`
	LotSizes map[string]int32 `json:"lot_sizes"`
	Warnings []string         `json:"warnings,omitempty"`
}

type configUpdateRequest struct {
	Config      rawConfig `json:"config"`
	ConfirmLive bool      `json:"confirm_live"`
}

type configUpdateResponse struct {
	OK       bool     `json:"ok"`
	Warnings []string `json:"warnings,omitempty"`
}

func (e *Engine) handleConfigAPI(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		e.handleConfigGet(w)
	case http.MethodPut, http.MethodPost:
		e.handleConfigPut(w, r)
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeJSONError(w, http.StatusMethodNotAllowed, "仅支持 GET / PUT")
	}
}

func (e *Engine) handleConfigGet(w http.ResponseWriter) {
	path := e.ConfigPath()
	if path == "" {
		writeJSONError(w, http.StatusInternalServerError, "引擎未关联配置文件")
		return
	}
	body, err := os.ReadFile(path)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("读取配置文件失败: %v", err))
		return
	}
	var raw rawConfig
	if _, err := toml.Decode(string(body), &raw); err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("解析配置文件失败: %v", err))
		return
	}
	cfg, err := parseConfig(body, path)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("配置文件校验失败: %v", err))
		return
	}
	fillRawDefaults(&raw, cfg)
	resp := configResponse{Path: path, Config: raw, LotSizes: map[string]int32{}, Warnings: cfg.Warnings}
	for _, stock := range cfg.Stocks {
		if lot := e.LotSize(stock.Symbol); lot > 0 {
			resp.LotSizes[stock.Symbol] = lot
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (e *Engine) handleConfigPut(w http.ResponseWriter, r *http.Request) {
	path := e.ConfigPath()
	if path == "" {
		writeJSONError(w, http.StatusInternalServerError, "引擎未关联配置文件")
		return
	}
	var req configUpdateRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, configBodyLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("请求体不是合法 JSON: %v", err))
		return
	}

	previous, err := os.ReadFile(path)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("读取当前配置失败: %v", err))
		return
	}
	currentCfg, err := parseConfig(previous, path)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("当前配置文件无法解析: %v", err))
		return
	}

	rendered, err := renderConfigTOML(req.Config)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("生成配置失败: %v", err))
		return
	}
	nextCfg, err := parseConfig(rendered, path)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateConfigGuards(currentCfg, nextCfg, req.ConfirmLive); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	for _, stock := range nextCfg.Stocks {
		lot := e.LotSize(stock.Symbol)
		if stock.Enabled && lot > 0 && stock.OrderQuantity%int64(lot) != 0 {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("%s: 每笔数量 %d 不是每手 %d 股的整数倍", stock.Symbol, stock.OrderQuantity, lot))
			return
		}
	}

	if bytes.Equal(bytes.TrimSpace(rendered), bytes.TrimSpace(previous)) {
		writeJSON(w, http.StatusOK, configUpdateResponse{OK: true, Warnings: nextCfg.Warnings})
		return
	}
	if err := writeFileAtomic(path, rendered, 0o644); err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("写入配置文件失败: %v", err))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), configApplyTimeout)
	defer cancel()
	if err := e.ApplyConfig(ctx); err != nil {
		if rollbackErr := writeFileAtomic(path, previous, 0o644); rollbackErr != nil {
			writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("应用失败: %v;回滚配置文件也失败: %v", err, rollbackErr))
			return
		}
		writeJSONError(w, http.StatusUnprocessableEntity, fmt.Sprintf("引擎拒绝新配置,文件已回滚: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, configUpdateResponse{OK: true, Warnings: nextCfg.Warnings})
}

// ConfigPath 返回引擎启动时加载的配置文件路径;热加载不会改变它。
func (e *Engine) ConfigPath() string {
	if e == nil {
		return ""
	}
	return e.configPath
}

// fillRawDefaults 把文件里省略的字段补成当前生效值,便于编辑界面完整展示。
func fillRawDefaults(raw *rawConfig, cfg *Config) {
	raw.Server.Host = cfg.Server.Host
	raw.Server.Port = cfg.Server.Port
	raw.Engine.PollInterval = firstNonEmpty(raw.Engine.PollInterval, cfg.Engine.PollInterval.String())
	raw.Engine.OrderTimeout = firstNonEmpty(raw.Engine.OrderTimeout, cfg.Engine.OrderTimeout.String())
	raw.Engine.StateFile = firstNonEmpty(raw.Engine.StateFile, "state.json")
	raw.Engine.MaxExposure = firstNonEmpty(raw.Engine.MaxExposure, cfg.Engine.MaxExposure.String())
	if raw.Engine.AccessTokenAutoRefresh == nil {
		value := cfg.Engine.AccessTokenAutoRefresh
		raw.Engine.AccessTokenAutoRefresh = &value
	}
	raw.Engine.AccessTokenRefreshBefore = firstNonEmpty(raw.Engine.AccessTokenRefreshBefore, cfg.Engine.AccessTokenRefreshBefore.String())
	raw.Engine.AccessTokenEnvFile = firstNonEmpty(raw.Engine.AccessTokenEnvFile, ".env")
	raw.Engine.TradeAPIWindow = firstNonEmpty(raw.Engine.TradeAPIWindow, cfg.Engine.TradeAPIWindow.String())
	raw.Engine.TradeAPIMaxCalls = cfg.Engine.TradeAPIMaxCalls
	raw.Engine.TradeAPIMinGap = firstNonEmpty(raw.Engine.TradeAPIMinGap, cfg.Engine.TradeAPIMinGap.String())
	raw.Engine.QuoteRequestTimeout = firstNonEmpty(raw.Engine.QuoteRequestTimeout, cfg.Engine.QuoteRequestTimeout.String())
	for i := range raw.Stocks {
		stock := &raw.Stocks[i]
		stock.Symbol = strings.TrimSpace(strings.ToUpper(stock.Symbol))
		stock.TrailPercent = firstNonEmpty(stock.TrailPercent, "1")
		if stock.Enabled == nil {
			value := true
			stock.Enabled = &value
		}
	}
}

func renderConfigTOML(raw rawConfig) ([]byte, error) {
	for i := range raw.Stocks {
		stock := &raw.Stocks[i]
		stock.Symbol = strings.TrimSpace(strings.ToUpper(stock.Symbol))
		stock.Remark = strings.TrimSpace(stock.Remark)
		if stock.Enabled == nil {
			value := true
			stock.Enabled = &value
		}
	}
	var buf bytes.Buffer
	encoder := toml.NewEncoder(&buf)
	encoder.Indent = ""
	if err := encoder.Encode(raw); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// validateConfigGuards 是编辑界面提交时的额外防线,比启动加载更严格:
// 阻止会直接造成亏损或失控下单的组合,以及未经确认从演练切到真实下单。
func validateConfigGuards(current, next *Config, confirmLive bool) error {
	if current != nil && current.Engine.DryRun && !next.Engine.DryRun && !confirmLive {
		return fmt.Errorf("从演练模式切换到真实下单需要明确确认")
	}
	if next.Engine.PollInterval < time.Second {
		return fmt.Errorf("engine.poll_interval 不能小于 1s")
	}
	if next.Engine.OrderTimeout < 10*time.Second {
		return fmt.Errorf("engine.order_timeout 不能小于 10s")
	}
	hundred := decimal.NewFromInt(100)
	for _, stock := range next.Stocks {
		if stock.SellPercent.GreaterThanOrEqual(hundred) || stock.BuyPercent.GreaterThanOrEqual(hundred) || stock.TrailPercent.GreaterThanOrEqual(hundred) {
			return fmt.Errorf("%s: 百分比阈值必须小于 100", stock.Symbol)
		}
		if stock.TrailPercent.GreaterThanOrEqual(stock.SellPercent) {
			return fmt.Errorf("%s: trail_percent 必须小于 sell_percent,否则回撤卖出可能低于买入价", stock.Symbol)
		}
		if stock.MaxLots > 100 {
			return fmt.Errorf("%s: max_lots 不能超过 100", stock.Symbol)
		}
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": message})
}
