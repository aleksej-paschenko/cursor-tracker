package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/aleksej-paschenko/cursor-tracker/internal/tracker"
)

const (
	defaultPort      = 4567
	gracefulShutdown = 2 * time.Second
)

func main() {
	logger, _ := zap.NewProduction(zap.AddStacktrace(zapcore.FatalLevel))
	defer func() {
		_ = logger.Sync()
	}()

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)

	s := tracker.New(logger,
		tracker.WithBotNumber(4),
		tracker.WithGracefulShutdown(gracefulShutdown),
		tracker.WithPort(defaultPort))

	ctx, cancel := context.WithCancel(context.Background())
	go s.Run(ctx)

	<-signalCh
	cancel()

	time.Sleep(gracefulShutdown)
}
