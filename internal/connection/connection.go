// Package connection owns local onboarding state, never backend authorization.
package connection

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/cloudnautique/discobox-mcp-ui/internal/discobox"
)

func canonical(c Config) Config {
	if len(c.Relays) == 0 {
		c.Relays = nil
	}
	return c
}

type Config struct {
	Endpoint string   `json:"endpoint"`
	Project  string   `json:"project"`
	Relays   []string `json:"relays,omitempty"`
}

func DefaultPath() (string, error) {
	p, e := os.UserConfigDir()
	return filepath.Join(p, "discobox-mcp", "connection.json"), e
}
func LocalAddress() string {
	if runtime.GOOS == "windows" {
		return "npipe://"
	}
	return "unix://"
}

func Resolve(raw string) string {
	if raw == "" || raw == "unix://" || raw == "npipe://" {
		return discobox.DefaultLocalEndpoint()
	}
	return raw
}
func Validate(c Config) error {
	if len(c.Endpoint) > 4096 || len(c.Project) > 256 || len(c.Relays) > 4 {
		return errors.New("connection settings are too large")
	}
	resolved := Resolve(c.Endpoint)
	// Constructing a catalog validates the endpoint/project without dialing.
	if e := discobox.ValidateConnection(resolved, c.Project); e != nil {
		return errors.New("use unix://, an absolute Unix socket/Windows named-pipe URL, an HTTP(S) origin, or discobox://<server-peer-id>, and a valid project ID")
	}
	if len(c.Relays) > 0 && !strings.HasPrefix(resolved, "discobox://") {
		return errors.New("relay settings require a remote peer connection")
	}
	for _, raw := range c.Relays {
		u, e := url.Parse(raw)
		if e != nil || len(raw) > 2048 || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("relay URLs must be HTTP(S) URLs without credentials, query, or fragment")
		}
	}
	return nil
}
func Load(path string) (Config, error) {
	var c Config
	f, e := os.Open(path)
	if e != nil {
		return c, e
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 65537))
	d.DisallowUnknownFields()
	if e = d.Decode(&c); e != nil {
		return c, errors.New("saved connection settings are invalid; open connection setup to replace them")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return c, errors.New("saved connection settings contain extra data")
	}
	if e = Validate(c); e != nil {
		return c, e
	}
	return c, nil
}
func save(path string, c Config) error {
	if e := Validate(c); e != nil {
		return e
	}
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return errors.New("cannot create connection settings directory")
	}
	data, _ := json.MarshalIndent(c, "", "  ")
	data = append(data, '\n')
	f, e := os.CreateTemp(filepath.Dir(path), ".connection-*")
	if e != nil {
		return errors.New("cannot create private connection settings")
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, e = f.Write(data); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return errors.New("cannot save connection settings")
	}
	return nil
}

type Result struct {
	State   string `json:"state"`
	Message string `json:"message"`
	// Steps is the layer-by-layer diagnosis behind State, when one ran.
	Steps         []discobox.Step `json:"steps,omitempty"`
	ServerVersion string          `json:"serverVersion,omitempty"`
}
type Status struct {
	Result
	Config            Config `json:"config"`
	ResolvedEndpoint  string `json:"resolvedEndpoint,omitempty"`
	PeerID            string `json:"peerId,omitempty"`
	EnrollmentCommand string `json:"enrollmentCommand,omitempty"`
	Managed           bool   `json:"managed"`
	RestartRequired   bool   `json:"restartRequired"`
	LocalDefault      string `json:"localDefault"`
	Scope             string `json:"scope"`
	// LastSuccess and LastFailure record real backend traffic, not only
	// explicit tests, so status reflects whether the connection works now.
	LastSuccess *time.Time `json:"lastSuccess,omitempty"`
	LastFailure *time.Time `json:"lastFailure,omitempty"`
	// CanApply means saved settings take effect without a restart.
	CanApply bool `json:"canApply"`
}

// Apply builds, tests and switches to a connection generation for config.
type Apply func(context.Context, Config) (Result, error)
type Probe func(context.Context, Config) Result

