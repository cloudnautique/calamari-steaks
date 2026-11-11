// Package web embeds the terminal app and, after npm run build, its dependencies.
package web

import (
	"embed"
	"fmt"
	"strings"
)

// The wildcard includes vendor when present, while allowing a tools-only build.
//
//go:embed *
var files embed.FS

func Asset(name string) ([]byte, error) { return files.ReadFile("vendor/" + name) }

func Ready() error {
	for _, name := range []string{"app.js", "tokens.css", "THIRD-PARTY-NOTICES.txt"} {
		if _, err := Asset(name); err != nil {
			return fmt.Errorf("missing %s: run npm install and npm run build, then rebuild the Go binary", name)
		}
	}
	return nil
}

func HTML() (string, error) {
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
