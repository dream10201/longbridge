package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	lbhttp "github.com/longbridge/openapi-go/http"
)

func TestParseAccessTokenExpiry(t *testing.T) {
	expiresAt := time.Date(2026, 4, 7, 12, 34, 56, 0, time.UTC)
	token := testJWT(t, expiresAt)

	got, err := parseAccessTokenExpiry(token)
	if err != nil {
		t.Fatalf("parseAccessTokenExpiry returned error: %v", err)
	}
	if !got.Equal(expiresAt) {
		t.Fatalf("expected %s, got %s", expiresAt, got)
	}
}

func TestAccessTokenManagerRefreshAndPersist(t *testing.T) {
	oldExpiresAt := time.Now().UTC().Add(30 * time.Minute).Truncate(time.Second)
	newExpiresAt := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	oldToken := testJWT(t, oldExpiresAt)
	newToken := testJWT(t, newExpiresAt)

	var gotAuthorization string
	var gotAPIKey string
	var gotExpiredAt string
	var gotSignature string
	var gotTimestamp string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		gotAPIKey = r.Header.Get("x-api-key")
		gotExpiredAt = r.URL.Query().Get("expired_at")
		gotSignature = r.Header.Get("x-api-signature")
		gotTimestamp = r.Header.Get("x-timestamp")

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":0,"message":"ok","data":{"token":"`+newToken+`","expired_at":"`+newExpiresAt.Format(time.RFC3339)+`"}}`)
	}))
	defer server.Close()

	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, ".env")
	if err := os.WriteFile(envPath, []byte("LONGBRIDGE_ACCESS_TOKEN="+oldToken+"\nOTHER=value\n"), 0600); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	t.Setenv(longbridgeAccessTokenKey, oldToken)
	t.Setenv(longportAccessTokenKey, "")

	apiClient, err := lbhttp.New(
		lbhttp.WithURL(server.URL),
		lbhttp.WithAppKey("test-app-key"),
		lbhttp.WithAppSecret("test-app-secret"),
		lbhttp.WithAccessToken(oldToken),
		lbhttp.WithClient(server.Client()),
	)
	if err != nil {
		t.Fatalf("new api client: %v", err)
	}

	manager := &AccessTokenManager{
		enabled:       true,
		refreshBefore: time.Hour,
		envFile:       envPath,
		apiClient:     apiClient,
		logger:        log.New(io.Discard, "", 0),
	}

	refreshed, err := manager.EnsureValid(context.Background())
	if err != nil {
		t.Fatalf("EnsureValid returned error: %v", err)
	}
	if !refreshed {
		t.Fatalf("expected token to be refreshed")
	}
	if gotAuthorization != oldToken {
		t.Fatalf("expected authorization header %q, got %q", oldToken, gotAuthorization)
	}
	if gotAPIKey != "test-app-key" {
		t.Fatalf("expected x-api-key header %q, got %q", "test-app-key", gotAPIKey)
	}
	if gotExpiredAt != oldExpiresAt.Format(time.RFC3339) {
		t.Fatalf("expected expired_at %q, got %q", oldExpiresAt.Format(time.RFC3339), gotExpiredAt)
	}
	if !strings.Contains(gotSignature, "Signature=") {
		t.Fatalf("expected signed request, got x-api-signature %q", gotSignature)
	}
	if gotTimestamp == "" {
		t.Fatalf("expected x-timestamp header to be set")
	}
	if got := os.Getenv(longbridgeAccessTokenKey); got != newToken {
		t.Fatalf("expected env token %q, got %q", newToken, got)
	}

	body, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	content := string(body)
	if !strings.Contains(content, "LONGBRIDGE_ACCESS_TOKEN="+newToken) {
		t.Fatalf("env file did not contain refreshed token: %s", content)
	}
	if !strings.Contains(content, "OTHER=value") {
		t.Fatalf("env file should preserve unrelated lines: %s", content)
	}
}

func TestAccessTokenManagerSkipsRefreshWhenExpiryFarAway(t *testing.T) {
	token := testJWT(t, time.Now().UTC().Add(24*time.Hour))
	t.Setenv(longbridgeAccessTokenKey, token)

	manager := &AccessTokenManager{
		enabled:       true,
		refreshBefore: time.Hour,
		logger:        log.New(io.Discard, "", 0),
	}

	refreshed, err := manager.EnsureValid(context.Background())
	if err != nil {
		t.Fatalf("EnsureValid returned error: %v", err)
	}
	if refreshed {
		t.Fatalf("expected no refresh when token is not near expiry")
	}
}

func TestAccessTokenManagerValidateConfiguredToken(t *testing.T) {
	t.Setenv(longbridgeAccessTokenKey, "not-a-jwt")

	manager := &AccessTokenManager{
		enabled: true,
		logger:  log.New(io.Discard, "", 0),
	}

	if err := manager.ValidateConfiguredToken(); err == nil {
		t.Fatalf("expected invalid token to be rejected")
	}
}

func TestAccessTokenManagerSnapshot(t *testing.T) {
	expiresAt := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	token := testJWT(t, expiresAt)
	t.Setenv(longbridgeAccessTokenKey, token)

	now := time.Now().UTC().Truncate(time.Second)
	manager := &AccessTokenManager{
		enabled:          true,
		refreshBefore:    time.Hour,
		envFile:          "/tmp/test.env",
		lastCheckedAt:    now,
		lastRefreshState: "ready",
		lastRefreshMsg:   "当前 token 尚未进入刷新窗口",
		logger:           log.New(io.Discard, "", 0),
	}

	status := manager.Snapshot()
	if !status.Enabled {
		t.Fatalf("expected enabled status")
	}
	if status.EnvFile != "/tmp/test.env" {
		t.Fatalf("unexpected env file: %s", status.EnvFile)
	}
	if !status.HasToken {
		t.Fatalf("expected token to be present")
	}
	if status.ExpiresAt == nil || !status.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("unexpected expires_at: %+v", status.ExpiresAt)
	}
	if status.RefreshDueAt == nil || !status.RefreshDueAt.Equal(expiresAt.Add(-time.Hour)) {
		t.Fatalf("unexpected refresh_due_at: %+v", status.RefreshDueAt)
	}
	if status.RefreshDueNow {
		t.Fatalf("expected token to not be due for refresh yet")
	}
	if status.LastRefreshState != "ready" {
		t.Fatalf("unexpected last refresh state: %s", status.LastRefreshState)
	}
}

func testJWT(t *testing.T, expiresAt time.Time) string {
	t.Helper()

	header, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	payload, err := json.Marshal(map[string]int64{"exp": expiresAt.Unix()})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}
