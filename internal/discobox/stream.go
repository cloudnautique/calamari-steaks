package discobox

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Wire values follow discobox/pkg/execstream/frame at reference commit e9444637.
const (
	Input    byte = 0
	Stdout   byte = 1
	Stderr   byte = 2
	Resize   byte = 3
	Error    byte = 5
	Exit     byte = 6
	Ready    byte = 8
	Repaint  byte = 14
	maxFrame      = 16 << 20
)

type Stream struct {
	conn net.Conn
	mu   sync.Mutex
}

func (c *Client) Attach(ctx context.Context) (*Stream, error) {
	// A terminal view needs the existing screen when attaching to an idle
	// session, not only output produced after it connected. Ready releases replay.
	address := strings.Replace(c.base, "http", "ws", 1) + c.execPath() + "/attach?replay=true"
	// WebSocket lifetime is controlled by ctx, not the JSON request timeout.
	httpClient := &http.Client{Transport: c.http.Transport, CheckRedirect: c.http.CheckRedirect}
	ws, response, err := websocket.Dial(ctx, address, &websocket.DialOptions{HTTPClient: httpClient, HTTPHeader: c.headers()})
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		if response != nil {
			return nil, &HTTPError{Status: response.StatusCode, Path: c.execPath() + "/attach"}
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("manager terminal attach canceled before it became ready: %w", ctx.Err())
		}
		return nil, errors.New("could not attach to manager; it may still be starting or be unreachable; reconnect to the same manager before sending input")
	}
	ws.SetReadLimit(maxFrame + 5)
	return &Stream{conn: websocket.NetConn(ctx, ws, websocket.MessageBinary)}, nil
}

func (s *Stream) Close() error { return s.conn.Close() }

func (s *Stream) Read() (byte, []byte, error) {
	var header [5]byte
	if _, err := io.ReadFull(s.conn, header[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(header[1:])
	if n > maxFrame {
		return 0, nil, errors.New("terminal frame exceeds 16 MiB")
	}
	data := make([]byte, int(n))
	_, err := io.ReadFull(s.conn, data)
	return header[0], data, err
}

func (s *Stream) Write(kind byte, data []byte) error {
	if len(data) > maxFrame {
		return errors.New("terminal frame exceeds 16 MiB")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	buffer := make([]byte, 5+len(data))
	buffer[0] = kind
	binary.BigEndian.PutUint32(buffer[1:5], uint32(len(data)))
	copy(buffer[5:], data)
	for len(buffer) > 0 {
		n, err := s.conn.Write(buffer)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		buffer = buffer[n:]
	}
	return nil
}

func (s *Stream) Resize(cols, rows int) error {
	if cols < 2 || cols > 500 || rows < 2 || rows > 200 {
		return errors.New("terminal dimensions out of bounds")
	}
	data, _ := json.Marshal(map[string]int{"cols": cols, "rows": rows})
	return s.Write(Resize, data)
}
