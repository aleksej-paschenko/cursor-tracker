package tracker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type mockConn struct {
	OnWriteJSON func(v interface{}) error
}

func (m *mockConn) Close() error {
	return nil
}

func (m *mockConn) WriteJSON(v interface{}) error {
	return m.OnWriteJSON(v)
}

var _ conn = &mockConn{}

func Test_handleClientActionsLoop(t *testing.T) {
	tests := []struct {
		name            string
		initActions     []clientAction
		sendAction      clientAction
		expectedUpdates map[sessionID][]sessionUpdateModel
	}{
		{
			name: "have two sessions, updating one of them",
			initActions: []clientAction{
				{sid: "1", actionType: moveMethod, X: 10, Y: 20},
				{sid: "2", actionType: moveMethod, X: 20, Y: 40},
			},
			sendAction: clientAction{
				sid:        "2",
				actionType: moveMethod,
				X:          1001,
				Y:          2002,
			},
			expectedUpdates: map[sessionID][]sessionUpdateModel{
				"1": {
					{SessionID: "2", Method: moveMethod, X: 20, Y: 40},
					{SessionID: "2", Method: moveMethod, X: 1001, Y: 2002},
				},
			},
		},
		{
			name: "have two sessions, the second quits, should get a leave update",
			initActions: []clientAction{
				{sid: "1", actionType: moveMethod, X: 10, Y: 20},
				{sid: "2", actionType: moveMethod, X: 20, Y: 40},
			},
			sendAction: clientAction{
				sid:        "2",
				actionType: leaveMethod,
			},
			expectedUpdates: map[sessionID][]sessionUpdateModel{
				"1": {
					{SessionID: "2", Method: moveMethod, X: 20, Y: 40},
					{SessionID: "2", Method: leaveMethod},
				},
			},
		},
		{
			name: "have two sessions, adding a new one",
			initActions: []clientAction{
				{sid: "1", actionType: moveMethod, X: 10, Y: 20},
				{sid: "2", actionType: moveMethod, X: 20, Y: 40},
			},
			sendAction: clientAction{
				sid:        "4",
				actionType: moveMethod,
				X:          101,
				Y:          202,
			},
			expectedUpdates: map[sessionID][]sessionUpdateModel{
				"1": {
					{SessionID: "2", Method: moveMethod, X: 20, Y: 40},
					{SessionID: "4", Method: moveMethod, X: 101, Y: 202},
				},
				"2": {
					{SessionID: "4", Method: moveMethod, X: 101, Y: 202},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			logger, _ := zap.NewDevelopment()
			ch := make(chan clientAction)
			go handleClientActionsLoop(context.Background(), logger, ch)

			// this mutex is used to make "go test -race" happy because
			// gotUpdates is updated from several goroutines
			var mu sync.Mutex
			gotUpdates := map[sessionID][]sessionUpdateModel{}

			for _, action := range tt.initActions {
				sid := action.sid
				action.conn = &mockConn{
					OnWriteJSON: func(v interface{}) error {
						mu.Lock()
						defer mu.Unlock()

						update := v.(sessionUpdateModel)
						gotUpdates[sid] = append(gotUpdates[sid], update)
						return nil
					},
				}
				ch <- action
			}

			ch <- tt.sendAction
			time.Sleep(1 * time.Second)

			mu.Lock()
			defer mu.Unlock()

			require.Equal(t, tt.expectedUpdates, gotUpdates)

		})
	}
}

func Test_handleClientActionsLoop_shouldLeaveAllWhenCtxCanceled(t *testing.T) {

	logger, _ := zap.NewDevelopment()
	ch := make(chan clientAction)

	ctx, cancel := context.WithCancel(context.Background())
	go handleClientActionsLoop(ctx, logger, ch)

	initActions := []clientAction{
		{sid: "1", actionType: moveMethod, X: 10, Y: 20},
		{sid: "2", actionType: moveMethod, X: 20, Y: 40},
	}

	// this mutex is used to make "go test -race" happy because
	// gotUpdates is updated from several goroutines
	var mu sync.Mutex
	gotUpdates := map[sessionID][]sessionUpdateModel{}

	for _, action := range initActions {
		sid := action.sid
		action.conn = &mockConn{
			OnWriteJSON: func(v interface{}) error {
				mu.Lock()
				defer mu.Unlock()
				update := v.(sessionUpdateModel)
				gotUpdates[sid] = append(gotUpdates[sid], update)
				return nil
			},
		}
		ch <- action
	}

	cancel()

	time.Sleep(1 * time.Second)

	expectedUpdates := map[sessionID][]sessionUpdateModel{
		"1": {
			{SessionID: "2", Method: moveMethod, X: 20, Y: 40},
			{SessionID: "2", Method: leaveMethod},
		},
		"2": {
			{SessionID: "1", Method: leaveMethod},
		},
	}

	mu.Lock()
	defer mu.Unlock()

	require.Equal(t, expectedUpdates, gotUpdates)
}

func Test_websocketHandler(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	ch := make(chan clientAction)

	handler := websocketHandler(logger, ch)
	server := httptest.NewServer(handler)
	defer server.Close()

	var sid sessionID
	go func() {
		url := strings.Replace(server.URL, "http", "ws", -1)
		conn, _, err := websocket.DefaultDialer.Dial(url, nil)
		require.Nil(t, err)

		// we use LocalAddr() because it is the dial side of the connection
		sid = sessionID(conn.LocalAddr().String())

		for i := 0; i < 3; i++ {
			err = conn.WriteJSON(&cursorMoveModel{X: 100 + i, Y: 200 + i})
			require.Nil(t, err)
		}
		conn.Close()
		// we can't close "ch" because it is still in use in websocketHandler
	}()

	var messages []clientAction
	for {
		msg := <-ch
		msg.conn = nil
		messages = append(messages, msg)
		if msg.actionType == leaveMethod {
			break
		}
	}
	expectedMessages := []clientAction{
		{sid: sid, actionType: moveMethod, X: 100, Y: 200},
		{sid: sid, actionType: moveMethod, X: 101, Y: 201},
		{sid: sid, actionType: moveMethod, X: 102, Y: 202},
		{sid: sid, actionType: leaveMethod, X: 0, Y: 0},
	}
	require.Equal(t, expectedMessages, messages)
}

func TestCursorTracker_Run(t *testing.T) {
	logger, _ := zap.NewDevelopment(zap.AddStacktrace(zapcore.FatalLevel))
	tracker := New(logger,
		WithPort(4567),
		WithBotNumber(7))
	ctx, cancel := context.WithCancel(context.Background())
	go tracker.Run(ctx)

	defer cancel()

	time.Sleep(3 * time.Second)

	cli := http.Client{}
	resp, err := cli.Get("http://localhost:4567/")
	require.Nil(t, err)

	indexPage, err := io.ReadAll(resp.Body)
	require.Nil(t, err)
	require.Equal(t, indexHtml, string(indexPage))

	conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:4567/websocket", nil)
	require.Nil(t, err)

	// we have to send this first message to get updates from other clients
	err = conn.WriteJSON(&cursorMoveModel{X: 100, Y: 200})
	require.Nil(t, err)

	sessionCh := make(chan sessionID)
	go func() {

		for {
			_, msg, err := conn.ReadMessage()
			require.Nil(t, err)
			model := sessionUpdateModel{}
			err = json.Unmarshal(msg, &model)
			require.Nil(t, err)
			sessionCh <- model.SessionID
		}

	}()

	timeout := time.After(5 * time.Second)
	sessionIDs := map[sessionID]struct{}{}
	for {
		var done time.Time
		select {
		case done = <-timeout:
		case sid := <-sessionCh:
			sessionIDs[sid] = struct{}{}
		}
		if !done.IsZero() {
			break
		}
	}

	require.True(t, len(sessionIDs) >= 7)
}
