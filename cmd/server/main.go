package main

import (
	"context"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"naughtyfication/internal/app"
	"naughtyfication/internal/config"
)

func main() {
	cfg := config.Load()

	application, err := app.New(cfg)
	if err != nil {
		panic(err)
	}

	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           application.Router(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			panic(err)
		}
	}()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		panic(err)
	}

	application.Close()
}
