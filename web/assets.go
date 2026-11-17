// Package web embeds the terminal app and, after npm run build, its dependencies.
package web

import (
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
)

// The wildcard includes vendor when present, while allowing a tools-only build.
//
//go:embed *
var files embed.FS

func Asset(name string) ([]byte, error) { return files.ReadFile("vendor/" + name) }

func Ready() error {
	for _, name := range []string{"app.js", "onboarding.js", "tokens.css", "THIRD-PARTY-NOTICES.txt"} {
		if _, err := Asset(name); err != nil {
			return fmt.Errorf("missing %s: run npm install and npm run build, then rebuild the Go binary", name)
		}
	}
	return nil
}

// The pages are built from embedded, immutable files, so each is assembled
// once per process. A failed build (assets missing) is not cached.
var (
	pagesMu sync.Mutex
	pages   = map[string]string{}
)

func cached(name string, build func() (string, error)) (string, error) {
	pagesMu.Lock()
	defer pagesMu.Unlock()
	if page, ok := pages[name]; ok {
		return page, nil
	}
	page, err := build()
	if err == nil {
		pages[name] = page
	}
	return page, err
}

func HTML() (string, error) { return cached("workspace", buildHTML) }

// SetupHTML is a small independent app, available even with no backend client.
func SetupHTML() (string, error) { return cached("setup", buildSetupHTML) }

func buildHTML() (string, error) {
	if err := Ready(); err != nil {
		return "", err
	}
	html, err := files.ReadFile("index.html")
	if err != nil {
		return "", err
	}
	css, err := files.ReadFile("app.css")
	if err != nil {
		return "", err
	}
	js, err := Asset("app.js")
	if err != nil {
		return "", err
	}
	tokens, err := Asset("tokens.css")
	if err != nil {
		return "", err
	}
	// Inline the bundled SDK, renderer and WASM. The MCP resource must not
	// require the host's sandboxed iframe to fetch a localhost HTTP asset.
	script := strings.ReplaceAll(string(js), "</script", "<\\/script")
	return strings.NewReplacer("<!--STYLE-->", "<style>"+string(tokens)+string(css)+"</style>", "<!--SCRIPT-->", "<script type=\"module\">"+script+"</script>").Replace(string(html)), nil
}

func buildSetupHTML() (string, error) {
	html, err := files.ReadFile("onboarding.html")
	if err != nil {
		return "", err
	}
	css, err := files.ReadFile("onboarding.css")
	if err != nil {
		return "", err
	}
	js, err := Asset("onboarding.js")
	if err != nil {
		return "", fmt.Errorf("build connection setup assets: %w", err)
	}
	script := strings.ReplaceAll(string(js), "</script", "<\\/script")
	return strings.NewReplacer("<!--STYLE-->", "<style>"+string(css)+"</style>", "<!--SCRIPT-->", "<script type=\"module\">"+script+"</script>").Replace(string(html)), nil
}

// ScriptHashes returns CSP source expressions ('sha256-…') for every inline
// <script type="module"> in a page built by HTML or SetupHTML, so the HTTP
// pages can allow exactly their own bundle instead of 'unsafe-inline'.
func ScriptHashes(html string) string {
	const open, end = "<script type=\"module\">", "</script>"
	var hashes []string
	for {
		start := strings.Index(html, open)
		if start < 0 {
			break
		}
		html = html[start+len(open):]
		stop := strings.Index(html, end)
		if stop < 0 {
			break
		}
		sum := sha256.Sum256([]byte(html[:stop]))
		hashes = append(hashes, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
		html = html[stop+len(end):]
	}
	return strings.Join(hashes, " ")
}
