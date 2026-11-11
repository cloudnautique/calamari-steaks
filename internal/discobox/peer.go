package discobox

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"

	"github.com/discobox-ai/discobox/pkg/endpoint"
)

// peerEndpoint deliberately accepts only a pinned peer, without DNS routing.
func peerEndpoint(raw string) (endpoint.Endpoint, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return endpoint.Endpoint{}, err
	}
	if u.Scheme != "discobox" || u.User != nil || u.Fragment != "" || u.ForceQuery || u.Opaque != "" || (u.Path != "" && u.Path != "/") {
		return endpoint.Endpoint{}, errors.New("use discobox://<peer-id> with optional repeated ?addr=IP:port hints")
	}
	if _, err := endpoint.ParseIrohID(u.Host); err != nil {
		return endpoint.Endpoint{}, fmt.Errorf("Discobox endpoint must pin a peer ID: %w", err)
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return endpoint.Endpoint{}, err
	}
	for key, values := range query {
		if key != "addr" {
			return endpoint.Endpoint{}, errors.New("only addr query parameters are supported for a peer endpoint")
		}
		for _, value := range values {
			address, err := netip.ParseAddrPort(value)
			if err != nil || address.Port() == 0 {
				return endpoint.Endpoint{}, errors.New("peer addr hints must be IP:port with a nonzero port")
			}
		}
	}
	return endpoint.Parse(raw)
}

func DefaultPeerIdentityPath() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "discobox-mcp", "iroh", "id_ed25519"), nil
}

// PeerIdentity loads or initializes the adapter's enrollment key. A corrupt
// identity is an error; replacing it would invalidate the server enrollment.
// The file is PKCS#8 PEM, matching Discobox's identity format.
func PeerIdentity(path string) (ed25519.PrivateKey, endpoint.IrohID, error) {
	key, err := loadPeerIdentity(path)
	if errors.Is(err, os.ErrNotExist) {
		_, key, err = ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, endpoint.IrohID{}, err
		}
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return nil, endpoint.IrohID{}, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, endpoint.IrohID{}, err
		}
		// Publish a fully written private file without replacing another
		// process's key, including when both initialize simultaneously.
		file, err := os.CreateTemp(filepath.Dir(path), ".identity-*")
		if err != nil {
			return nil, endpoint.IrohID{}, err
		}
		defer os.Remove(file.Name())
		defer file.Close()
		if _, err := file.Write(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})); err != nil {
			return nil, endpoint.IrohID{}, err
		}
		if err := file.Sync(); err != nil {
			return nil, endpoint.IrohID{}, err
		}
		if err := file.Close(); err != nil {
			return nil, endpoint.IrohID{}, err
		}
		if err := os.Link(file.Name(), path); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, endpoint.IrohID{}, err
		}
		key, err = loadPeerIdentity(path)
	}
	if err != nil {
		return nil, endpoint.IrohID{}, fmt.Errorf("load adapter peer identity: %w", err)
	}
	id, err := endpoint.IrohIDFromPublicKey(key.Public().(ed25519.PublicKey))
	return key, id, err
}

func loadPeerIdentity(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" || len(rest) != 0 {
		return nil, errors.New("identity must contain one PKCS#8 private key PEM block")
	}
	value, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("invalid PKCS#8 peer identity")
	}
	key, ok := value.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("peer identity must be ed25519")
	}
	return key, nil
}

// ConfigurePeer installs one process-wide transport using the persistent key
// as issuer and an ephemeral dialing key, as current Discobox clients do.
// Call once at startup, before New or EnsureManager for a peer endpoint.
func ConfigurePeer(raw, identityPath string, relays []string) (endpoint.IrohID, error) {
	if _, err := peerEndpoint(raw); err != nil {
		return endpoint.IrohID{}, err
	}
	issuer, id, err := PeerIdentity(identityPath)
	if err != nil {
		return endpoint.IrohID{}, err
	}
	_, transport, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return endpoint.IrohID{}, err
	}
	return id, endpoint.ConfigureIroh(endpoint.IrohConfig{SecretKey: transport, Issuer: issuer, RelayURLs: relays})
}
