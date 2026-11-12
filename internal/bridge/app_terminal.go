package bridge

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudnautique/discobox-mcp-ui/internal/discobox"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const appTerminalIdle = 90 * time.Second
const appTerminalBuffer = 1 << 20

// A stopped manager may need tens of seconds to boot. Keep the wait inside
// the usual 60-second host tool budget, and bound only the initial attach.
const appTerminalAttachTimeout = 45 * time.Second

type terminalChunk struct {
	cursor uint64
	data   []byte
}
type appTerminal struct {
	id      string
	manager *managerConnection
	stream  *discobox.Stream
	cancel  context.CancelFunc
	timer   *time.Timer
	mu      sync.Mutex
	chunks  []terminalChunk
	bytes   int
	cursor  uint64
	wake    chan struct{}
	done    bool
}
type terminalRead struct {
	TerminalID string `json:"terminalId"`
	After      uint64 `json:"after"`
}
type terminalAction struct {
	TerminalID string `json:"terminalId"`
	Action     string `json:"action" jsonschema:"claim, release, input, resize or close"`
	Text       string `json:"text,omitempty"`
	Data       string `json:"data,omitempty" jsonschema:"Base64 terminal input; never replay an uncertain input"`
	Cols       int    `json:"cols,omitempty"`
	Rows       int    `json:"rows,omitempty"`
}

func (s *Server) appTools(server *mcp.Server) {
	appOnly := mcp.Meta{"ui": map[string]any{"visibility": []string{"app"}}}
	mcp.AddTool(server, &mcp.Tool{Name: "terminal_open", Description: "Attach an MCP App view to the pinned terminal. Does not create or replace a manager.", Meta: appOnly}, func(ctx context.Context, _ *mcp.CallToolRequest, input managerInput) (*mcp.CallToolResult, map[string]any, error) {
		manager, err := s.manager(ctx, input.ManagerID)
		if err != nil {
			return nil, nil, err
		}
		screen, err := manager.Client.Screen(ctx, 0)
		if err != nil {
			return nil, nil, err
		}
		var key [16]byte
		if _, err := rand.Read(key[:]); err != nil {
			return nil, nil, err
		}
		id := hex.EncodeToString(key[:])
		// The attach outlives this tool request. An idle timer and explicit close
		// bound its lifetime even if the host destroys an iframe without teardown.
		lifetime, cancel := context.WithCancel(context.Background())
		stopRequest := context.AfterFunc(ctx, cancel)
		defer stopRequest()
		// Stop the startup deadline after dialing without canceling the stream.
		var timedOut atomic.Bool
		stopTimer := time.AfterFunc(appTerminalAttachTimeout, func() { timedOut.Store(true); cancel() })
		stream, err := manager.Client.Attach(lifetime)
		stopTimer.Stop()
		if err != nil {
			cancel()
			if timedOut.Load() {
				return nil, nil, fmt.Errorf("manager did not finish starting or attaching within %s; reconnect to the same manager once it is running (no input was sent)", appTerminalAttachTimeout)
			}
			return nil, nil, err
		}
		view := &appTerminal{id: id, manager: manager, stream: stream, cancel: cancel, wake: make(chan struct{})}
		s.appMu.Lock()
		defer s.appMu.Unlock()
		if len(s.appTerminals) >= 8 {
			cancel()
			stream.Close()
			return nil, nil, errors.New("too many terminal views; close an existing view or wait for it to expire")
		}
		s.appTerminals[id] = view
		view.timer = time.AfterFunc(appTerminalIdle, func() { s.closeAppTerminal(id) })
		go view.readOutput()
		if err := stream.Write(discobox.Ready, nil); err != nil {
			delete(s.appTerminals, id)
			view.timer.Stop()
			cancel()
			stream.Close()
			return nil, nil, err
		}
		return nil, map[string]any{"terminalId": id, "session": manager.Client.Session, "cols": screen["cols"], "rows": screen["rows"], "cursor": 0}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "terminal_read", Description: "Read terminal bytes through the MCP App bridge from a cursor. May wait up to 12 seconds; safe to repeat a read.", Meta: appOnly, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, input terminalRead) (*mcp.CallToolResult, map[string]any, error) {
		view, err := s.appTerminal(input.TerminalID)
		if err != nil {
			return nil, nil, err
		}
		hold := time.NewTimer(12 * time.Second)
		defer hold.Stop()
		for {
			view.mu.Lock()
			if input.After > view.cursor || (len(view.chunks) > 0 && input.After+1 < view.chunks[0].cursor) {
				view.mu.Unlock()
				return nil, nil, errors.New("terminal output cursor expired; reconnect to repaint the screen")
			}
			chunks := []string{}
			cursor := input.After
			size := 0
			for _, chunk := range view.chunks {
				if chunk.cursor > input.After {
					chunks = append(chunks, base64.StdEncoding.EncodeToString(chunk.data))
					cursor = chunk.cursor
					size += len(chunk.data)
					if size >= 128<<10 {
						break
					}
				}
			}
			done, wake, latest := view.done, view.wake, view.cursor
			view.mu.Unlock()
			if len(chunks) > 0 || done {
				return nil, map[string]any{"chunks": chunks, "cursor": cursor, "closed": done && cursor == latest}, nil
			}
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-hold.C:
				return nil, map[string]any{"chunks": chunks, "cursor": cursor, "closed": false}, nil
			case <-wake:
			}
		}
	})
	mcp.AddTool(server, &mcp.Tool{Name: "terminal_action", Description: "MCP App terminal ownership, input, resize and detach. Never retry input whose outcome is uncertain.", Meta: appOnly}, func(ctx context.Context, _ *mcp.CallToolRequest, input terminalAction) (*mcp.CallToolResult, map[string]any, error) {
		view, err := s.appTerminal(input.TerminalID)
		if err != nil {
			return nil, nil, err
		}
		switch input.Action {
		case "claim":
			err = view.manager.claim(view.id)
		case "release":
			view.manager.release(view.id)
		case "close":
			s.closeAppTerminal(view.id)
		case "input":
			if len(input.Data) > base64.StdEncoding.EncodedLen(64<<10) {
				return nil, nil, errors.New("terminal input exceeds 65536 bytes")
			}
			data, decodeErr := base64.StdEncoding.DecodeString(input.Data)
			if decodeErr != nil || len(data) == 0 || len(data) > 64<<10 {
				return nil, nil, errors.New("terminal input must be 1 to 65536 bytes encoded as base64")
			}
			err = view.manager.writeAs(view.id, func() error { return view.stream.Write(discobox.Input, data) })
		case "message":
			err = view.manager.writeAs(view.id, func() error { _, err := view.manager.Client.Send(ctx, input.Text, true); return err })
		case "resize":
			err = view.manager.writeAs(view.id, func() error {
				if err := view.stream.Resize(input.Cols, input.Rows); err != nil {
					return err
				}
				return view.stream.Write(discobox.Repaint, nil)
			})
		default:
			err = fmt.Errorf("unknown terminal action")
		}
		return nil, map[string]any{"ok": err == nil}, err
	})
}

