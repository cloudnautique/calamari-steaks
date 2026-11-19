package bridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/cloudnautique/discobox-mcp-ui/internal/connection"
	"github.com/cloudnautique/discobox-mcp-ui/web"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const setupResourceURI = "ui://discobox/connection-v3.html"

func setupResult(value any) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Connection setup updated. No backend authorization or box creation was performed."}}, Meta: mcp.Meta{"onboarding": value}}
}

const setupTokenTTL = 30 * time.Minute
const setupTokenLimit = 32

// setupSave is connection_save's input: the draft profile plus a single-use
// save token that the widget received only in private _meta.
type setupSave struct {
	Endpoint  string   `json:"endpoint"`
	Project   string   `json:"project"`
	Relays    []string `json:"relays,omitempty"`
	SaveToken string   `json:"saveToken" jsonschema:"Private single-use token from the setup widget's _meta"`
}

// withSetupToken attaches a fresh single-use save token in _meta. Like the
// approval review token, it never appears in content or structuredContent, so
// a model on a host that exposed connection_save still could not retarget the
// adapter's saved profile without the widget's private metadata.
func (s *Server) withSetupToken(result *mcp.CallToolResult) (*mcp.CallToolResult, error) {
	var key [24]byte
	if _, err := rand.Read(key[:]); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(key[:])
	now := time.Now()
	s.setupMu.Lock()
	if s.setupTokens == nil {
		s.setupTokens = map[string]time.Time{}
	}
	for id, expires := range s.setupTokens {
		if now.After(expires) {
			delete(s.setupTokens, id)
		}
	}
	for id := range s.setupTokens {
		if len(s.setupTokens) < setupTokenLimit {
			break
		}
		delete(s.setupTokens, id)
	}
	s.setupTokens[token] = now.Add(setupTokenTTL)
	s.setupMu.Unlock()
	if result == nil {
		result = &mcp.CallToolResult{}
	}
	if result.Meta == nil {
		result.Meta = mcp.Meta{}
	}
	result.Meta["setup"] = map[string]any{"saveToken": token}
	return result, nil
}
func (s *Server) consumeSetupToken(token string) bool {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	expires, found := s.setupTokens[token]
	delete(s.setupTokens, token)
	return found && token != "" && time.Now().Before(expires)
}
func (s *Server) setupController() (*connection.Controller, error) {
	if s.Onboarding == nil {
		return nil, errors.New("connection setup is not configured by this launcher")
	}
	return s.Onboarding, nil
}
func (s *Server) onboardingTools(server *mcp.Server) {
	ui := mcp.Meta{"ui": map[string]any{"resourceUri": setupResourceURI}}
	mcp.AddTool(server, &mcp.Tool{Name: "open_connection_setup", Description: "SHOW UI: connect to Discobox using the local socket or a pinned remote Iroh server. Always available, including offline. Shows peer enrollment instructions; cannot authorize itself or create boxes.", Meta: ui}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
		c, e := s.setupController()
		if e != nil {
			return nil, nil, e
		}
		status := c.Status()
		result := setupResult(status)
		result.Content = []mcp.Content{&mcp.TextContent{Text: status.Message + " Local setup uses the shared Discobox resolver: unix:// on macOS/Linux or npipe:// on Windows. For a remote server, enter its discobox:// peer address, call get_connection_peer_id, and have an already-authorized operator run the returned enrollment command on the intended server. The widget cannot approve itself. Saving in the widget applies the connection at once where the adapter allows it; otherwise restart the adapter. Text-only clients can set --discobox and --project launcher flags (or the extension overrides), then restart and call test_connection. Do not paste private keys or credentials into chat."}}
		result, e = s.withSetupToken(result)
		if e != nil {
			return nil, nil, e
		}
		return result, status, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "get_backend_health", Description: "Read project pool health, disk free GiB and percent used, CPU, RAM and placement capacity. Compare across configured servers before choosing a workload destination. Missing metrics are unknown. Check sample timestamps.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
		report, err := s.backendHealth(ctx)
		return nil, report, err
	})
	mcp.AddTool(server, &mcp.Tool{Name: "connection_status", Description: "Show the Discobox connection settings, the last test (with its layer-by-layer diagnosis), when backend traffic last succeeded or failed, and whether a restart is needed. Does not contact the backend; use test_connection for a fresh check.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
		c, e := s.setupController()
		if e != nil {
			return nil, nil, e
		}
		result, e := s.withSetupToken(nil)
		if e != nil {
			return nil, connection.Status{}, e
		}
		return result, c.Status(), nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "test_connection", Description: "Test the configured Discobox connection layer by layer (address, identity, relay, peer, admission, server) and read the project's catalog. A timeout does not prove enrollment was denied. No creation, input, enrollment or automatic retry.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
		c, e := s.setupController()
		if e != nil {
			return nil, nil, e
		}
		r, e := c.Test(ctx, c.Current)
		return nil, r, e
	})
	mcp.AddTool(server, &mcp.Tool{Name: "get_connection_peer_id", Description: "Load or initialize this adapter's persistent identity and return only its public peer ID and copyable operator enrollment command. No server connection or enrollment is performed. Never request private key contents."}, func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
		c, e := s.setupController()
		if e != nil {
			return nil, nil, e
		}
		r, e := c.Identity()
		return nil, r, e
	})
	private := mcp.Meta{"ui": map[string]any{"visibility": []string{"app"}}}
	mcp.AddTool(server, &mcp.Tool{Name: "connection_test", Description: "App-only: test the person's draft connection settings. Bounded read only; no automatic enrollment.", Meta: private}, func(ctx context.Context, _ *mcp.CallToolRequest, in connection.Config) (*mcp.CallToolResult, any, error) {
		c, e := s.setupController()
		if e != nil {
			return nil, nil, e
		}
		r, e := c.Test(ctx, in)
		if e != nil {
			return nil, nil, e
		}
		result, e := s.withSetupToken(setupResult(r))
		return result, nil, e
	})
	mcp.AddTool(server, &mcp.Tool{Name: "connection_save", Description: "App-only: save the person's explicit local/remote profile without credentials. Applies it to new requests when possible; never retargets open views.", Meta: private}, func(ctx context.Context, _ *mcp.CallToolRequest, in setupSave) (*mcp.CallToolResult, any, error) {
		c, e := s.setupController()
		if e != nil {
			return nil, nil, e
		}
		if !s.consumeSetupToken(in.SaveToken) {
			return nil, nil, errors.New("connection setup save token is missing, expired or already used; reopen the setup widget and save again")
		}
		r, e := c.SaveContext(ctx, connection.Config{Endpoint: in.Endpoint, Project: in.Project, Relays: in.Relays})
		if e != nil {
			return nil, nil, e
		}
		result, e := s.withSetupToken(setupResult(r))
		return result, nil, e
	})
	server.AddResource(&mcp.Resource{URI: setupResourceURI, Name: "Discobox connection setup", MIMEType: "text/html;profile=mcp-app"}, setupResource)
	// Invalid endpoint/key/config must not make the MCP server disappear. Other
	// tools/resources return actionable errors instead of dereferencing nil.
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if s.backend() == nil {
				if method == "tools/call" {
					name := ""
					switch p := req.GetParams().(type) {
					case *mcp.CallToolParamsRaw:
						name = p.Name
					case *mcp.CallToolParams:
						name = p.Name
					}
					if name != "open_connection_setup" && name != "test_connection" && name != "get_connection_peer_id" && !strings.HasPrefix(name, "connection_") {
						return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "Connection setup is required. Call open_connection_setup (or connection_status in text-only clients) and save working settings."}}}, nil
					}
				}
				if method == "resources/read" {
					if p, ok := req.GetParams().(*mcp.ReadResourceParams); ok && !strings.HasPrefix(p.URI, "ui://") {
						return nil, errors.New("connection setup is required; call open_connection_setup")
					}
				}
			}
			return next(ctx, method, req)
		}
	})
}
func (s *Server) backendHealth(ctx context.Context) (map[string]any, error) {
	client := s.backend()
	if client == nil {
		return nil, errors.New("set up a Discobox connection to read pool health")
	}
	return client.BackendHealth(ctx)
}

