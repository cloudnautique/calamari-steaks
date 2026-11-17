package bridge

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
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

// maxAppTerminals bounds live terminal streams per adapter. When it is
// reached, the least recently used view that nobody is typing in is closed
// rather than refusing the newer view.
const maxAppTerminals = 8

type terminalOpenInput struct {
	ManagerID string `json:"managerId,omitempty" jsonschema:"Box ID."`
	Refresh   bool   `json:"refresh,omitempty" jsonschema:"Re-resolve the box's current primary session first. Set after the previous session ended or on an explicit reconnect."`
}

const (
	evictedSuperseded = "superseded"
	evictedCapacity   = "capacity"
)

type terminalChunk struct {
	cursor uint64
	data   []byte
}
type appTerminal struct {
	id           string
	box          string
	manager      *managerConnection
	client       *discobox.Client // the session this view attached to; never retargeted
	lastActive   time.Time        // last human interaction, guarded by Server.appMu
	evicted      string           // why the adapter closed this view, guarded by mu
	stream       *discobox.Stream
	cancel       context.CancelFunc
	timer        *time.Timer
	mu           sync.Mutex
	chunks       []terminalChunk
	bytes        int
	cursor       uint64
	wake         chan struct{}
	done         bool
	sessionEnded bool
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

// terminalResult keeps the view handle and terminal bytes in _meta, which the
// host delivers to the app but not to model context. A terminalId is an input
// capability for a human view, so it MUST NOT appear in content or
// structuredContent (go-sdk would also mirror structuredContent into text).
func terminalResult(data map[string]any) *mcp.CallToolResult {
	return &mcp.CallToolResult{Meta: mcp.Meta{"terminal": data}, Content: []mcp.Content{&mcp.TextContent{Text: "Terminal transport response for the app only."}}}
}

func (s *Server) appTools(server *mcp.Server) {
	appOnly := mcp.Meta{"ui": map[string]any{"visibility": []string{"app"}}}
	mcp.AddTool(server, &mcp.Tool{Name: "terminal_open", Description: "Attach an MCP App view to the pinned terminal. Does not create a box. A newer view of the same box takes over its stream.", Meta: appOnly}, func(ctx context.Context, _ *mcp.CallToolRequest, input terminalOpenInput) (*mcp.CallToolResult, any, error) {
		var manager *managerConnection
		var info map[string]any
		var err error
		if input.Refresh {
			manager, info, err = s.reopen(ctx, input.ManagerID)
			if err == nil && manager == nil {
				return nil, nil, discobox.ErrNoPrimary
			}
		} else {
			manager, err = s.manager(ctx, input.ManagerID)
		}
		if err != nil {
			return nil, nil, err
		}
		client := manager.Client()
		screen, err := client.Screen(ctx, 0)
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
		stream, err := client.Attach(lifetime)
		stopTimer.Stop()
		if err != nil {
			cancel()
			if timedOut.Load() {
				return nil, nil, fmt.Errorf("the box did not finish starting or attaching within %s; reconnect once it is running (no input was sent)", appTerminalAttachTimeout)
			}
			return nil, nil, err
		}
		view := &appTerminal{id: id, box: client.Session.Box, manager: manager, client: client, stream: stream, cancel: cancel, wake: make(chan struct{}), lastActive: time.Now()}
		evict := s.makeRoomFor(view)
		defer func() {
			for key, reason := range evict {
				go s.closeAppTerminalReason(key, reason)
			}
		}()
		s.appMu.Lock()
		defer s.appMu.Unlock()
		if len(s.appTerminals)-len(evict) >= maxAppTerminals {
			cancel()
			stream.Close()
			return nil, nil, errors.New("every terminal view is in use by a person; leave one of them before opening another")
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
		result := map[string]any{"terminalId": id, "session": client.Session, "cols": screen["cols"], "rows": screen["rows"], "cursor": 0}
		for key, value := range info {
			result[key] = value
		}
		return terminalResult(result), nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "terminal_read", Description: "Read terminal bytes through the MCP App bridge from a cursor. May wait up to 12 seconds; safe to repeat a read.", Meta: appOnly, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, input terminalRead) (*mcp.CallToolResult, any, error) {
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
			done, wake, latest, ended := view.done, view.wake, view.cursor, view.sessionEnded
			view.mu.Unlock()
			if len(chunks) > 0 || done {
				view.mu.Lock()
				evicted := view.evicted
				view.mu.Unlock()
				return terminalResult(map[string]any{"chunks": chunks, "cursor": cursor, "closed": done && cursor == latest, "ended": ended, "evicted": evicted}), nil, nil
			}
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-hold.C:
				return terminalResult(map[string]any{"chunks": chunks, "cursor": cursor, "closed": false}), nil, nil
			case <-wake:
			}
		}
	})
	mcp.AddTool(server, &mcp.Tool{Name: "terminal_action", Description: "MCP App terminal ownership, input, resize and detach. Never retry input whose outcome is uncertain.", Meta: appOnly}, func(ctx context.Context, _ *mcp.CallToolRequest, input terminalAction) (*mcp.CallToolResult, any, error) {
		view, err := s.appTerminal(input.TerminalID)
		if err != nil {
			return nil, nil, err
		}
		if input.Action != "close" {
			s.touchAppTerminal(view)
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
			err = view.manager.writeAs(view.id, func() error { _, err := view.client.Send(ctx, input.Text, true); return err })
		case "resize":
			resized, resizeErr := view.manager.resizeAs(view.id, func() error {
				if err := view.stream.Resize(input.Cols, input.Rows); err != nil {
					return err
				}
				return view.stream.Write(discobox.Repaint, nil)
			})
			if resizeErr != nil {
				return nil, nil, resizeErr
			}
			return terminalResult(map[string]any{"ok": true, "resized": resized}), nil, nil
		default:
			err = fmt.Errorf("unknown terminal action")
		}
		if err != nil {
			return nil, nil, err
		}
		return terminalResult(map[string]any{"ok": true}), nil, nil
	})
}

