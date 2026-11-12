package bridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/cloudnautique/discobox-mcp-ui/internal/discobox"
	"github.com/coder/websocket"
)

type browserMessage struct {
	Type  string `json:"type"`
	Token string `json:"token,omitempty"`
	Data  string `json:"data,omitempty"`
	Text  string `json:"text,omitempty"`
	Cols  int    `json:"cols,omitempty"`
	Rows  int    `json:"rows,omitempty"`
}

func (s *Server) terminal(w http.ResponseWriter, r *http.Request) {
	// Check origins ourselves, exactly, before using websocket's bypass option.
	if !s.allowedOrigins[r.Header.Get("Origin")] {
		http.Error(w, "terminal origin denied; configure --ui-origin for this MCP host", http.StatusForbidden)
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ws.SetReadLimit(128 << 10)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	authCtx, authCancel := context.WithTimeout(ctx, 5*time.Second)
	kind, payload, err := ws.Read(authCtx)
	authCancel()
	var auth browserMessage
	if err != nil || kind != websocket.MessageText || json.Unmarshal(payload, &auth) != nil || auth.Type != "auth" || !s.validToken(auth.Token) {
		ws.Close(websocket.StatusPolicyViolation, "authentication required")
		return
	}
	manager, err := s.manager(ctx, r.URL.Query().Get("managerId"))
	if err != nil {
		sendJSON(ctx, ws, map[string]any{"type": "error", "message": err.Error()})
		return
	}
	screen, err := manager.Client.Screen(ctx, 0)
	if err != nil {
		sendJSON(ctx, ws, map[string]any{"type": "error", "message": err.Error()})
		return
	}
	stream, err := manager.Client.Attach(ctx)
	if err != nil {
		sendJSON(ctx, ws, map[string]any{"type": "error", "message": err.Error()})
		return
	}
	defer stream.Close()
	var key [16]byte
	if _, err := rand.Read(key[:]); err != nil {
		return
	}
	owner := hex.EncodeToString(key[:])
	defer manager.release(owner)
	if err := sendJSON(ctx, ws, map[string]any{"type": "connected", "session": manager.Client.Session, "cols": screen["cols"], "rows": screen["rows"]}); err != nil {
		return
	}
	// An attach is a byte stream: frame boundaries need not match WS messages.
	outputDone := make(chan struct{})
	go func() {
		defer close(outputDone)
		defer cancel()
		for {
			kind, data, err := stream.Read()
			if err != nil {
				return
			}
			switch kind {
			case discobox.Stdout, discobox.Stderr:
				writeCtx, done := context.WithTimeout(ctx, 10*time.Second)
				err = ws.Write(writeCtx, websocket.MessageBinary, data)
				done()
				if err != nil {
					return
				}
			case discobox.Error:
				if sendJSON(ctx, ws, map[string]any{"type": "error", "message": string(data)}) != nil {
					return
				}
			case discobox.Exit:
				sendJSON(ctx, ws, map[string]any{"type": "exit", "details": string(data)})
				return
			}
		}
	}()
	defer func() { cancel(); stream.Close(); <-outputDone }()
	if err := stream.Write(discobox.Ready, nil); err != nil {
		return
	}
	for {
		kind, payload, err := ws.Read(ctx)
		if err != nil {
			return
		}
		var message browserMessage
		if kind != websocket.MessageText || json.Unmarshal(payload, &message) != nil {
			ws.Close(websocket.StatusUnsupportedData, "expected a JSON message")
			return
		}
		switch message.Type {
		case "claim":
			err = manager.claim(owner)
			if err == nil {
				err = sendJSON(ctx, ws, map[string]any{"type": "control", "owned": true})
			}
		case "release":
			manager.release(owner)
			err = sendJSON(ctx, ws, map[string]any{"type": "control", "owned": false})
		case "input":
			err = manager.writeAs(owner, func() error { return stream.Write(discobox.Input, []byte(message.Data)) })
		case "message":
			err = manager.writeAs(owner, func() error { _, err := manager.Client.Send(ctx, message.Text, true); return err })
			if err == nil {
				err = sendJSON(ctx, ws, map[string]any{"type": "sent"})
			}
		case "resize":
			err = manager.writeAs(owner, func() error {
				if err := stream.Resize(message.Cols, message.Rows); err != nil {
					return err
				}
				return stream.Write(discobox.Repaint, nil)
			})
		default:
			ws.Close(websocket.StatusPolicyViolation, "unknown terminal action")
			return
		}
		if err != nil {
			if sendJSON(ctx, ws, map[string]any{"type": "error", "message": err.Error()}) != nil {
				return
			}
		}
	}
}

func sendJSON(ctx context.Context, ws *websocket.Conn, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return ws.Write(ctx, websocket.MessageText, data)
}
