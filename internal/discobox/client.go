// Package discobox is a deliberately small client of the public Discobox API.
// The external MCP server does not import or change the control plane.
package discobox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	endpointpkg "github.com/discobox-ai/discobox/pkg/endpoint"
)

type Session struct {
	Project string `json:"project"`
	Box     string `json:"box"`
	Exec    string `json:"exec"`
}

var identifier = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func (s Session) Validate() error {
	for _, value := range []string{s.Project, s.Box, s.Exec} {
		if !identifier.MatchString(value) {
			return errors.New("project, box and exec must be IDs containing only letters, digits, underscores or hyphens")
		}
	}
	return nil
}

type Client struct {
	endpoint string
	base     string
	http     *http.Client
	token    string
	Session  Session
	// RoleTagPending names the discovery role a launch could not confirm on
	// its box. Empty when the tag was written or was not needed.
	RoleTagPending string
}

func New(endpoint, token string, session Session) (*Client, error) {
	if err := session.Validate(); err != nil {
		return nil, err
	}
	return newClient(endpoint, token, session)
}

// DefaultLocalEndpoint shares Discobox's platform resolver, including its
// explicit DISCOBOX_SERVER_ADDRESS override. It never starts a server.
func DefaultLocalEndpoint() string { return endpointpkg.DefaultEndpoint() }

// ValidateConnection checks configuration without creating an Iroh transport or dialing.
func ValidateConnection(endpoint, project string) error {
	if !identifier.MatchString(project) {
		return errors.New("invalid project ID")
	}
	_, err := parseClientEndpoint(endpoint)
	return err
}
func parseClientEndpoint(endpoint string) (endpointpkg.Endpoint, error) {
	if endpoint == "unix://" || endpoint == "npipe://" {
		endpoint = DefaultLocalEndpoint()
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpointpkg.Endpoint{}, fmt.Errorf("parse Discobox endpoint: %w", err)
	}
	if u.User != nil || u.ForceQuery || (u.RawQuery != "" && u.Scheme != "discobox") || u.Fragment != "" || u.Opaque != "" {
		return endpointpkg.Endpoint{}, errors.New("endpoint must not contain credentials, a query or a fragment")
	}
	switch u.Scheme {
	case "unix":
		if u.Host != "" || u.Path == "" {
			return endpointpkg.Endpoint{}, errors.New("use unix:///absolute/path/to/server.sock")
		}
	case "npipe":
		if u.Host != "" || !strings.HasPrefix(u.Path, "//./pipe/") || len(u.Path) <= len("//./pipe/") {
			return endpointpkg.Endpoint{}, errors.New("use npipe:////./pipe/discobox for a local Windows named pipe")
		}
	case "http", "https":
		if u.Host == "" || (u.Path != "" && u.Path != "/") {
			return endpointpkg.Endpoint{}, errors.New("HTTP endpoint must be an origin without a path")
		}
	case "discobox":
		if _, err := peerEndpoint(endpoint); err != nil {
			return endpointpkg.Endpoint{}, err
		}
	default:
		return endpointpkg.Endpoint{}, errors.New("endpoint must use http, https, unix, npipe or discobox")
	}
	return endpointpkg.Parse(endpoint)
}

// newClient also supports the unbound client used during manager creation.
func newClient(endpoint, token string, session Session) (*Client, error) {
	if endpoint == "unix://" || endpoint == "npipe://" {
		endpoint = DefaultLocalEndpoint()
	}
	parsed, err := parseClientEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if parsed.Scheme == "unix" || parsed.Scheme == "npipe" {
		transport.Proxy = nil
	}
	base, client, err := endpointpkg.HTTPClient(parsed, transport)
	if err != nil {
		return nil, err
	}
	client.Timeout = 75 * time.Second
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{endpoint: strings.TrimRight(endpoint, "/"), base: strings.TrimRight(base, "/"), token: token, Session: session, http: client}, nil
}

func (c *Client) execPath() string {
	return "/api/projects/" + c.Session.Project + "/sandboxes/" + c.Session.Box + "/execs/" + c.Session.Exec
}

func (c *Client) headers() http.Header {
	h := make(http.Header)
	if c.token != "" {
		h.Set("Authorization", "Bearer "+c.token)
	}
	return h
}

const defaultResponseLimit = 2 << 20

