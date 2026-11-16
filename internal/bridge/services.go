package bridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/cloudnautique/discobox-mcp-ui/internal/discobox"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const forwardIdle = 15 * time.Minute
const forwardLimit = 8

// No application bytes pass through a tool result or model context.
type forwardInfo struct {
	ExpiresAt time.Time `json:"expiresAt,omitempty"`
	OpenedAt  time.Time `json:"openedAt"`
	ID        string    `json:"id"`
	BoxID     string    `json:"boxId"`
	Port      int       `json:"port"`
	Address   string    `json:"address"`
	URL       string    `json:"url,omitempty"`
	State     string    `json:"state"`
	Message   string    `json:"message"`
}
type serviceForward struct {
	target   discobox.ServicePort
	info     forwardInfo
	listener net.Listener
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	last     time.Time
	conns    map[net.Conn]bool
	slots    chan struct{}
}
type serviceInput struct {
	BoxID     string `json:"boxId"`
	ServiceID string `json:"serviceId,omitempty"`
	Action    string `json:"action,omitempty"`
	Port      int    `json:"port,omitempty"`
	ForwardID string `json:"forwardId,omitempty"`
}

type forwardInput struct {
	ForwardID string `json:"forwardId"`
}
type forwardListInput struct {
	BoxID string `json:"boxId,omitempty"`
}
type serviceTargetInput struct {
	BoxID     string `json:"boxId"`
	ServiceID string `json:"serviceId"`
}
type serviceActionInput struct {
	BoxID     string `json:"boxId"`
	ServiceID string `json:"serviceId"`
	Action    string `json:"action"`
}
type servicePortInput struct {
	BoxID     string `json:"boxId"`
	Port      int    `json:"port"`
	ServiceID string `json:"serviceId,omitempty"`
}

