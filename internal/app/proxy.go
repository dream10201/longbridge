package app

import (
	"net/http"
	"time"
)

func newProxyAwareHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyFromEnvironment
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}
