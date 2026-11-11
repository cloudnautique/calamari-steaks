package discobox

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/cloudnautique/discobox-mcp-ui/internal/testhttp"
	"github.com/coder/websocket"
)

func TestAttachReadsFramesAcrossWebSocketMessages(t *testing.T) {
	network := testhttp.NewNetwork(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server := network.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/projects/p/sandboxes/b/execs/e/attach" {
			t.Errorf("unexpected attach: %s", r.URL)
		}
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer ws.CloseNow()
		kind, ready, err := ws.Read(ctx)
		if err != nil || kind != websocket.MessageBinary || string(ready) != string([]byte{Ready, 0, 0, 0, 0}) {
			t.Errorf("ready handshake: %v %v", ready, err)
			return
		}
		// The header and a multibyte UTF-8 character both straddle WS messages.
		payload := []byte("ready ✓")
		frame := make([]byte, 5+len(payload))
		frame[0] = Stdout
		binary.BigEndian.PutUint32(frame[1:5], uint32(len(payload)))
		copy(frame[5:], payload)
		for _, chunk := range [][]byte{frame[:2], frame[2 : len(frame)-1], frame[len(frame)-1:]} {
			if err := ws.Write(ctx, websocket.MessageBinary, chunk); err != nil {
				t.Error(err)
				return
			}
		}
		ws.Read(ctx) // Keep the stream open until the client detaches.
	}))
	defer server.Close()
	client, err := New(server.URL, "", Session{Project: "p", Box: "b", Exec: "e"})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := client.Attach(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := stream.Write(Ready, nil); err != nil {
		t.Fatal(err)
	}
	kind, data, err := stream.Read()
	if err != nil || kind != Stdout || string(data) != "ready ✓" {
		t.Fatalf("read: %d %q %v", kind, data, err)
	}
}

func TestRejectsOversizedFrameBeforeAllocation(t *testing.T) {
	reader, writer := net.Pipe()
	defer reader.Close()
	go func() {
		defer writer.Close()
		var header [5]byte
		header[0] = Stdout
		binary.BigEndian.PutUint32(header[1:], maxFrame+1)
		writer.Write(header[:])
	}()
	stream := &Stream{conn: reader}
	if _, _, err := stream.Read(); err == nil || err == io.EOF {
		t.Fatalf("oversized frame not rejected: %v", err)
	}
}
