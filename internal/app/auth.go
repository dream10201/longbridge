package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	lbconfig "github.com/longbridge/openapi-go/config"
)

const (
	longbridgeAccessTokenKey = "LONGBRIDGE_ACCESS_TOKEN"
	longportAccessTokenKey   = "LONGPORT_ACCESS_TOKEN"
	defaultRefreshHTTPURL    = "https://openapi.longbridge.com"
)

type AccessTokenManager struct {
	enabled       bool
	refreshBefore time.Duration
	envFile       string
	httpURL       string
	appKey        string
	httpClient    *http.Client
	logger        *log.Logger

	mu               sync.Mutex
	lastCheckedAt    time.Time
	lastRefreshAt    *time.Time
	lastRefreshState string
	lastRefreshMsg   string
}

type refreshAccessTokenEnvelope struct {
	Code    int                      `json:"code"`
	Message string                   `json:"message"`
	Data    refreshAccessTokenResult `json:"data"`
}

type refreshAccessTokenResult struct {
	Token       string `json:"token"`
	AccessToken string `json:"access_token"`
	ExpiredAt   string `json:"expired_at"`
}

type accessTokenClaims struct {
	Exp int64 `json:"exp"`
}

type AccessTokenStatus struct {
	Enabled            bool       `json:"enabled"`
	EnvFile            string     `json:"env_file,omitempty"`
	RefreshBefore      string     `json:"refresh_before,omitempty"`
	LastCheckedAt      *time.Time `json:"last_checked_at,omitempty"`
	HasToken           bool       `json:"has_token"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
	RefreshDueAt       *time.Time `json:"refresh_due_at,omitempty"`
	RefreshDueNow      bool       `json:"refresh_due_now"`
	LastRefreshAt      *time.Time `json:"last_refresh_at,omitempty"`
	LastRefreshState   string     `json:"last_refresh_state,omitempty"`
	LastRefreshMessage string     `json:"last_refresh_message,omitempty"`
}

func NewAccessTokenManager(cfg *Config, logger *log.Logger) (*AccessTokenManager, error) {
	if cfg == nil {
		return nil, fmt.Errorf("配置不能为空")
	}

	sdkCfg, err := lbconfig.New()
	if err != nil {
		return nil, fmt.Errorf("初始化 LongPort SDK 配置失败: %w", err)
	}
	httpURL := sdkCfg.HttpURL
	if httpURL == "" {
		httpURL = defaultRefreshHTTPURL
	}

	timeout := sdkCfg.HTTPTimeout
	if timeout <= 0 {
		timeout = cfg.Engine.QuoteRequestTimeout
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}

	return &AccessTokenManager{
		enabled:       cfg.Engine.AccessTokenAutoRefresh,
		refreshBefore: cfg.Engine.AccessTokenRefreshBefore,
		envFile:       cfg.Engine.AccessTokenEnvFile,
		httpURL:       strings.TrimRight(httpURL, "/"),
		appKey:        strings.TrimSpace(sdkCfg.AppKey),
		httpClient:    newProxyAwareHTTPClient(timeout),
		logger:        logger,
	}, nil
}

func (m *AccessTokenManager) ValidateConfiguredToken() error {
	if m == nil || !m.enabled {
		return nil
	}

	token := strings.TrimSpace(currentAccessToken())
	if token == "" {
		return fmt.Errorf("未找到 Access Token")
	}
	if _, err := parseAccessTokenExpiry(token); err != nil {
		return fmt.Errorf("当前 Access Token 无法用于自动刷新: %w", err)
	}
	return nil
}

func (m *AccessTokenManager) EnsureValid(ctx context.Context) (bool, error) {
	if m == nil || !m.enabled {
		return false, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	m.lastCheckedAt = now

	token := strings.TrimSpace(currentAccessToken())
	if token == "" {
		m.lastRefreshState = "missing"
		m.lastRefreshMsg = "未找到 Access Token"
		return false, nil
	}

	expiresAt, err := parseAccessTokenExpiry(token)
	if err != nil {
		m.lastRefreshState = "error"
		m.lastRefreshMsg = fmt.Sprintf("无法解析当前 Access Token 到期时间: %v", err)
		return false, fmt.Errorf("无法解析当前 Access Token 到期时间: %w", err)
	}
	if time.Until(expiresAt) > m.refreshBefore {
		m.lastRefreshState = "ready"
		m.lastRefreshMsg = "当前 token 尚未进入刷新窗口"
		return false, nil
	}

	refreshed, err := m.refreshAccessToken(ctx, token, expiresAt)
	if err != nil {
		failedAt := time.Now().UTC()
		m.lastRefreshAt = &failedAt
		m.lastRefreshState = "error"
		m.lastRefreshMsg = err.Error()
		return false, err
	}

	newToken := refreshed.TokenValue()
	os.Setenv(longbridgeAccessTokenKey, newToken)
	os.Setenv(longportAccessTokenKey, newToken)

	if m.envFile != "" {
		if err := writeAccessTokenEnvFile(m.envFile, newToken); err != nil {
			m.logf("Access Token 已刷新，但写回 %s 失败: %v", m.envFile, err)
		}
	}

	newExpiresAt, parseErr := parseRefreshExpiredAt(refreshed)
	refreshedAt := time.Now().UTC()
	m.lastRefreshAt = &refreshedAt
	if parseErr != nil {
		m.lastRefreshState = "ok"
		m.lastRefreshMsg = "Access Token 已刷新，但解析新到期时间失败"
		m.logf("Access Token 已刷新，但解析新到期时间失败: %v", parseErr)
	} else {
		m.lastRefreshState = "ok"
		m.lastRefreshMsg = fmt.Sprintf("Access Token 已刷新，新到期时间 %s", newExpiresAt.UTC().Format(time.RFC3339))
		m.logf("Access Token 已自动刷新，新到期时间 %s", newExpiresAt.UTC().Format(time.RFC3339))
	}
	return true, nil
}

func (m *AccessTokenManager) Snapshot() AccessTokenStatus {
	if m == nil {
		return AccessTokenStatus{}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	status := AccessTokenStatus{
		Enabled:            m.enabled,
		EnvFile:            m.envFile,
		RefreshBefore:      m.refreshBefore.String(),
		HasToken:           strings.TrimSpace(currentAccessToken()) != "",
		LastRefreshState:   m.lastRefreshState,
		LastRefreshMessage: m.lastRefreshMsg,
	}
	if !m.lastCheckedAt.IsZero() {
		checkedAt := m.lastCheckedAt
		status.LastCheckedAt = &checkedAt
	}
	if m.lastRefreshAt != nil && !m.lastRefreshAt.IsZero() {
		refreshedAt := *m.lastRefreshAt
		status.LastRefreshAt = &refreshedAt
	}

	token := strings.TrimSpace(currentAccessToken())
	if token == "" {
		return status
	}

	expiresAt, err := parseAccessTokenExpiry(token)
	if err != nil {
		if status.LastRefreshState == "" {
			status.LastRefreshState = "error"
			status.LastRefreshMessage = fmt.Sprintf("当前 token 无法解析过期时间: %v", err)
		}
		return status
	}

	expiresAt = expiresAt.UTC()
	status.ExpiresAt = &expiresAt
	refreshDueAt := expiresAt.Add(-m.refreshBefore)
	status.RefreshDueAt = &refreshDueAt
	status.RefreshDueNow = !time.Now().UTC().Before(refreshDueAt)
	return status
}

func (m *AccessTokenManager) refreshAccessToken(ctx context.Context, token string, expiresAt time.Time) (*refreshAccessTokenResult, error) {
	params := url.Values{}
	params.Set("expired_at", expiresAt.UTC().Format(time.RFC3339))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.httpURL+"/v1/token/refresh?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("构造刷新 Access Token 请求失败: %w", err)
	}
	req.Header.Set("Authorization", token)
	if m.appKey != "" {
		req.Header.Set("x-api-key", m.appKey)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("刷新 Access Token 请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取刷新 Access Token 响应失败: %w", err)
	}

	var envelope refreshAccessTokenEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("解析刷新 Access Token 响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK || envelope.Code != 0 {
		message := strings.TrimSpace(envelope.Message)
		if message == "" {
			message = strings.TrimSpace(string(body))
		}
		return nil, fmt.Errorf("刷新 Access Token 失败: http=%d code=%d message=%s", resp.StatusCode, envelope.Code, message)
	}
	if strings.TrimSpace(envelope.Data.TokenValue()) == "" {
		return nil, fmt.Errorf("刷新 Access Token 失败: 响应中缺少 token")
	}
	return &envelope.Data, nil
}

func currentAccessToken() string {
	if value := strings.TrimSpace(os.Getenv(longbridgeAccessTokenKey)); value != "" {
		return value
	}
	return strings.TrimSpace(os.Getenv(longportAccessTokenKey))
}

func parseAccessTokenExpiry(token string) (time.Time, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) < 2 {
		return time.Time{}, fmt.Errorf("token 不是合法 JWT")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, fmt.Errorf("解码 JWT payload 失败: %w", err)
	}

	var claims accessTokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}, fmt.Errorf("解析 JWT payload 失败: %w", err)
	}
	if claims.Exp <= 0 {
		return time.Time{}, fmt.Errorf("JWT payload 缺少 exp")
	}
	return time.Unix(claims.Exp, 0).UTC(), nil
}

func parseRefreshExpiredAt(result *refreshAccessTokenResult) (time.Time, error) {
	if result == nil {
		return time.Time{}, fmt.Errorf("刷新结果为空")
	}
	if raw := strings.TrimSpace(result.ExpiredAt); raw != "" {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			return t.UTC(), nil
		}
		if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			return t.UTC(), nil
		}
	}
	return parseAccessTokenExpiry(result.TokenValue())
}

func (r *refreshAccessTokenResult) TokenValue() string {
	if r == nil {
		return ""
	}
	if token := strings.TrimSpace(r.Token); token != "" {
		return token
	}
	return strings.TrimSpace(r.AccessToken)
}

func writeAccessTokenEnvFile(path string, token string) error {
	absPath := path
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Clean(absPath)
	}

	content, err := os.ReadFile(absPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("读取 env 文件失败: %w", err)
	}

	updated, _ := upsertEnvValue(string(content), longbridgeAccessTokenKey, token, true)
	updated, _ = upsertEnvValue(updated, longportAccessTokenKey, token, false)
	if strings.TrimSpace(updated) == "" {
		updated = longbridgeAccessTokenKey + "=" + token + "\n"
	}

	if err := os.WriteFile(absPath, []byte(updated), 0600); err != nil {
		return fmt.Errorf("写入 env 文件失败: %w", err)
	}
	return nil
}

func upsertEnvValue(content string, key string, value string, appendIfMissing bool) (string, bool) {
	lines := strings.Split(content, "\n")
	found := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if after, ok := strings.CutPrefix(trimmed, "export "); ok {
			trimmed = strings.TrimSpace(after)
		}
		if !strings.HasPrefix(trimmed, key+"=") {
			continue
		}

		prefix := key + "="
		if strings.HasPrefix(strings.TrimSpace(line), "export ") {
			prefix = "export " + prefix
		}
		lines[i] = prefix + value
		found = true
	}

	if !found && appendIfMissing {
		if len(lines) == 1 && lines[0] == "" {
			lines[0] = key + "=" + value
			return strings.Join(lines, "\n"), false
		}
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
			lines = strings.Split(content, "\n")
		}
		lines = append(lines, key+"="+value)
	}
	return strings.Join(lines, "\n"), found
}

func (m *AccessTokenManager) logf(format string, args ...any) {
	if m == nil || m.logger == nil {
		return
	}
	m.logger.Printf(format, args...)
}