func (s *Server) forwards(box string) []forwardInfo {
	s.serviceMu.Lock()
	defer s.serviceMu.Unlock()
	out := []forwardInfo{}
	for _, f := range s.serviceForwards {
		if box == "" || f.info.BoxID == box {
			info := f.info
			f.mu.Lock()
			info.ExpiresAt = f.last.Add(forwardIdle)
			f.mu.Unlock()
			out = append(out, info)
		}
	}
	return out
}
func (s *Server) closedForwards(box string) []forwardInfo {
	s.serviceMu.Lock()
	defer s.serviceMu.Unlock()
	out := []forwardInfo{}
	for _, info := range s.serviceClosed {
		if box == "" || info.BoxID == box {
			out = append(out, info)
		}
	}
	return out
}
func (s *Server) closeForward(id string) error {
	return s.closeForwardReason(id, "Closed explicitly or during adapter shutdown")
}
func (s *Server) closeForwardReason(id, reason string) error {
	s.serviceMu.Lock()
	f := s.serviceForwards[id]
	delete(s.serviceForwards, id)
	if f != nil {
		info := f.info
		info.State = "closed"
		info.Message = reason
		s.serviceClosed = append(s.serviceClosed, info)
		if len(s.serviceClosed) > 16 {
			s.serviceClosed = s.serviceClosed[len(s.serviceClosed)-16:]
		}
	}
	s.serviceMu.Unlock()
	if f == nil {
		return errors.New("forward is already closed or expired")
	}
	f.cancel()
	f.listener.Close()
	f.mu.Lock()
	for c := range f.conns {
		c.Close()
	}
	f.mu.Unlock()
	return nil
}
func (s *Server) CloseServiceForwards() {
	for _, f := range s.forwards("") {
		s.closeForward(f.ID)
	}
}
func (s *Server) keepForward(id string) error {
	s.serviceMu.Lock()
	f := s.serviceForwards[id]
	s.serviceMu.Unlock()
	if f == nil {
		return errors.New("forward closed or expired; open it again explicitly")
	}
	f.mu.Lock()
	f.last = time.Now()
	f.mu.Unlock()
	return nil
}
func (s *Server) openForward(ctx context.Context, in serviceInput) (forwardInfo, error) {
	if in.Port < 1 || in.Port > 65535 {
		return forwardInfo{}, errors.New("select a currently reported TCP port")
	}
	if s.managerOptions.Parent != "" {
		return forwardInfo{}, errors.New("the sandbox API role does not allow TCP tunnels; open this server through an authorized host adapter (for example the MCPB). No local forward was opened")
	}
	select {
	case s.serviceOpenSlots <- struct{}{}:
		defer func() { <-s.serviceOpenSlots }()
	default:
		return forwardInfo{}, errors.New("connection tests are busy; wait before another explicit open")
	}
	// Revalidate even an existing forward before returning it.
	probe, port, e := s.Client.DialServicePort(ctx, in.BoxID, s.managerOptions.Parent, in.Port)
	if e != nil {
		var h *discobox.HTTPError
		if errors.As(e, &h) && h.Status == 403 {
			return forwardInfo{}, errors.New("Discobox denied TCP forwarding (403); an authorized user connection is required")
		}
		return forwardInfo{}, e
	}
	probe.Close()
	if in.ServiceID != "" && port.ServiceID != in.ServiceID {
		return forwardInfo{}, errors.New("this port no longer belongs to that service; refresh the catalog")
	}
	s.serviceMu.Lock()
	for _, f := range s.serviceForwards {
		if f.info.BoxID == in.BoxID && f.info.Port == in.Port {
			info := f.info
			s.serviceMu.Unlock()
			s.keepForward(info.ID)
			return info, nil
		}
	}
	if len(s.serviceForwards) >= forwardLimit {
		s.serviceMu.Unlock()
		return forwardInfo{}, errors.New("eight forwards are already open; close one before opening another")
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		s.serviceMu.Unlock()
		return forwardInfo{}, errors.New("could not reserve a loopback port")
	}
	var key [16]byte
	if _, e = rand.Read(key[:]); e != nil {
		listener.Close()
		s.serviceMu.Unlock()
		return forwardInfo{}, e
	}
	lifetime, cancel := context.WithCancel(context.Background())
	info := forwardInfo{OpenedAt: time.Now().UTC(), ID: hex.EncodeToString(key[:]), BoxID: in.BoxID, Port: in.Port, Address: listener.Addr().String(), State: "listening", Message: "Loopback listener on the adapter's machine. Initial TCP probe passed; current application readiness and authentication remain the service's responsibility."}
	if port.Protocol == "http" || port.Protocol == "https" {
		info.URL = port.Protocol + "://" + info.Address + "/"
	}
	f := &serviceForward{target: port, info: info, listener: listener, ctx: lifetime, cancel: cancel, last: time.Now(), conns: map[net.Conn]bool{}, slots: make(chan struct{}, 8)}
	s.serviceForwards[info.ID] = f
	s.serviceMu.Unlock()
	go s.serveForward(f)
	go s.watchForward(f)
	return info, nil
}
func (s *Server) watchForward(f *serviceForward) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-f.ctx.Done():
			return
		case <-ticker.C:
			f.mu.Lock()
			expired := time.Since(f.last) > forwardIdle
			f.mu.Unlock()
			if expired {
				s.closeForwardReason(f.info.ID, "Expired after 15 minutes without traffic or widget renewal")
				return
			}
			ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
			e := s.Client.BoxAccess(ctx, f.info.BoxID, s.managerOptions.Parent)
			cancel()
			if e != nil {
				s.closeForwardReason(f.info.ID, "Closed because box access could not be revalidated; refresh and reopen explicitly")
				return
			}
		}
	}
}
func (s *Server) serveForward(f *serviceForward) {
	for {
		local, e := f.listener.Accept()
		if e != nil {
			return
		}
		select {
		case f.slots <- struct{}{}:
		default:
			local.Close()
			continue
		}
		f.mu.Lock()
		f.conns[local] = true
		f.last = time.Now()
		f.mu.Unlock()
		go s.spliceForward(f, local)
	}
}
func (s *Server) spliceForward(f *serviceForward, local net.Conn) {
	defer func() { local.Close(); f.mu.Lock(); delete(f.conns, local); f.mu.Unlock(); <-f.slots }()
	remote, target, e := s.Client.DialServicePort(f.ctx, f.info.BoxID, s.managerOptions.Parent, f.info.Port)
	if e != nil {
		return
	}
	defer remote.Close()
	if target.Host != f.target.Host || target.Protocol != f.target.Protocol || target.ServiceID != f.target.ServiceID {
		s.closeForwardReason(f.info.ID, "Closed because the reported destination changed")
		return
	}
	touch := func() {
		f.mu.Lock()
		f.last = time.Now()
		f.mu.Unlock()
		local.SetDeadline(time.Now().Add(5 * time.Minute))
		remote.SetDeadline(time.Now().Add(5 * time.Minute))
	}
	touch()
	done := make(chan error, 2)
	go func() {
		_, e := io.Copy(&forwardWriter{Writer: remote, touch: touch}, local)
		if c, ok := remote.(interface{ CloseWrite() error }); ok {
			c.CloseWrite()
		}
		done <- e
	}()
	go func() {
		_, e := io.Copy(&forwardWriter{Writer: local, touch: touch}, remote)
		if c, ok := local.(interface{ CloseWrite() error }); ok {
			c.CloseWrite()
		}
		done <- e
	}()
	if e := <-done; e != nil {
		local.Close()
		remote.Close()
	}
	<-done
}