// Controller keeps one immutable active connection per process. Saving a new
// profile does not retarget any existing terminal, file, approval, or desktop.
type Controller struct {
	mu                 sync.Mutex
	probeMu            sync.Mutex
	Current            Config
	Path, IdentityPath string
	Managed            bool
	Probe              Probe
	status             Result
	pending            *Config
	peer               string
	needsRestart       bool
	// Apply, when set, switches the running adapter to saved settings.
	Apply       Apply
	lastSuccess time.Time
	lastFailure time.Time
}

// Observe records the outcome of real backend traffic for status.
func (c *Controller) Observe(ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ok {
		c.lastSuccess = time.Now()
		if c.status.State == "not_tested" {
			c.status = Result{State: "connected", Message: "Connected: the box catalog was read successfully."}
		}
	} else {
		c.lastFailure = time.Now()
	}
}

func New(c Config, path, identity string, managed bool, probe Probe, initialError error) *Controller {
	c = canonical(c)
	state := Result{State: "not_tested", Message: "Test this connection. Setup never creates a box."}
	if initialError != nil {
		state = Result{State: "setup_required", Message: "Connection initialization failed. Review settings and identity, then save them again."}
	}
	return &Controller{Current: c, Path: path, IdentityPath: identity, Managed: managed, Probe: probe, status: state, needsRestart: initialError != nil}
}
func (c *Controller) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	display := c.Current
	if Validate(display) != nil {
		display = Config{Endpoint: "", Project: "default"}
	}
	s := Status{LocalDefault: LocalAddress(), Result: c.status, Config: display, ResolvedEndpoint: "", Managed: c.Managed, PeerID: c.peer, RestartRequired: c.pending != nil, CanApply: c.Apply != nil && !c.Managed, Scope: "Peer enrollment grants the server's default-user access across authorized projects; selecting a project does not narrow that credential."}
	if !c.lastSuccess.IsZero() {
		at := c.lastSuccess
		s.LastSuccess = &at
	}
	if !c.lastFailure.IsZero() {
		at := c.lastFailure
		s.LastFailure = &at
	}
	if display.Endpoint != "" {
		s.ResolvedEndpoint = Resolve(display.Endpoint)
	}
	if c.pending != nil {
		s.Config = *c.pending
		if c.status.State != "" && c.status.State != "not_tested" && c.status.State != "connected" && c.status.State != "setup_required" {
			s.Result = c.status
		} else {
			s.Result = Result{State: "restart_required", Message: "Settings saved. Restart this MCP adapter to apply them; existing views stay on the current connection."}
		}
		s.ResolvedEndpoint = Resolve(s.Config.Endpoint)
	}
	if c.peer != "" {
		s.EnrollmentCommand = "discobox --server unix:// admin peer add " + c.peer + " --name discobox-mcp"
	}
	return s
}
func (c *Controller) Identity() (Status, error) {
	_, id, e := discobox.PeerIdentity(c.IdentityPath)
	if e != nil {
		return c.Status(), errors.New("identity could not be loaded; keep the existing key and repair it explicitly, rather than generating a replacement")
	}
	c.mu.Lock()
	c.peer = id.String()
	c.mu.Unlock()
	return c.Status(), nil
}
func (c *Controller) Test(ctx context.Context, candidate Config) (Result, error) {
	candidate = canonical(candidate)
	if e := Validate(candidate); e != nil {
		return Result{}, e
	}
	if !c.Managed && candidate.Endpoint != c.Current.Endpoint && !strings.HasPrefix(candidate.Endpoint, "unix://") && !strings.HasPrefix(candidate.Endpoint, "npipe://") && !strings.HasPrefix(candidate.Endpoint, "discobox://") {
		return Result{}, errors.New("choose a local socket or a pinned remote peer")
	}
	if c.Managed && !reflect.DeepEqual(candidate, c.Current) {
		return Result{}, errors.New("this connection is pinned by the launcher; change its flags or extension settings")
	}
	if !c.probeMu.TryLock() {
		return Result{}, errors.New("a connection test is already running")
	}
	defer c.probeMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	result := c.Probe(ctx, candidate)
	c.mu.Lock()
	if reflect.DeepEqual(candidate, c.Current) {
		c.status = result
	}
	c.mu.Unlock()
	return result, nil
}