func (s *Server) setupHTTP(mux *http.ServeMux) {
	mux.HandleFunc("GET /setup", func(w http.ResponseWriter, r *http.Request) {
		html, e := web.SetupHTML()
		if e != nil {
			http.Error(w, "Setup assets unavailable", 503)
			return
		}
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src "+web.ScriptHashes(html)+"; style-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(html))
	})
	mux.Handle("POST /api/connection/{action}", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := s.setupController()
		if e != nil {
			http.Error(w, e.Error(), 503)
			return
		}
		var in connection.Config
		var value any
		switch r.PathValue("action") {
		case "health":
			value, e = s.backendHealth(r.Context())
		case "status":
			value = c.Status()
		case "identity":
			value, e = c.Identity()
		case "test", "save":
			d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
			d.DisallowUnknownFields()
			if e = d.Decode(&in); e == nil {
				if r.PathValue("action") == "test" {
					value, e = c.Test(r.Context(), in)
				} else {
					value, e = c.Save(in)
				}
			}
		default:
			http.NotFound(w, r)
			return
		}
		if e != nil {
			http.Error(w, e.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(value)
	})))
}

var legacySetupResources = map[string]bool{"connection-v2.html": true, "connection-v1.html": true}

func setupResource(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	html, e := web.SetupHTML()
	if e != nil {
		return nil, e
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "text/html;profile=mcp-app", Text: html, Meta: mcp.Meta{"ui": map[string]any{"prefersBorder": true, "csp": map[string]any{"connectDomains": []string{}, "resourceDomains": []string{}}}}}}}, nil
}