func (s *Server) appTerminal(id string) (*appTerminal, error) {
	s.appMu.Lock()
	defer s.appMu.Unlock()
	view := s.appTerminals[id]
	if view == nil {
		switch s.evictedTerminals[id] {
		case evictedSuperseded:
			return nil, errors.New("this box was opened in a newer view, which took over its terminal; reconnect here to resume")
		case evictedCapacity:
			return nil, errors.New("this terminal view was paused to make room for another; reconnect to resume")
		}
		return nil, errors.New("terminal view expired; reconnect to the same box")
	}
	view.timer.Reset(appTerminalIdle)
	return view, nil
}

// makeRoomFor picks views to close when the adapter is at capacity: older
// views of the same box first (the newer view supersedes them), then the
// least recently used. A view a person is typing in is never chosen. Below
// capacity, several views of one box may coexist (an observer beside the
// view that holds input).
func (s *Server) makeRoomFor(next *appTerminal) map[string]string {
	// Snapshot first: archive holds a box's focus lock while taking appMu, so
	// focus must never be checked while appMu is held.
	s.appMu.Lock()
	views := make([]*appTerminal, 0, len(s.appTerminals))
	for _, view := range s.appTerminals {
		views = append(views, view)
	}
	s.appMu.Unlock()
	evict := map[string]string{}
	candidates := []*appTerminal{}
	for _, view := range views {
		if view.holdsControl() {
			continue
		}
		candidates = append(candidates, view)
	}
	s.appMu.Lock()
	defer s.appMu.Unlock()
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i].box == next.box, candidates[j].box == next.box
		if a != b {
			return a
		}
		return candidates[i].lastActive.Before(candidates[j].lastActive)
	})
	for _, view := range candidates {
		if len(s.appTerminals)-len(evict) < maxAppTerminals {
			break
		}
		if view.box == next.box {
			evict[view.id] = evictedSuperseded
		} else {
			evict[view.id] = evictedCapacity
		}
	}
	return evict
}

func (view *appTerminal) holdsControl() bool {
	view.manager.mu.Lock()
	defer view.manager.mu.Unlock()
	return view.manager.controller == view.id
}

func (s *Server) touchAppTerminal(view *appTerminal) {
	s.appMu.Lock()
	view.lastActive = time.Now()
	s.appMu.Unlock()
}

func (s *Server) closeAppTerminal(id string) { s.closeAppTerminalReason(id, "") }

func (s *Server) closeAppTerminalReason(id, reason string) {
	s.appMu.Lock()
	view := s.appTerminals[id]
	delete(s.appTerminals, id)
	if view != nil && reason != "" {
		if len(s.evictedTerminals) >= 64 {
			clear(s.evictedTerminals)
		}
		s.evictedTerminals[id] = reason
	}
	s.appMu.Unlock()
	if view != nil {
		view.mu.Lock()
		view.evicted = reason
		view.mu.Unlock()
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
		if kind == discobox.Exit {
			view.mu.Lock()
			view.sessionEnded = true
			view.mu.Unlock()
			return
		}
		if kind == discobox.Error {
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