// Save stores the profile and, when the adapter can, applies it at once:
// new operations use it, open views stay on the previous connection.
func (c *Controller) Save(candidate Config) (Status, error) {
	return c.SaveContext(context.Background(), candidate)
}
func (c *Controller) SaveContext(ctx context.Context, candidate Config) (Status, error) {
	candidate = canonical(candidate)
	if c.Managed {
		return c.Status(), errors.New("this connection is pinned by the launcher; change its flags or extension settings")
	}
	// The widget offers local sockets and pinned Iroh peers, not a general proxy.
	if !strings.HasPrefix(candidate.Endpoint, "unix://") && !strings.HasPrefix(candidate.Endpoint, "npipe://") && !strings.HasPrefix(candidate.Endpoint, "discobox://") {
		return c.Status(), errors.New("choose This computer or a pinned discobox:// server")
	}
	c.mu.Lock()
	if e := save(c.Path, candidate); e != nil {
		c.mu.Unlock()
		return c.Status(), e
	}
	changed := !reflect.DeepEqual(candidate, c.Current) || c.needsRestart
	if changed {
		copy := candidate
		c.pending = &copy
	} else {
		c.pending = nil
	}
	apply := c.Apply
	c.mu.Unlock()
	if !changed || apply == nil {
		return c.Status(), nil
	}
	if !c.probeMu.TryLock() {
		return c.Status(), errors.New("a connection test is running; save again when it finishes")
	}
	defer c.probeMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	result, e := apply(ctx, candidate)
	c.mu.Lock()
	if e == nil {
		c.Current, c.pending, c.needsRestart = candidate, nil, false
		result.Message = "Connected and applied. New requests use this connection; views that are already open stay on the previous one until closed."
		c.status = result
	} else {
		result.Message = "Saved, but not applied: " + result.Message + " Fix the problem and save again, or restart the adapter."
		c.status = result
	}
	c.mu.Unlock()
	return c.Status(), nil
}

// ProbeConnection diagnoses each layer to the server over the connection's
// own transport, then reads the project's catalog. It never creates or
// starts anything.
func ProbeConnection(ctx context.Context, conn *discobox.Connection) Result {
	steps, version := conn.Diagnose(ctx)
	for _, step := range steps {
		if step.Status != "failed" {
			continue
		}
		result := Result{Steps: steps, ServerVersion: version, State: "unreachable", Message: step.Summary}
		switch step.Layer {
		case "address":
			result.State = "invalid_settings"
		case "runtime", "identity", "bind":
			result.State = "identity_error"
		case "relay":
			result.State = "relay_unreachable"
		case "admission":
			result.State = "not_admitted"
		case "server":
			result.State = "server_unavailable"
		}
		if step.Hint != "" {
			result.Message += ". " + step.Hint
		}
		return result
	}
	result := probeCatalog(ctx, conn.Client)
	result.Steps, result.ServerVersion = steps, version
	return result
}

// ProbeConfigured performs only a bounded project catalog GET. The caller owns
// Iroh transport configuration; the CLI runs candidate tests in an isolated process.
func ProbeConfigured(ctx context.Context, c Config, token string) Result {
	client, e := discobox.NewCatalog(Resolve(c.Endpoint), token, c.Project)
	if e != nil {
		return Result{State: "invalid_settings", Message: "Invalid connection settings."}
	}
	return probeCatalog(ctx, client)
}

func probeCatalog(ctx context.Context, client *discobox.Client) Result {
	_, e := client.ListBoxes(ctx, "")
	if e == nil {
		return Result{State: "connected", Message: "Connected. The selected project's box catalog is readable; no box was started or created."}
	}
	var h *discobox.HTTPError
	switch {
	case errors.As(e, &h) && h.Status == 401:
		return Result{State: "unauthorized", Message: "The API requires authorization. For Iroh, enroll this adapter using an already-authorized operator."}
	case errors.As(e, &h) && h.Status == 403:
		return Result{State: "forbidden", Message: "This connection is not allowed to read the selected project. Check operator enrollment and project access."}
	case errors.Is(e, os.ErrNotExist):
		return Result{State: "socket_missing", Message: "The local socket was not found. Start Discobox, choose a custom socket, or select Remote computer."}
	case errors.Is(e, os.ErrPermission):
		return Result{State: "socket_denied", Message: "The local socket could not be opened with this user's filesystem permissions."}
	default:
		return Result{State: "unreachable", Message: "Could not reach or verify Discobox. Check the server listener, address, relay, and network. A timeout does not establish enrollment denial."}
	}
}
