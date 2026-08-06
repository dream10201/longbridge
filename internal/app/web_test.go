package app

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPHandlerServesDashboardAssets(t *testing.T) {
	handler := NewHTTPHandler(&Engine{}, ServerConfig{})

	tests := []struct {
		path        string
		contentType string
	}{
		{path: "/", contentType: "text/html"},
		{path: "/api/status", contentType: "application/json"},
		{path: "/static/dashboard.css", contentType: "text/css"},
		{path: "/static/dashboard.js", contentType: "text/javascript"},
		{path: "/proxy/8080/", contentType: "text/html"},
		{path: "/proxy/8080/api/status", contentType: "application/json"},
		{path: "/proxy/8080/static/dashboard.css", contentType: "text/css"},
		{path: "/proxy/8080/static/dashboard.js", contentType: "text/javascript"},
	}

	for _, tt := range tests {
		req := httptest.NewRequest(http.MethodGet, tt.path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want %d", tt.path, rec.Code, http.StatusOK)
		}
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, tt.contentType) {
			t.Fatalf("%s: content-type = %q, want prefix %q", tt.path, got, tt.contentType)
		}
	}
}

func TestHTTPHandlerGzipCompressesStatus(t *testing.T) {
	handler := NewHTTPHandler(&Engine{}, ServerConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("content-encoding = %q, want gzip", got)
	}
	gz, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	body, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("read gzip body: %v", err)
	}
	if !strings.Contains(string(body), "\"symbols\"") {
		t.Fatalf("expected decompressed JSON status, got %q", string(body))
	}
}

func TestHTTPHandlerStatusETagReturns304(t *testing.T) {
	handler := NewHTTPHandler(&Engine{}, ServerConfig{})

	first := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	firstRec := httptest.NewRecorder()
	handler.ServeHTTP(firstRec, first)
	etag := firstRec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("expected ETag header on status response")
	}

	// 带上 If-None-Match 且声明支持 gzip：必须返回干净的 304(不能被 gzip 写坏)
	second := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	second.Header.Set("If-None-Match", etag)
	second.Header.Set("Accept-Encoding", "gzip")
	secondRec := httptest.NewRecorder()
	handler.ServeHTTP(secondRec, second)

	if secondRec.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want %d", secondRec.Code, http.StatusNotModified)
	}
	if secondRec.Body.Len() != 0 {
		t.Fatalf("expected empty 304 body, got %d bytes", secondRec.Body.Len())
	}
	if got := secondRec.Header().Get("Content-Encoding"); got == "gzip" {
		t.Fatalf("304 response must not be gzip-encoded, got %q", got)
	}
}

func TestHTTPHandlerDashboardUsesRelativeAssetPaths(t *testing.T) {
	handler := NewHTTPHandler(&Engine{}, ServerConfig{})
	req := httptest.NewRequest(http.MethodGet, "/proxy/8080/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, `href="/static/`) || strings.Contains(body, `src="/static/`) {
		t.Fatalf("expected dashboard to use relative static asset paths, got body containing absolute /static path")
	}
	if !strings.Contains(body, `href="static/dashboard.css"`) || !strings.Contains(body, `src="static/dashboard.js"`) {
		t.Fatalf("expected dashboard to reference relative static assets")
	}
}

func TestHTTPHandlerAuthHeader(t *testing.T) {
	cfg := ServerConfig{AuthSecret: "topsecret"}
	handler := NewHTTPHandler(&Engine{}, cfg)

	tests := []struct {
		name       string
		path       string
		headerVal  string
		wantStatus int
	}{
		{"无密钥拒绝", "/", "", http.StatusUnauthorized},
		{"密钥错误拒绝", "/api/status", "wrong", http.StatusUnauthorized},
		{"密钥正确放行", "/", "topsecret", http.StatusOK},
		{"healthz 免鉴权", "/healthz", "", http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.headerVal != "" {
				req.Header.Set(authHeaderName, tc.headerVal)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("%s: expected status %d, got %d", tc.path, tc.wantStatus, rec.Code)
			}
		})
	}
}

func TestHTTPHandlerAuthDisabledByDefault(t *testing.T) {
	handler := NewHTTPHandler(&Engine{}, ServerConfig{})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 when auth disabled, got %d", rec.Code)
	}
}
