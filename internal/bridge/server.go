package bridge

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/cloudnautique/discobox-mcp-ui/internal/discobox"
	"github.com/cloudnautique/discobox-mcp-ui/web"
)

// Server owns a catalog on one configured endpoint/project. Connected manager
// sessions are pinned independently; callers cannot supply upstream URLs.
type Server struct {
	Client          *discobox.Client
	Origin          string
	token           string
	allowedOrigins  map[string]bool
	managersMu      sync.Mutex
	managers        map[string]*managerConnection
	managerOptions  discobox.ManagerOptions
	appMu           sync.Mutex
	appTerminals    map[string]*appTerminal
	desktopMu       sync.Mutex
	desktops        map[string]*desktopSession
	desktopOpening  int
	approvalMu      sync.Mutex
	approvalReviews map[string]approvalReview
}

func New(client *discobox.Client, origin string, extraOrigins []string, accessToken string, options discobox.ManagerOptions) (*Server, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.Host == "" {
		return nil, errors.New("invalid local UI origin")
	}
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return nil, err
	}
	if accessToken == "" {
		accessToken = hex.EncodeToString(key[:])
	}
	if decoded, err := hex.DecodeString(accessToken); err != nil || len(decoded) != 32 {
		return nil, errors.New("access token must contain 64 hexadecimal characters")
	}
	s := &Server{Client: client, Origin: origin, token: accessToken, allowedOrigins: map[string]bool{origin: true}, appTerminals: make(map[string]*appTerminal), managers: make(map[string]*managerConnection), managerOptions: options}
	s.desktops = make(map[string]*desktopSession)
	if client.Session.Box != "" {
		s.managers[client.Session.Box] = &managerConnection{Client: client}
	}
	for _, origin := range extraOrigins {
		if origin != "null" {
			u, err := url.Parse(origin)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
				return nil, errors.New("UI origins must be exact http(s) origins, or the literal null for an opaque MCP iframe")
			}
		}
		s.allowedOrigins[origin] = true
	}
	return s, nil
}

func (s *Server) BrowserURL() string { return s.Origin + "/#token=" + s.token }
func (s *Server) Token() string      { return s.token }
func (s *Server) validToken(token string) bool {
	return subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) == 1
}

func (s *Server) connection() map[string]any {
	return map[string]any{"origin": s.Origin, "token": s.token, "session": s.Client.Session}
}

// A single lock covers both ownership and writing. A control transfer cannot
// race a tool submission or slip between parts of a terminal input operation.
func (s *managerConnection) writeAs(owner string, write func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.archived {
		return errors.New("manager is archived; choose another manager")
	}
	if owner == "mcp" {
		if s.controller != "" {
			return errors.New("a person has input focus; wait for them to leave the composer or terminal before sending instructions")
		}
	} else if s.controller != owner {
		return errors.New("take terminal control before typing")
	}
	return write()
}

// Geometry does not require a keyboard claim. A passive view may fit while
// nobody is typing, but must not resize underneath another human controller.
func (s *managerConnection) resizeAs(view string, resize func() error) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.archived {
		return false, errors.New("manager is archived; choose another manager")
	}
	if s.controller != "" && s.controller != view {
		return false, nil
	}
	if err := resize(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *managerConnection) claim(owner string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.archived {
		return errors.New("manager is archived; choose another manager")
	}
	if s.controller != "" && s.controller != owner {
		return errors.New("another browser has terminal control")
	}
	s.controller = owner
	return nil
}

func (s *managerConnection) release(owner string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.controller == owner {
		s.controller = ""
	}
}

func (s *Server) Handler(mcpHandler http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self' 'unsafe-inline' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'; connect-src 'self' "+strings.Replace(s.Origin, "http", "ws", 1)+"; img-src 'self' data:; font-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'")
		html, err := web.HTML()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(html))
	})
	mux.HandleFunc("GET /assets/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		types := map[string]string{"ghostty-web.js": "text/javascript", "mcp-app.js": "text/javascript", "ghostty-vt.wasm": "application/wasm", "tokens.css": "text/css"}
		mime, ok := types[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		data, err := web.Asset(name)
		if err != nil {
			http.Error(w, "UI assets missing: run npm install and npm run build, then rebuild the Go server", http.StatusServiceUnavailable)
			return
		}
		// Assets are public code only. The iframe can load them without credentials.
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", mime)
		w.Write(data)
	})
	mux.Handle("GET /api/session", s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.connection())
	})))
	mux.Handle("POST /api/files/{action}", s.auth(http.HandlerFunc(s.filesHTTP)))
	mux.Handle("POST /api/managers/{action}", s.auth(http.HandlerFunc(s.workspaceHTTP)))
	mux.HandleFunc("GET /terminal", s.terminal)
	if mcpHandler != nil {
		mux.Handle("/mcp", s.auth(mcpHandler))
	}
	u, _ := url.Parse(s.Origin)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != u.Host {
			http.Error(w, "unexpected host", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && !s.allowedOrigins[origin] {
			http.Error(w, "origin denied", http.StatusForbidden)
			return
		}
		if !s.validToken(trimBearer(r.Header.Get("Authorization"))) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func trimBearer(header string) string {
	const prefix = "Bearer "
	if len(header) < len(prefix) || header[:len(prefix)] != prefix {
		return ""
	}
	return header[len(prefix):]
}
