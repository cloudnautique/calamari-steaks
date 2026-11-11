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
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
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
	base    string
	http    *http.Client
	token   string
	Session Session
}

func New(endpoint, token string, session Session) (*Client, error) {
	if err := session.Validate(); err != nil {
		return nil, err
	}
	return newClient(endpoint, token, session)
}

// newClient also supports the unbound client used during manager creation.
func newClient(endpoint, token string, session Session) (*Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse Discobox endpoint: %w", err)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("endpoint must not contain credentials, a query or a fragment")
	}
	switch u.Scheme {
	case "unix":
		if u.Host != "" || u.Path == "" {
			return nil, errors.New("use unix:///absolute/path/to/server.sock")
		}
		transport.Proxy = nil
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", u.Path)
		}
		endpoint = "http://discobox.local"
	case "http", "https":
		if u.Host == "" || (u.Path != "" && u.Path != "/") {
			return nil, errors.New("HTTP endpoint must be an origin without a path")
		}
	default:
		return nil, errors.New("endpoint must use http, https or unix")
	}
	return &Client{base: strings.TrimRight(endpoint, "/"), token: token, Session: session, http: &http.Client{
		Transport: transport, Timeout: 75 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
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

func (c *Client) request(ctx context.Context, method, path string, body any) (map[string]any, error) {
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
	const limit = 2 << 20
	data, err = io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, errors.New("Discobox response exceeded 2 MiB; request less output")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Do not forward arbitrary upstream error bodies or credential-bearing URLs.
		return nil, &HTTPError{Status: response.StatusCode, Path: path}
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

var errNoPrimary = errors.New("no primary exec found; start the manager using Discobox, then retry")

// ResolvePrimary pins the current primary exec once. Replacements require reconnecting
// the integration rather than silently directing subsequent input to a new process.
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
				return errors.New("multiple primary execs reported; specify --exec")
			}
			found, _ = entry["id"].(string)
		}
	}
	if found == "" {
		return errNoPrimary
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
	return c.request(ctx, http.MethodGet, fmt.Sprintf("%s/screen?scrollback=%d", c.execPath(), scrollback), nil)
}

func (c *Client) Send(ctx context.Context, text string, submit bool) (map[string]any, error) {
	if strings.TrimSpace(text) == "" || len(text) > 64<<10 {
		return nil, errors.New("message must contain 1 to 65536 bytes of text")
	}
	parts := []map[string]string{{"text": text}}
	if submit {
		parts = append(parts, map[string]string{"key": "Enter"})
	}
	return c.request(ctx, http.MethodPost, c.execPath()+"/input", map[string]any{"input": parts})
}

func (c *Client) Interrupt(ctx context.Context) (map[string]any, error) {
	return c.request(ctx, http.MethodPost, c.execPath()+"/input", map[string]any{"input": []map[string]string{{"key": "C-c"}}})
}

func (c *Client) Wait(ctx context.Context, after string, seconds int) (map[string]any, error) {
	if after == "" || len(after) > 256 {
		return nil, errors.New("after must be the resumeAfter cursor from send or wait")
	}
	if seconds < 1 || seconds > 60 {
		return nil, errors.New("timeout must be between 1 and 60 seconds")
	}
	return c.request(ctx, http.MethodPost, c.execPath()+"/wait", map[string]any{
		"timeoutSeconds": seconds, "until": map[string]any{"after": after, "exit": true,
			"programStates": []map[string]string{{"state": "idle"}, {"state": "done"}, {"state": "blocked"}, {"state": "error"}}},
	})
}
