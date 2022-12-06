package tracker

import (
	"context"
	"math/rand"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

const (
	maxCoordinate = 600
)

func next(coordinate, delta int) (int, int) {
	if coordinate+delta >= maxCoordinate {
		return coordinate - delta, -10
	}
	if coordinate+delta <= 0 {
		return coordinate + delta, 10
	}
	return coordinate + delta, delta
}

// runBot connects to the given websocketEndpoint and emulates working-with-mouse activity.
// Periodically, it commits suicide.
func runBot(ctx context.Context, logger *zap.Logger, websocketEndpoint string) {
	conn, _, err := websocket.DefaultDialer.Dial(websocketEndpoint, nil)
	if err != nil {
		logger.Error("bot failed to dial", zap.Error(err))
		return
	}
	x, dx := rand.Intn(maxCoordinate), 10
	y, dy := rand.Intn(maxCoordinate), 10
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
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
