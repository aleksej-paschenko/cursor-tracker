package main

import (
	"context"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/aleksej-paschenko/cursor-tracker/internal/tracker"
)

const (
	defaultPort = 4567
)

func main() {
	logger, _ := zap.NewProduction(zap.AddStacktrace(zapcore.FatalLevel))
	defer logger.Sync()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	gracefulShutdown := 5 * time.Second

	portStr := os.Getenv("PORT")
	logger.Info("PORT", zap.String("value", portStr))

	portNumber, err := strconv.Atoi(portStr)
	if len(portStr) == 0 || err != nil {
		portNumber = defaultPort
	}

	s := tracker.New(logger,
		tracker.WithBotNumber(4),
		tracker.WithGracefulShutdown(gracefulShutdown),
		tracker.WithPort(portNumber))

	ctx, cancel := context.WithCancel(context.Background())
	go s.Run(ctx)

	<-sigs

	cancel()

	time.Sleep(gracefulShutdown)
}
