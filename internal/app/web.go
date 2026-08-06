package app

import (
	"compress/gzip"
	"embed"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed web/dashboard.html web/dashboard.css web/dashboard.js
var dashboardAssets embed.FS

var dashboardTemplate = template.Must(template.New("dashboard.html").Funcs(template.FuncMap{
	"toJSON": func(v any) template.JS {
		data, err := json.Marshal(v)
		if err != nil {
			return template.JS("null")
		}
		return template.JS(data)
	},
}).ParseFS(dashboardAssets, "web/dashboard.html"))

func NewHTTPHandler(engine *Engine) http.Handler {
	mux := http.NewServeMux()
	staticAssets, err := fs.Sub(dashboardAssets, "web")
	if err != nil {
		panic(err)
	}
	staticHandler := http.FileServer(http.FS(staticAssets))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/api/status" || strings.HasSuffix(path, "/api/status") {
			body, err := json.Marshal(engine.Snapshot())
			if err != nil {
				http.Error(w, "序列化状态失败", http.StatusInternalServerError)
				return
			}
			// 弱 ETag：内容未变时返回 304，避免休市期间反复传输相同快照
			etag := fmt.Sprintf(`W/"%x"`, fnvSum(body))
			w.Header().Set("ETag", etag)
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			if r.Header.Get("If-None-Match") == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			_, _ = w.Write(body)
			return
		}
		if idx := strings.LastIndex(path, "/static/"); idx >= 0 {
			assetReq := r.Clone(r.Context())
			assetReq.URL.Path = "/" + strings.TrimPrefix(path[idx+len("/static/"):], "/")
			staticHandler.ServeHTTP(w, assetReq)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = dashboardTemplate.Execute(w, engine.Snapshot())
	})
	return gzipMiddleware(mux)
}

func fnvSum(data []byte) uint64 {
	h := fnv.New64a()
	_, _ = h.Write(data)
	return h.Sum64()
}

// gzipMiddleware 对支持 gzip 的客户端压缩响应；304/204 等无响应体的状态不压缩。
func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		gzw := &gzipResponseWriter{ResponseWriter: w}
		defer gzw.Close()
		next.ServeHTTP(gzw, r)
	})
}

type gzipResponseWriter struct {
	http.ResponseWriter
	gz          *gzip.Writer
	wroteHeader bool
}

func (w *gzipResponseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	if code == http.StatusNotModified || code == http.StatusNoContent {
		w.ResponseWriter.WriteHeader(code)
		return
	}
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Add("Vary", "Accept-Encoding")
	w.Header().Del("Content-Length")
	w.gz = gzip.NewWriter(w.ResponseWriter)
	w.ResponseWriter.WriteHeader(code)
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.gz != nil {
		return w.gz.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

func (w *gzipResponseWriter) Close() error {
	if w.gz != nil {
		return w.gz.Close()
	}
	return nil
}
