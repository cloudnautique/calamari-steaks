package discobox

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/discobox-ai/discobox/pkg/endpoint"
)

// Connection is one connection generation: a catalog client and, for a
// remote peer, the Iroh endpoint it owns. Unlike ConfigurePeer, any number
// can exist in one process, so a saved profile applies without a restart.
type Connection struct {
	Client *Client
	iroh   *endpoint.IrohEndpoint
}

// Connect builds a connection without dialing (an Iroh endpoint binds its
// socket on first use). token is the upstream API credential, if any.
func Connect(raw, token, project, identityPath string, relays []string) (*Connection, error) {
	if !strings.HasPrefix(raw, "discobox://") {
		client, err := NewCatalog(raw, token, project)
		if err != nil {
			return nil, err
		}
		return &Connection{Client: client}, nil
	}
	parsed, err := peerEndpoint(raw)
	if err != nil {
		return nil, err
	}
	if !identifier.MatchString(project) {
		return nil, errors.New("invalid project ID")
	}
	issuer, _, err := PeerIdentity(identityPath)
	if err != nil {
		return nil, err
	}
	// A fresh dialing key each generation, certified by the enrolled
	// identity, as ConfigurePeer does for the process-wide transport.
	_, dialing, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	iroh, err := endpoint.NewIrohEndpoint(endpoint.IrohConfig{SecretKey: dialing, Issuer: issuer, RelayURLs: relays})
	if err != nil {
		return nil, err
	}
	peer, err := parsed.IrohID()
	if err != nil {
		return nil, err
	}
	transport, err := iroh.RoundTripper(peer, http.DefaultTransport.(*http.Transport).Clone(), parsed.IrohAddrs...)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Transport: transport, Timeout: 75 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &Connection{Client: &Client{endpoint: strings.TrimRight(raw, "/"), base: endpoint.LogicalHTTPBaseURL, http: client, token: token, Session: Session{Project: project}}, iroh: iroh}, nil
}

// Step is one layer of a connection diagnosis, in the person's terms.
type Step struct {
	Layer   string `json:"layer"`
	Status  string `json:"status"`
	Summary string `json:"summary,omitempty"`
	Hint    string `json:"hint,omitempty"`
}

// Diagnose walks the layers to the server (address, native library, relay,
// peer connection, admission, server health) using this connection's own
// transport. It does not check project access; read the catalog for that.
func (c *Connection) Diagnose(ctx context.Context) ([]Step, string) {
	opts := endpoint.DiagnoseOptions{RelayTimeout: 4 * time.Second, ConnectTimeout: 12 * time.Second, RequestTimeout: 8 * time.Second}
	var diagnosis endpoint.Diagnosis
	if c.iroh != nil {
		diagnosis = c.iroh.Diagnose(ctx, c.Client.endpoint, opts)
	} else {
		diagnosis = endpoint.Diagnose(ctx, c.Client.endpoint, opts)
	}
	return Steps(diagnosis), diagnosis.ServerVersion
}

// Steps converts an upstream diagnosis, dropping raw error text and detail
// lines that may name local paths or addresses.
func Steps(diagnosis endpoint.Diagnosis) []Step {
	steps := make([]Step, 0, len(diagnosis.Steps))
	for _, step := range diagnosis.Steps {
		steps = append(steps, Step{Layer: step.Layer, Status: string(step.Status), Summary: truncate(step.Summary, 300), Hint: truncate(step.Hint, 400)})
	}
	return steps
}

func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return strings.ToValidUTF8(text[:limit], "") + "…"
}
