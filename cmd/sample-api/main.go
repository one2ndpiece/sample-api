package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/one2ndpiece/sample-api/internal/server"
)

const (
	publicAddress   = ":8080"
	metricsAddress  = ":9090"
	cpuWorkDuration = 250 * time.Millisecond
	slowDelay       = 2 * time.Second
	drainDelay      = 2 * time.Second
	shutdownTimeout = 10 * time.Second
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	app := server.New(server.Config{
		PodName:         os.Getenv("POD_NAME"),
		CPUWorkDuration: cpuWorkDuration,
		SlowDelay:       slowDelay,
		DrainDelay:      drainDelay,
		ShutdownTimeout: shutdownTimeout,
		Logger:          logger,
	})

	if err := app.Run(ctx, publicAddress, metricsAddress); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("server stopped unexpectedly", "error", err)
		os.Exit(1)
	}
}