func (s *Server) appTerminal(id string) (*appTerminal, error) {
	s.appMu.Lock()
	defer s.appMu.Unlock()
	view := s.appTerminals[id]
	if view == nil {
		return nil, errors.New("terminal view expired; reconnect to the same manager")
	}
	view.timer.Reset(appTerminalIdle)
	return view, nil
}
func (s *Server) closeAppTerminal(id string) {
	s.appMu.Lock()
	view := s.appTerminals[id]
	delete(s.appTerminals, id)
	s.appMu.Unlock()
	if view != nil {
		view.timer.Stop()
		view.cancel()
		view.stream.Close()
		view.manager.release(id)
	}
}
func (view *appTerminal) readOutput() {
	defer func() {
		view.mu.Lock()
		view.done = true
		close(view.wake)
		view.wake = make(chan struct{})
		view.mu.Unlock()
	}()
	for {
		kind, data, err := view.stream.Read()
		if err != nil {
			return
		}
		if kind == discobox.Exit || kind == discobox.Error {
			return
		}
		if kind != discobox.Stdout && kind != discobox.Stderr {
			continue
		}
		view.mu.Lock()
		for len(data) > 0 {
			size := min(len(data), 32<<10)
			part := append([]byte(nil), data[:size]...)
			data = data[size:]
			view.cursor++
			view.chunks = append(view.chunks, terminalChunk{cursor: view.cursor, data: part})
			view.bytes += size
			for view.bytes > appTerminalBuffer {
				view.bytes -= len(view.chunks[0].data)
				view.chunks[0].data = nil
				view.chunks = view.chunks[1:]
			}
		}
		close(view.wake)
		view.wake = make(chan struct{})
		view.mu.Unlock()
	}
}
