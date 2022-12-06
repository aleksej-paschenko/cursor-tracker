package tracker

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"

	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

//go:embed index.html
var indexHtml string

type Options struct {
	bots                    int
	port                    int
	gracefulShutdownTimeout time.Duration
	websocketHandlerCtor    func(logger *zap.Logger, userActionCh chan<- userAction) http.HandlerFunc
}

type Option func(o *Options)

func WithGracefulShutdown(timeout time.Duration) Option {
	return func(o *Options) {
		o.gracefulShutdownTimeout = timeout
	}
}

func WithPort(port int) Option {
	return func(o *Options) {
		o.port = port
	}
}

func WithBotNumber(bots int) Option {
	return func(o *Options) {
		if bots < 0 {
			bots = 0
		}
		if bots > 1000 {
			bots = 1000
		}
		o.bots = bots
	}
}

type CursorTracker struct {
	userActionCh chan userAction

	portNumber              int
	logger                  *zap.Logger
	mux                     *http.ServeMux
	bots                    int
	gracefulShutdownTimeout time.Duration
}

func New(logger *zap.Logger, opts ...Option) *CursorTracker {
	options := &Options{
		websocketHandlerCtor: websocketHandler,
	}
	for _, o := range opts {
		o(options)
	}
	tracker := &CursorTracker{
		userActionCh: make(chan userAction),
		mux:          http.NewServeMux(),
		bots:         options.bots,
		logger:       logger,
	}
	tracker.mux.HandleFunc("/websocket", options.websocketHandlerCtor(logger, tracker.userActionCh))
	tracker.mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		fmt.Fprintf(writer, indexHtml)
	})

	return tracker
}

func (trk *CursorTracker) Run(ctx context.Context) {
	g, _ := errgroup.WithContext(ctx)
	g.Go(func() error {
		endpoint := fmt.Sprintf("ws://localhost:%d/websocket", trk.portNumber)
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(4 * time.Second):
			}

			var wg sync.WaitGroup
			wg.Add(trk.bots)
			for i := 0; i < trk.bots; i++ {
				go runBot(ctx, trk.logger, endpoint, &wg)
			}
			wg.Wait()
		}
	})
	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", trk.portNumber),
		Handler: trk.mux,
	}
	g.Go(func() error {
		err := httpServer.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			trk.logger.Info("CursorTracker quit")
			return nil
		}
		trk.logger.Fatal("ListedAndServe() failed", zap.Error(err))
		return err
	})
	g.Go(func() error {
		handleUserActionsLoop(ctx, trk.logger, trk.userActionCh)
		return nil
	})

	if err := g.Wait(); err != nil {
		trk.logger.Error("CursorTracker.Run() failed", zap.Error(err))
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), trk.gracefulShutdownTimeout)
	defer cancel()

	if err := httpServer.Shutdown(shutdownContext); err != nil {
		trk.logger.Fatal("Shutdown() failed", zap.Error(err))
	}
}

type userActionType string

const (
	moveMethod  userActionType = "move"
	leaveMethod userActionType = "leave"
)

type sessionUpdateModel struct {
	SessionID sessionID      `json:"sessionId"`
	Method    userActionType `json:"method"`
	X         int            `json:"x"`
	Y         int            `json:"y"`
}

type cursorMoveModel struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type sessionID string

type conn interface {
	WriteJSON(v interface{}) error
}

type userAction struct {
	sid        sessionID
	actionType userActionType
	X, Y       int
	conn       conn
}

func websocketHandler(logger *zap.Logger, userActionCh chan<- userAction) http.HandlerFunc {
	upgrader := websocket.Upgrader{}
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			logger.Error("failed to upgrade HTTP connection to websocket protocol",
				zap.Error(err),
				zap.String("remoteAddr", conn.RemoteAddr().String()))
			return
		}
		sid := sessionID(conn.RemoteAddr().String())
		logger.Info("new connection", zap.String("remoteAddr", conn.RemoteAddr().String()))

		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				userActionCh <- userAction{
					sid:        sid,
					actionType: leaveMethod,
				}
				logger.Error("conn.ReadMessage failed", zap.Error(err))
				return
			}
			mm := cursorMoveModel{}
			if err := json.Unmarshal(msg, &mm); err != nil {
				logger.Error("failed to unmarshal cursorMoveModel", zap.Error(err))
				continue
			}
			userActionCh <- userAction{
				sid:        sid,
				actionType: moveMethod,
				X:          mm.X,
				Y:          mm.Y,
				conn:       conn,
			}
		}
	}
}

func handleUserActionsLoop(ctx context.Context, logger *zap.Logger, userActionCh <-chan userAction) {
	connections := make(map[sessionID]conn)
	for {
		select {
		case msg := <-userActionCh:
			switch msg.actionType {
			case leaveMethod:
				delete(connections, msg.sid)
			case moveMethod:
				connections[msg.sid] = msg.conn
			}
			for cid, conn := range connections {
				if cid == msg.sid {
					continue
				}
				msg := sessionUpdateModel{
					SessionID: msg.sid,
					Method:    msg.actionType,
					X:         msg.X,
					Y:         msg.Y,
				}
				if err := conn.WriteJSON(msg); err != nil {
					logger.Error("WriteJSON failed", zap.Error(err))
					continue
				}
			}
		case <-ctx.Done():
			var messages []sessionUpdateModel
			for cid := range connections {
				messages = append(messages, sessionUpdateModel{
					SessionID: cid,
					Method:    leaveMethod,
				})
			}
			for cid, conn := range connections {
				logger.Info("sending quit signal to a user",
					zap.String("remoteAddr", string(cid)))

				for _, msg := range messages {
					if err := conn.WriteJSON(msg); err != nil {
						logger.Error("WriteJSON failed", zap.Error(err))
						continue
					}
				}
			}
			return
		}
	}
}
