package discobox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
)

const DesktopServiceID = "ai.discobox.desktop"

type DesktopInfo struct {
	BoxID   string `json:"boxId"`
	Project string `json:"project"`
	Port    int    `json:"port"`
}

// Discover the declared desktop, never an arbitrary caller-supplied port/URL.
// Metadata reads do not depend on a primary exec and do not wake the box.
func (c *Client) Desktop(ctx context.Context, id, parent string) (DesktopInfo, error) {
	box, err := c.managerBox(ctx, id, parent)
	if err != nil {
		return DesktopInfo{}, err
	}
	if err = managerLifecycleError(box); err != nil {
		return DesktopInfo{}, err
	}
	runtime, _ := box["runtime"].(map[string]any)
	status, _ := runtime["agentStatus"].(map[string]any)
	ports, _ := status["ports"].([]any)
	info := DesktopInfo{BoxID: id, Project: c.Session.Project}
	for _, raw := range ports {
		port, _ := raw.(map[string]any)
		if port["serviceId"] != DesktopServiceID {
			continue
		}
		number, ok := port["port"].(float64)
		if !ok || number != float64(int(number)) || number < 1 || number > 65535 || port["protocol"] != "http" {
			return DesktopInfo{}, errors.New("invalid declared desktop service")
		}
		if info.Port != 0 {
			return DesktopInfo{}, errors.New("ambiguous desktop service declaration")
		}
		info.Port = int(number)
	}
	if info.Port == 0 {
		return DesktopInfo{}, errors.New("no declared desktop is available; check desktop provisioning in Discobox and retry once its service is reported")
	}
	return info, nil
}

// DialDesktop is server-side only. The canvas never receives this URL or an
// upstream credential. Establishing the HTTP tunnel may wake a stopped box.
func (c *Client) DialDesktop(ctx context.Context, id, parent string) (net.Conn, DesktopInfo, error) {
	info, err := c.Desktop(ctx, id, parent)
	if err != nil {
		return nil, info, err
	}

	// The ordinary /http proxy dials the container IP; desktop services bind
	// loopback. Carry the viewer WebSocket over the sandbox-agent TCP tunnel.
	transport := &http.Transport{DialContext: func(_ context.Context, _, _ string) (net.Conn, error) { return c.dialDesktopTCP(ctx, info) }}
	client := &http.Client{Transport: transport, CheckRedirect: c.http.CheckRedirect}
	ws, response, err := websocket.Dial(ctx, "ws://desktop.local/websockify", &websocket.DialOptions{HTTPClient: client, Subprotocols: []string{"binary"}})
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		var refusal *HTTPError
		if errors.As(err, &refusal) && refusal.Status == http.StatusForbidden {
			return nil, info, errors.New("Discobox denied desktop TCP tunnel access (403). The sandbox API role does not grant this permission; use an authorized host connection")
		}
		return nil, info, fmt.Errorf("desktop connection failed: %w", err)
	}
	ws.SetReadLimit(16 << 20)
	return websocket.NetConn(ctx, ws, websocket.MessageBinary), info, nil
}

func (c *Client) dialDesktopTCP(ctx context.Context, info DesktopInfo) (net.Conn, error) {
	return c.dialTCP(ctx, info.BoxID, "127.0.0.1", info.Port)
}
func (c *Client) dialTCP(ctx context.Context, id, host string, port int) (net.Conn, error) {
	route := "/api/projects/" + c.Session.Project + "/sandboxes/" + id + "/tcp/attach"
	query := url.Values{"host": []string{host}, "port": []string{strconv.Itoa(port)}}
	client := &http.Client{Transport: c.http.Transport, CheckRedirect: c.http.CheckRedirect}
	dialCtx, cancelDial := context.WithTimeout(ctx, 12*time.Second)
	defer cancelDial()
	ws, response, err := websocket.Dial(dialCtx, strings.Replace(c.base, "http", "ws", 1)+route+"?"+query.Encode(), &websocket.DialOptions{HTTPClient: client, HTTPHeader: c.headers()})
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		if response != nil {
			return nil, &HTTPError{Status: response.StatusCode, Path: route}
		}
		return nil, errors.New("TCP tunnel refused or unreachable")
	}
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	ws.SetReadLimit(1 << 20)
	conn := websocket.NetConn(ctx, ws, websocket.MessageBinary)
	return &desktopTunnelConn{Conn: conn, framed: &Stream{conn: conn}}, nil
}

type desktopTunnelConn struct {
	net.Conn
	framed  *Stream
	pending []byte
}

func (c *desktopTunnelConn) Read(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	for len(c.pending) == 0 {
		kind, body, err := c.framed.Read()
		if err != nil {
			return 0, err
		}
		switch kind {
		case Stdout:
			c.pending = body
		case CloseOutput:
			return 0, io.EOF
		case Error:
			return 0, errors.New("TCP tunnel ended with an upstream error")
		}
	}
	n := copy(data, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}
func (c *desktopTunnelConn) Write(data []byte) (int, error) {
	if err := c.framed.Write(Input, data); err != nil {
		return 0, err
	}
	return len(data), nil
}

// CloseWrite preserves TCP half-close through Discobox framing.
func (c *desktopTunnelConn) CloseWrite() error { return c.framed.Write(CloseInput, nil) }