// listingResponseLimit admits a whole project catalog, whose entries carry
// each box's full task prompt (up to 64 KiB).
const listingResponseLimit = 32 << 20

func (c *Client) request(ctx context.Context, method, path string, body any) (map[string]any, error) {
	return c.requestLimited(ctx, method, path, body, defaultResponseLimit)
}

func (c *Client) requestLimited(ctx context.Context, method, path string, body any, limit int) (map[string]any, error) {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}
	r, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	r.Header = c.headers()
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(r)
	if err != nil {
		return nil, fmt.Errorf("Discobox request failed: %w", err)
	}
	defer response.Body.Close()
	data, err = io.ReadAll(io.LimitReader(response.Body, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("Discobox response exceeded %d MiB; request less output", limit>>20)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Do not forward arbitrary upstream error bodies or credential-bearing URLs.
		return nil, &HTTPError{Status: response.StatusCode, Path: path}
	}
	// Discobox archives acknowledge DELETE with 202 and no response body.
	if len(data) == 0 && (response.StatusCode == http.StatusNoContent || (method == http.MethodDelete && response.StatusCode == http.StatusAccepted)) {
		return map[string]any{}, nil
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("invalid Discobox JSON: %w", err)
	}
	return result, nil
}

type HTTPError struct {
	Status int
	Path   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("Discobox returned HTTP %d for %s", e.Status, e.Path)
}

// ErrNoPrimary means the box has no primary session yet, usually because it
// is still provisioning or its harness has not started.
var ErrNoPrimary = errors.New("this box has no terminal session yet; it may still be starting")

// ResolvePrimary pins the current primary exec. The bridge re-resolves only on
// an explicit reopen, never to silently redirect input to a new process.
func (c *Client) ResolvePrimary(ctx context.Context) error {
	if c.Session.Exec != "primary" {
		return nil
	}
	path := "/api/projects/" + c.Session.Project + "/sandboxes/" + c.Session.Box + "/execs"
	result, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	items, _ := result["execs"].([]any)
	var found string
	for _, item := range items {
		entry, _ := item.(map[string]any)
		if entry["primary"] == true {
			if found != "" {
				return errors.New("Discobox reported more than one primary session for this box; open it in the Discobox console to choose one")
			}
			found, _ = entry["id"].(string)
		}
	}
	if found == "" {
		return ErrNoPrimary
	}
	if !identifier.MatchString(found) {
		return errors.New("invalid primary exec ID from Discobox")
	}
	c.Session.Exec = found
	return nil
}

func (c *Client) Screen(ctx context.Context, scrollback int) (map[string]any, error) {
	if scrollback < 0 || scrollback > 500 {
		return nil, errors.New("scrollback must be between 0 and 500")
	}
	result, err := c.request(ctx, http.MethodGet, fmt.Sprintf("%s/screen?scrollback=%d", c.execPath(), scrollback), nil)
	return result, sessionError(err)
}

func (c *Client) Send(ctx context.Context, text string, submit bool) (map[string]any, error) {
	if strings.TrimSpace(text) == "" || len(text) > 64<<10 {
		return nil, errors.New("message must contain 1 to 65536 bytes of text")
	}
	parts := []map[string]string{{"text": text}}
	if submit {
		parts = append(parts, map[string]string{"key": "Enter"})
	}
	result, err := c.request(ctx, http.MethodPost, c.execPath()+"/input", map[string]any{"input": parts})
	return result, sessionError(err)
}

func (c *Client) Interrupt(ctx context.Context) (map[string]any, error) {
	result, err := c.request(ctx, http.MethodPost, c.execPath()+"/input", map[string]any{"input": []map[string]string{{"key": "C-c"}}})
	return result, sessionError(err)
}

// MaxWaitSeconds keeps one wait inside the ~60 second tool-call budget that
// MCP hosts apply, leaving room for transport latency and the response.
const MaxWaitSeconds = 45

// Wait conditions. Each one also ends on the exec exiting.
const (
	UntilSettled    = "settled"     // any reported idle, done, blocked or error state, or a Stop/Notification hook
	UntilTurnEnd    = "turn_end"    // the agent's turn ended: Stop hook or idle, done or error state
	UntilNeedsInput = "needs_input" // the agent is blocked on the person: Notification hook or blocked state
	UntilQuiet      = "quiet"       // no output or input for QuietSeconds
)

