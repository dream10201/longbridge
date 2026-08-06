package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // 内嵌时区数据,distroless/scratch 基础镜像下 TZ 也能生效

	"longbridge/internal/app"
)

func main() {
	configPath := flag.String("config", "config.toml", "配置文件路径")
	healthcheck := flag.Bool("healthcheck", false, "健康检查模式:请求 health-url,HTTP 200 时退出码 0(供 Docker HEALTHCHECK 用,distroless 镜像无 curl)")
	healthURL := flag.String("health-url", "http://127.0.0.1:20017/healthz", "健康检查地址")
	flag.Parse()

	if *healthcheck {
		os.Exit(runHealthcheck(*healthURL))
	}

	cfg, err := app.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	logger := log.New(os.Stdout, "[longbridge] ", log.LstdFlags|log.Lmicroseconds)

	engine, err := app.NewEngine(cfg, logger)
	if err != nil {
		log.Fatalf("初始化引擎失败: %v", err)
	}
	defer engine.Close()

	server := &http.Server{
		Addr:              cfg.Server.Address(),
		Handler:           app.NewHTTPHandler(engine, cfg.Server),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 2)

	go func() {
		errCh <- engine.Run(ctx)
	}()

	go func() {
		logger.Printf("Web 监控页面监听 %s", cfg.Server.Address())
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil {
			logger.Printf("运行异常: %v", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Printf("关闭 HTTP 服务失败: %v", err)
	}
}

// runHealthcheck 以退出码形式报告服务状态,替代镜像内的 curl。
func runHealthcheck(url string) int {
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: HTTP %d\n", resp.StatusCode)
		return 1
	}
	return 0
}
