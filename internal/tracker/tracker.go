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
}

type Option func(o *Options)

func WithGracefulShutdown(timeout time.Duration) Option {
	return func(o *Options) {
		o.gracefulShutdownTimeout = timeout
	}
}

// WithPort configures a port to open for incoming connections.
func WithPort(port int) Option {
	return func(o *Options) {
		o.port = port
	}
}

// WithBotNumber configures a number of bots to be added to CursorTracker.
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
	logger *zap.Logger
	mux    *http.ServeMux

	clientActionCh chan clientAction

	portNumber              int
	bots                    int
	gracefulShutdownTimeout time.Duration
}

// New returns a new instance of CursorTracker.
func New(logger *zap.Logger, opts ...Option) *CursorTracker {
	options := &Options{}
	for _, o := range opts {
		o(options)
	}
	tracker := &CursorTracker{
		clientActionCh: make(chan clientAction),
		mux:            http.NewServeMux(),
		bots:           options.bots,
		portNumber:     options.port,
		logger:         logger,
	}
	tracker.mux.HandleFunc("/websocket", websocketHandler(logger, tracker.clientActionCh))
	tracker.mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		fmt.Fprint(writer, indexHtml)
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
			case <-time.After(2 * time.Second):
			}

			var wg sync.WaitGroup
			wg.Add(trk.bots)

			for i := 0; i < trk.bots; i++ {
				go func() {
					defer wg.Done()
					runBot(ctx, trk.logger, endpoint)
				}()
			}
			wg.Wait()
		}
	})
	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", trk.portNumber),
		Handler: trk.mux,
	}
	trk.logger.Info("Running http server",
		zap.Int("port", trk.portNumber),
		zap.String("address", fmt.Sprintf("http://localhost:%d/", trk.portNumber)))

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
		handleClientActionsLoop(ctx, trk.logger, trk.clientActionCh)
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

type clientActionType string

const (
	moveMethod  clientActionType = "move"
	leaveMethod clientActionType = "leave"
)

// sessionUpdateModel is a JSON model to encode a session state.
// Once a session is updated, it'll be sent to all connected clients.
type sessionUpdateModel struct {
	SessionID sessionID        `json:"sessionId"`
	Method    clientActionType `json:"method"`
	X         int              `json:"x"`
	Y         int              `json:"y"`
}

// cursorMoveModel is a JSON model to encode a cursor position.
// Once a mouse position in a browser is changed,
// the browser will send this new position to CursorTracker.
type cursorMoveModel struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// sessionID is a unique identifier of a client's session.
type sessionID string

type conn interface {
	WriteJSON(v interface{}) error
	Close() error
}

// clientAction is used to pass information from a websocket goroutine to handleClientActionsLoop.
type clientAction struct {
	sid        sessionID
	actionType clientActionType
	X, Y       int
	conn       conn
}

// websocketHandler handles each websocket connection in its own goroutine.
func websocketHandler(logger *zap.Logger, clientActionCh chan<- clientAction) http.HandlerFunc {
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
				clientActionCh <- clientAction{
					sid:        sid,
					actionType: leaveMethod,
				}
				if websocket.IsCloseError(err, websocket.CloseGoingAway) {
					logger.Info("connection closed", zap.String("remoteAddr", string(sid)))
					return
				}
				logger.Error("conn.ReadMessage failed", zap.Error(err))
				return
			}
			mm := cursorMoveModel{}
			if err := json.Unmarshal(msg, &mm); err != nil {
				logger.Error("failed to unmarshal cursorMoveModel", zap.Error(err))
				continue
			}
			clientActionCh <- clientAction{
				sid:        sid,
				actionType: moveMethod,
				X:          mm.X,
				Y:          mm.Y,
				conn:       conn,
			}
		}
	}
}

// handleClientActionsLoop handles all clients' updates in a loop.
// When a client updates its mouse coordinates, the method sends this update to the other clients.
func handleClientActionsLoop(ctx context.Context, logger *zap.Logger, clientActionCh <-chan clientAction) {
	connections := make(map[sessionID]conn)

	// we don't use
	// for msg := range clientActionCh { } + close(clientActionCh) pattern here,
	// because http.Server doesn't close hijacked connections such as websockets,
	// so websocketHandler keeps writing to clientActionCh. If we close the channel, it could lead to a panic.

	for {
		select {
		case msg := <-clientActionCh:
			switch msg.actionType {
			case leaveMethod:
				delete(connections, msg.sid)
			case moveMethod:
				connections[msg.sid] = msg.conn
			}
			for sid, conn := range connections {
				if sid == msg.sid {
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
			messages := make([]sessionUpdateModel, 0, len(connections))
			for sid := range connections {
				messages = append(messages, sessionUpdateModel{
					SessionID: sid,
					Method:    leaveMethod,
				})
			}
			for sid, conn := range connections {
				logger.Info("sending quit signal",
					zap.String("remoteAddr", string(sid)))

				for _, msg := range messages {
					if msg.SessionID == sid {
						continue
					}
					if err := conn.WriteJSON(msg); err != nil {
						logger.Error("WriteJSON failed", zap.Error(err))
						continue
					}
				}
				conn.Close()
			}
			return
		}
	}
}