type WaitOptions struct {
	// After is a resumeAfter cursor. Without it the wait counts from the last
	// input this adapter sent, or from the start of the wait if none was sent.
	After        string
	Until        string
	QuietSeconds int
	Seconds      int
}

func waitStates(states ...string) []map[string]string {
	result := make([]map[string]string, 0, len(states))
	for _, state := range states {
		result = append(result, map[string]string{"state": state})
	}
	return result
}

func waitCondition(options WaitOptions) (map[string]any, error) {
	until := map[string]any{"exit": true}
	switch options.Until {
	case "", UntilSettled:
		until["programStates"] = waitStates("idle", "done", "blocked", "error")
		until["hookEvents"] = []string{"Stop", "Notification"}
	case UntilTurnEnd:
		until["programStates"] = waitStates("idle", "done", "error")
		until["hookEvents"] = []string{"Stop"}
	case UntilNeedsInput:
		until["programStates"] = waitStates("blocked")
		until["hookEvents"] = []string{"Notification"}
	case UntilQuiet:
		quiet := options.QuietSeconds
		if quiet == 0 {
			quiet = 10
		}
		if quiet < 1 || quiet > 600 {
			return nil, errors.New("quietSeconds must be between 1 and 600")
		}
		until["quietSeconds"] = quiet
	default:
		return nil, errors.New("until must be settled, turn_end, needs_input or quiet")
	}
	if options.After != "" {
		until["after"] = options.After
	} else {
		until["fromLastInput"] = true
	}
	return until, nil
}

// Wait blocks until a condition holds or the timeout passes. Every answer
// carries resumeAfter; pass it back as After so nothing between calls is missed.
func (c *Client) Wait(ctx context.Context, options WaitOptions) (map[string]any, error) {
	if len(options.After) > 256 {
		return nil, errors.New("after must be the resumeAfter cursor from launch, send or wait")
	}
	if options.Seconds == 0 {
		options.Seconds = 30
	}
	if options.Seconds < 1 || options.Seconds > MaxWaitSeconds {
		return nil, fmt.Errorf("timeoutSeconds must be between 1 and %d", MaxWaitSeconds)
	}
	until, err := waitCondition(options)
	if err != nil {
		return nil, err
	}
	result, err := c.request(ctx, http.MethodPost, c.execPath()+"/wait", map[string]any{"timeoutSeconds": options.Seconds, "until": until})
	var status *HTTPError
	if err != nil && errors.As(err, &status) && status.Status == http.StatusBadRequest {
		// Older servers predate hook events and fromLastInput. A wait is
		// read-only, so asking again with only the legacy fields is safe.
		legacy := map[string]any{"exit": true}
		for _, key := range []string{"programStates", "quietSeconds", "after"} {
			if value, ok := until[key]; ok {
				legacy[key] = value
			}
		}
		if legacy["programStates"] == nil && legacy["quietSeconds"] == nil {
			return nil, err
		}
		result, err = c.request(ctx, http.MethodPost, c.execPath()+"/wait", map[string]any{"timeoutSeconds": options.Seconds, "until": legacy})
	}
	return result, sessionError(err)
}

// Cursor returns a resumeAfter for "now" by asking for a one-second wait.
// A later wait passed this cursor sees every hook and status after it.
func (c *Client) Cursor(ctx context.Context) (string, error) {
	result, err := c.request(ctx, http.MethodPost, c.execPath()+"/wait", map[string]any{"timeoutSeconds": 1, "until": map[string]any{"exit": true}})
	if err != nil {
		return "", sessionError(err)
	}
	cursor, _ := result["resumeAfter"].(string)
	return cursor, nil
}

// ErrSessionReplaced reports that the pinned exec no longer exists. The
// adapter never retargets input on its own; reopening the box adopts the
// box's current primary session.
var ErrSessionReplaced = errors.New("this box's terminal session ended or was replaced; call open_box to adopt its current session (no input was sent to the new session)")

func sessionError(err error) error {
	var status *HTTPError
	if err != nil && errors.As(err, &status) && (status.Status == http.StatusNotFound || status.Status == http.StatusGone) {
		return fmt.Errorf("%w: %v", ErrSessionReplaced, err)
	}
	return err
}