type forwardWriter struct {
	io.Writer
	touch func()
}

func (w *forwardWriter) Write(p []byte) (int, error) {
	n, e := w.Writer.Write(p)
	if n > 0 {
		w.touch()
	}
	return n, e
}
func (s *Server) servicesData(ctx context.Context, box string) (map[string]any, error) {
	catalog, e := s.Client.Services(ctx, box, s.managerOptions.Parent)
	if e != nil {
		return nil, e
	}
	return map[string]any{"view": "services", "boxId": box, "project": s.Client.Session.Project, "catalog": catalog, "forwards": s.forwards(box), "closedForwards": s.closedForwards(box), "forwardingAvailable": s.managerOptions.Parent == ""}, nil
}
func servicePrivate(data any) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Service app response; details are delivered privately to the widget."}}, Meta: mcp.Meta{"services": data}}
}
func (s *Server) serviceTools(server *mcp.Server) {
	s.serviceBrowserTool(server)
	ui := mcp.Meta{"ui": map[string]any{"resourceUri": uiResource}}
	private := mcp.Meta{"ui": map[string]any{"visibility": []string{"app"}}}
	mcp.AddTool(server, &mcp.Tool{Name: "list_services", Description: "SHOW UI: list declared services and reported ports in an authorized box. Service catalog access may wake it. Does not start a service or open a forward. Opens the Services workspace.", Meta: ui, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, in desktopTarget) (*mcp.CallToolResult, map[string]any, error) {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		data, e := s.servicesData(ctx, in.BoxID)
		if e != nil {
			return nil, nil, e
		}
		return workspaceResult(data)
	})
	mcp.AddTool(server, &mcp.Tool{Name: "control_service", Description: "Explicitly start, stop or restart a declared service by boxId/serviceId. Does not create declarations. No automatic retry after an uncertain response; inspect state before another action."}, func(ctx context.Context, _ *mcp.CallToolRequest, in serviceActionInput) (*mcp.CallToolResult, map[string]any, error) {
		ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		data, e := s.Client.ControlService(ctx, in.BoxID, s.managerOptions.Parent, in.ServiceID, in.Action)
		return nil, data, e
	})
	mcp.AddTool(server, &mcp.Tool{Name: "open_service", Description: "Open a loopback TCP forward for a currently reported/declared box port. May wake it. Requires an authorized host connection; sandbox API roles cannot tunnel. Does not start the service. The address is on the adapter's machine, not a hosted preview. HTTP(S) ports include a browser URL; bytes stay outside model context.", Meta: ui}, func(ctx context.Context, _ *mcp.CallToolRequest, in servicePortInput) (*mcp.CallToolResult, map[string]any, error) {
		ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		info, e := s.openForward(ctx, serviceInput{BoxID: in.BoxID, Port: in.Port, ServiceID: in.ServiceID})
		if e != nil {
			return nil, nil, e
		}
		return workspaceResult(map[string]any{"view": "services", "boxId": in.BoxID, "project": s.Client.Session.Project, "forward": info})
	})
	mcp.AddTool(server, &mcp.Tool{Name: "close_service_forward", Description: "Close only this adapter's selected forward and its connections. Does not stop the remote service."}, func(ctx context.Context, _ *mcp.CallToolRequest, in forwardInput) (*mcp.CallToolResult, map[string]any, error) {
		e := s.closeForward(in.ForwardID)
		return nil, map[string]any{"forwardId": in.ForwardID, "closed": e == nil}, e
	})
	mcp.AddTool(server, &mcp.Tool{Name: "list_service_forwards", Description: "List this adapter's active local forwards, optionally for a boxId. Read-only; does not renew leases or return application content.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, in forwardListInput) (*mcp.CallToolResult, map[string]any, error) {
		return nil, map[string]any{"forwards": s.forwards(in.BoxID), "closedForwards": s.closedForwards(in.BoxID)}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "read_service_logs", Description: "TEXT ONLY: explicitly read up to 64 KiB of recent untrusted service logs. Logs may contain sensitive application output; use the Services widget for private viewing.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, in serviceTargetInput) (*mcp.CallToolResult, map[string]any, error) {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		data, e := s.Client.ServiceLogs(ctx, in.BoxID, s.managerOptions.Parent, in.ServiceID)
		return nil, data, e
	})
	mcp.AddTool(server, &mcp.Tool{Name: "service_logs", Description: "App-only private service log read.", Meta: private}, func(ctx context.Context, _ *mcp.CallToolRequest, in serviceTargetInput) (*mcp.CallToolResult, any, error) {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		data, e := s.Client.ServiceLogs(ctx, in.BoxID, s.managerOptions.Parent, in.ServiceID)
		if e != nil {
			return nil, nil, e
		}
		return servicePrivate(data), nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "service_keepalive", Description: "App-only renew an existing local forward lease while its Services widget is visible. Does not create or reopen a forward.", Meta: private}, func(ctx context.Context, _ *mcp.CallToolRequest, in forwardInput) (*mcp.CallToolResult, any, error) {
		if e := s.keepForward(in.ForwardID); e != nil {
			return nil, nil, e
		}
		return servicePrivate(map[string]any{"renewed": true}), nil, nil
	})
}
func (s *Server) serviceHTTP(mux *http.ServeMux) {
	mux.Handle("POST /api/services/{action}", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in serviceInput
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
		d.DisallowUnknownFields()
		if e := d.Decode(&in); e != nil {
			http.Error(w, "invalid service request", 400)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		var data any
		var e error
		switch r.PathValue("action") {
		case "list_services":
			data, e = s.servicesData(ctx, in.BoxID)
		case "control_service":
			data, e = s.Client.ControlService(ctx, in.BoxID, s.managerOptions.Parent, in.ServiceID, in.Action)
		case "open_service":
			data, e = s.openForward(ctx, in)
			if e == nil {
				data = map[string]any{"forward": data}
			}
		case "close_service_forward":
			e = s.closeForward(in.ForwardID)
			data = map[string]any{"closed": e == nil}
		case "list_service_forwards":
			data = map[string]any{"forwards": s.forwards(in.BoxID), "closedForwards": s.closedForwards(in.BoxID)}
		case "service_open_browser":
			data, e = s.openServiceBrowser(in.ForwardID)
		case "service_logs":
			data, e = s.Client.ServiceLogs(ctx, in.BoxID, s.managerOptions.Parent, in.ServiceID)
		case "service_keepalive":
			e = s.keepForward(in.ForwardID)
			data = map[string]any{"renewed": e == nil}
		default:
			http.NotFound(w, r)
			return
		}
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(data)
	})))
}
