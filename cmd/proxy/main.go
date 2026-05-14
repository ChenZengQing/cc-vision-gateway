package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cc-vision-gateway/internal/cache"
	"cc-vision-gateway/internal/config"
	"cc-vision-gateway/internal/routing"
	"cc-vision-gateway/internal/server"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config error", "error", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}))
	slog.SetDefault(logger)

	modelMap, err := routing.LoadModelMap(cfg.ModelMapFile)
	if err != nil {
		slog.Error("model map error", "error", err)
		os.Exit(1)
	}

	imageCache, err := cache.Open(cfg)
	if err != nil {
		slog.Error("cache error", "error", err)
		os.Exit(1)
	}
	defer imageCache.Close()

	handler := server.New(cfg, modelMap, imageCache, logger)
	httpServer := &http.Server{
		Addr:              cfg.ProxyHost + ":" + cfg.ProxyPort,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       90 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("starting cc-vision-gateway", "addr", httpServer.Addr)
		errCh <- httpServer.ListenAndServe()
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-stop:
		slog.Info("shutdown signal", "signal", sig.String())
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "error", err)
		os.Exit(1)
	}
}
