package bridge

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const desktopBuffer = 2 << 20
const desktopReply = 128 << 10
const desktopInput = 64 << 10
const desktopIdle = 90 * time.Second

type desktopTarget struct {
	BoxID string `json:"boxId"`
}
type desktopReadInput struct {
	DesktopID string `json:"desktopId"`
	After     uint64 `json:"after"`
}
type desktopActionInput struct {
	DesktopID string `json:"desktopId"`
	Action    string `json:"action" jsonschema:"write, claim, release or close"`
	Data      string `json:"data,omitempty"`
	Sequence  uint64 `json:"sequence,omitempty"`
}
type desktopSession struct {
	reason        string
	id, box       string
	manager       *managerConnection
	conn          net.Conn
	cancel        context.CancelFunc
	timer         *time.Timer
	mu            sync.Mutex
	chunks        []terminalChunk
	bytes         int
	cursor, acked uint64
	done          bool
	wake, space   chan struct{}
	writeMu       sync.Mutex
	sequence      uint64
	lastDigest    [32]byte
}

func desktopResult(data map[string]any) *mcp.CallToolResult {
	// Stream bytes and capability handles MUST NOT appear in content or
	// structuredContent. _meta is delivered to the app, not model context.
	return &mcp.CallToolResult{Meta: mcp.Meta{"desktop": data}, Content: []mcp.Content{&mcp.TextContent{Text: "Desktop transport response for the app only."}}}
}
func (s *Server) desktopTools(server *mcp.Server) {
	appOnly := mcp.Meta{"ui": map[string]any{"visibility": []string{"app"}}}
	mcp.AddTool(server, &mcp.Tool{Name: "open_box_desktop", Description: "SHOW UI: open the live desktop of an authorized box in the MCP App canvas. May wake the box when its desktop connects. Does not require a terminal session. Pixels, clipboard, and input stay in app-only transport; no screenshots or desktop content are returned to the model.", Meta: mcp.Meta{"ui": map[string]any{"resourceUri": uiResource}}}, func(ctx context.Context, _ *mcp.CallToolRequest, input desktopTarget) (*mcp.CallToolResult, map[string]any, error) {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		info, err := s.Client.Desktop(ctx, input.BoxID, s.managerOptions.Parent)
		if err != nil {
			return nil, nil, err
		}
		data := map[string]any{"view": "desktop", "boxId": info.BoxID, "project": info.Project}
		return &mcp.CallToolResult{Meta: mcp.Meta{"connection": data}, Content: []mcp.Content{&mcp.TextContent{Text: "Desktop viewer requested. Desktop content remains outside model context."}}}, data, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "desktop_open", Description: "App-only VNC connection; returns a private session in _meta, never pixels in model-visible output.", Meta: appOnly}, func(ctx context.Context, _ *mcp.CallToolRequest, input desktopTarget) (*mcp.CallToolResult, any, error) {

		s.desktopMu.Lock()
		if len(s.desktops)+s.desktopOpening >= 4 {
			s.desktopMu.Unlock()
			return nil, nil, errors.New("too many desktop views; close an existing desktop")
		}
		s.desktopOpening++
		s.desktopMu.Unlock()
		defer func() { s.desktopMu.Lock(); s.desktopOpening--; s.desktopMu.Unlock() }()
		life, cancel := context.WithCancel(context.Background())
		stopRequest := context.AfterFunc(ctx, cancel)
		defer stopRequest()
		deadline := time.AfterFunc(45*time.Second, cancel)
		conn, info, err := s.Client.DialDesktop(life, input.BoxID, s.managerOptions.Parent)
		deadline.Stop()
		if err != nil {
			cancel()
			return nil, nil, err
		}
		var key [16]byte
		if _, err = rand.Read(key[:]); err != nil {
			cancel()
			conn.Close()
			return nil, nil, err
		}
		id := hex.EncodeToString(key[:])
		view := &desktopSession{id: id, box: info.BoxID, manager: s.boxController(info.BoxID), conn: conn, cancel: cancel, wake: make(chan struct{}), space: make(chan struct{})}
		s.desktopMu.Lock()
		if len(s.desktops) >= 4 {
			s.desktopMu.Unlock()
			cancel()
			conn.Close()
			return nil, nil, errors.New("too many desktop views")
		}
		s.desktops[id] = view
		view.timer = time.AfterFunc(desktopIdle, func() { s.closeDesktop(id) })
		s.desktopMu.Unlock()
		go view.readOutput()
		return desktopResult(map[string]any{"desktopId": id, "boxId": info.BoxID, "cursor": 0}), nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "desktop_read", Description: "App-only bounded VNC byte read with acknowledgement cursor. Graphics are exclusively in _meta. Long-polls for at most 12 seconds.", Meta: appOnly}, func(ctx context.Context, _ *mcp.CallToolRequest, input desktopReadInput) (*mcp.CallToolResult, any, error) {
		view, err := s.desktop(input.DesktopID)
		if err != nil {
			return nil, nil, err
		}
		data, err := view.read(ctx, input.After)
		if err != nil {
			return nil, nil, err
		}
		return desktopResult(data), nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "desktop_action", Description: "App-only VNC writes, focus ownership, or close. Never retry an uncertain input. VNC data is not model-visible.", Meta: appOnly}, func(ctx context.Context, _ *mcp.CallToolRequest, input desktopActionInput) (*mcp.CallToolResult, any, error) {
		view, err := s.desktop(input.DesktopID)
		if err != nil {
			return nil, nil, err
		}
		switch input.Action {
		case "claim":
			view.mu.Lock()
			if view.done {
				err = errors.New("desktop connection is closed")
			} else {
				err = view.manager.claim(view.id)
			}
			view.mu.Unlock()
		case "release":
			view.manager.release(view.id)
		case "close":
			s.closeDesktop(view.id)
		case "write":
			if len(input.Data) > base64.StdEncoding.EncodedLen(desktopInput) {
				return nil, nil, errors.New("desktop write exceeds 64 KiB")
			}
			data, decodeErr := base64.StdEncoding.DecodeString(input.Data)
			if decodeErr != nil || len(data) == 0 || len(data) > desktopInput {
				return nil, nil, errors.New("invalid desktop byte batch")
			}
			err = view.write(input.Sequence, data)
			if err != nil {
				s.closeDesktop(view.id)
			}
		default:
			err = errors.New("invalid desktop action")
		}
		if err != nil {
			return nil, nil, err
		}
		return desktopResult(map[string]any{"ok": true}), nil, nil
	})
}
func (s *Server) boxController(id string) *managerConnection {
	s.managersMu.Lock()
	defer s.managersMu.Unlock()
	if s.managers[id] == nil {
		s.managers[id] = &managerConnection{}
	}
	return s.managers[id]
}
func (s *Server) desktop(id string) (*desktopSession, error) {
	s.desktopMu.Lock()
	defer s.desktopMu.Unlock()
	view := s.desktops[id]
	if view == nil {
		return nil, errors.New("desktop view expired; reopen the desktop (no input was replayed)")
	}
	view.timer.Reset(desktopIdle)
	return view, nil
}
func (s *Server) closeDesktop(id string) {
	s.desktopMu.Lock()
	view := s.desktops[id]
	delete(s.desktops, id)
	s.desktopMu.Unlock()
	if view == nil {
		return
	}
	view.timer.Stop()
	view.cancel()
	view.conn.Close()
	view.mu.Lock()
	view.done = true
	close(view.wake)
	view.wake = make(chan struct{})
	close(view.space)
	view.space = make(chan struct{})
	view.mu.Unlock()
	view.manager.release(id)
}
func (v *desktopSession) write(sequence uint64, data []byte) error {
	v.writeMu.Lock()
	defer v.writeMu.Unlock()
	digest := sha256.Sum256(data)
	if sequence == v.sequence && sequence != 0 && digest == v.lastDigest {
		return nil
	}
	if sequence != v.sequence+1 {
		return errors.New("desktop write sequence mismatch; reopen without replaying input")
	}
	if err := v.conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	for len(data) > 0 {
		n, err := v.conn.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return errors.New("desktop write stalled")
		}
		data = data[n:]
	}
	v.sequence = sequence
	v.lastDigest = digest
	return nil
}
func (v *desktopSession) read(ctx context.Context, after uint64) (map[string]any, error) {
	hold := time.NewTimer(12 * time.Second)
	defer hold.Stop()
	for {
		v.mu.Lock()
		if after > v.cursor || after < v.acked {
			v.mu.Unlock()
			return nil, errors.New("desktop cursor expired; reopen without replaying input")
		}
		// Acknowledgement releases buffered bytes. The producer blocks at the cap,
		// preserving the VNC byte stream rather than dropping graphics mid-frame.
		for len(v.chunks) > 0 && v.chunks[0].cursor <= after {
			v.bytes -= len(v.chunks[0].data)
			v.chunks[0].data = nil
			v.chunks = v.chunks[1:]
		}
		v.acked = after
		close(v.space)
		v.space = make(chan struct{})
		chunks := []string{}
		var payload []byte
		cursor := after
		size := 0
		for _, chunk := range v.chunks {
			if size+len(chunk.data) > desktopReply {
				break
			}
			payload = append(payload, chunk.data...)
			cursor = chunk.cursor
			size += len(chunk.data)
		}
		if len(payload) > 0 {
			chunks = append(chunks, base64.StdEncoding.EncodeToString(payload))
		}
		done, wake, latest, reason := v.done, v.wake, v.cursor, v.reason
		v.mu.Unlock()
		if len(chunks) > 0 || done {
			return map[string]any{"chunks": chunks, "cursor": cursor, "closed": done && cursor == latest, "reason": reason}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-hold.C:
			return map[string]any{"chunks": chunks, "cursor": cursor, "closed": false}, nil
		case <-wake:
		}
	}
}
func (v *desktopSession) readOutput() {
	defer func() { v.mu.Lock(); v.done = true; close(v.wake); v.wake = make(chan struct{}); v.mu.Unlock() }()
	data := make([]byte, 32<<10)
	for {
		v.mu.Lock()
		for v.bytes+len(data) > desktopBuffer && !v.done {
			space := v.space
			v.mu.Unlock()
			<-space
			v.mu.Lock()
		}
		done := v.done
		v.mu.Unlock()
		if done {
			return
		}
		n, err := v.conn.Read(data)
		if n > 0 {
			v.mu.Lock()
			v.cursor++
			v.chunks = append(v.chunks, terminalChunk{cursor: v.cursor, data: append([]byte(nil), data[:n]...)})
			v.bytes += n
			close(v.wake)
			v.wake = make(chan struct{})
			v.mu.Unlock()
		}
		if err != nil {
			v.mu.Lock()
			v.reason = err.Error()
			v.mu.Unlock()
			return
		}
	}
}
