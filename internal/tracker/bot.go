package tracker

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

const (
	maxCoordinate = 600
)

func next(coordinate, delta int) (int, int) {
	if coordinate+delta >= maxCoordinate {
		return coordinate - delta, -1
	}
	if coordinate+delta <= 0 {
		return coordinate + delta, 1
	}
	return coordinate + delta, delta
}

func runBot(ctx context.Context, logger *zap.Logger, address string, wg *sync.WaitGroup) {
	defer func() {
		wg.Done()
	}()
	conn, _, err := websocket.DefaultDialer.Dial(address, nil)
	if err != nil {
		logger.Error("bot failed to dial", zap.Error(err))
		return
	}
	x, dx := rand.Intn(maxCoordinate), 1
	y, dy := rand.Intn(maxCoordinate), 1
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Millisecond):
		}
		x, dx = next(x, dx)
		y, dy = next(y, dy)
		if err := conn.WriteJSON(cursorMoveModel{X: x, Y: y}); err != nil {
			logger.Error("bot failed to writeJSON", zap.Error(err))
			return
		}

		if rand.Intn(5000) == 1 {
			logger.Info("bot quit")
			conn.Close()
			return
		}
	}
}
