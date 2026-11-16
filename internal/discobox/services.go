package discobox

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const serviceDialTimeout = 12 * time.Second

type Service struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"`
	Ports       []int  `json:"ports"`
	Problem     string `json:"problem,omitempty"`
}
type ServicePort struct {
	Port      int    `json:"port"`
	Protocol  string `json:"protocol"`
	Host      string `json:"host"`
	ServiceID string `json:"serviceId,omitempty"`
	Name      string `json:"name,omitempty"`
	Reported  bool   `json:"reported"`
}
type ServiceCatalog struct {
	BoxID               string        `json:"boxId"`
	Services            []Service     `json:"services"`
	Ports               []ServicePort `json:"ports"`
	ManagementAvailable bool          `json:"managementAvailable"`
	Message             string        `json:"message,omitempty"`
}

var serviceID = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,180}$`)

func validServiceID(id string) bool { return serviceID.MatchString(id) && id != "." && id != ".." }
func servicePath(project, box, id string) string {
	p := "/api/projects/" + project + "/sandboxes/" + box + "/services"
	if id != "" {
		p += "/" + url.PathEscape(id)
	}
	return p
}
func portNumber(raw any) int {
	v, ok := raw.(float64)
	if !ok || v < 1 || v > 65535 || v != float64(int(v)) {
		return 0
	}
	return int(v)
}
func portHost(addresses []any) (string, error) {
	for _, raw := range addresses {
		s, _ := raw.(string)
		if s == "0.0.0.0" || s == "::" || s == "[::]" || s == "*" || s == "127.0.0.1" || s == "::1" || s == "[::1]" || s == "localhost" {
			return "localhost", nil
		}
	}
	for _, raw := range addresses {
		s, _ := raw.(string)
		if ip := net.ParseIP(strings.Trim(s, "[]")); ip != nil {
			return ip.String(), nil
		}
	}
	if len(addresses) > 0 {
		return "", errors.New("reported port has no valid local bind address")
	}
	return "localhost", nil
}

// ServiceCatalog combines declared services with agent-reported ports. It never
// accepts a caller-supplied target host or creates a service declaration.
func (c *Client) Services(ctx context.Context, id, parent string) (ServiceCatalog, error) {
	out := ServiceCatalog{BoxID: id, Services: []Service{}, Ports: []ServicePort{}}
	box, e := c.managerBox(ctx, id, parent)
	if e != nil {
		return out, e
	}
	if e = managerLifecycleError(box); e != nil {
		return out, e
	}
	runtime, _ := box["runtime"].(map[string]any)
	status, _ := runtime["agentStatus"].(map[string]any)
	ports, _ := status["ports"].([]any)
	for _, raw := range ports {
		p, _ := raw.(map[string]any)
		n := portNumber(p["port"])
		if n == 0 {
			continue
		}
		addresses, _ := p["addresses"].([]any)
		host, e := portHost(addresses)
		if e != nil {
			continue
		}
		proto, _ := p["protocol"].(string)
		sid, _ := p["serviceId"].(string)
		name, _ := p["serviceName"].(string)
		out.Ports = append(out.Ports, ServicePort{Port: n, Protocol: proto, Host: host, ServiceID: sid, Name: name, Reported: true})
	}
	data, e := c.request(ctx, http.MethodGet, servicePath(c.Session.Project, id, ""), nil)
	if e != nil {
		var h *HTTPError
		if errors.As(e, &h) && (h.Status == 403 || h.Status == 404 || h.Status == 501) {
			out.Message = fmt.Sprintf("Service management unavailable (HTTP %d); reported ports are shown separately.", h.Status)
			return out, nil
		}
		return out, e
	}
	items, ok := data["services"].([]any)
	if !ok && data["services"] != nil {
		return out, errors.New("invalid service catalog")
	}
	out.ManagementAvailable = true
	for _, raw := range items {
		v, _ := raw.(map[string]any)
		sid, _ := v["id"].(string)
		if !validServiceID(sid) {
			return out, errors.New("invalid service identity")
		}
		name, _ := v["name"].(string)
		state, _ := v["status"].(string)
		description, _ := v["description"].(string)
		problem, _ := v["problem"].(string)
		entry := Service{ID: sid, Name: name, Status: state, Description: description, Problem: problem, Ports: []int{}}
		numbers, _ := v["ports"].([]any)
		for _, raw := range numbers {
			n := portNumber(raw)
			if n == 0 {
				continue
			}
			entry.Ports = append(entry.Ports, n)
			present := false
			for _, p := range out.Ports {
				if p.Port == n {
					present = true
					break
				}
			}
			if !present {
				out.Ports = append(out.Ports, ServicePort{Port: n, Host: "localhost", ServiceID: sid, Name: name})
			}
		}
		out.Services = append(out.Services, entry)
	}
	return out, nil
}
func (c *Client) ControlService(ctx context.Context, id, parent, sid, action string) (map[string]any, error) {
	if !validServiceID(sid) || (action != "start" && action != "stop" && action != "restart") {
		return nil, errors.New("choose a declared service and start, stop, or restart")
	}
	if e := c.BoxAccess(ctx, id, parent); e != nil {
		return nil, e
	}
	return c.request(ctx, http.MethodPost, servicePath(c.Session.Project, id, sid)+"/"+action, nil)
}
func (c *Client) ServiceLogs(ctx context.Context, id, parent, sid string) (map[string]any, error) {
	if !validServiceID(sid) {
		return nil, errors.New("invalid service ID")
	}
	if e := c.BoxAccess(ctx, id, parent); e != nil {
		return nil, e
	}
	data, e := c.request(ctx, http.MethodGet, servicePath(c.Session.Project, id, sid)+"/logs", nil)
	if e != nil {
		return nil, e
	}
	entries, _ := data["entries"].([]any)
	truncated := len(entries) > 100
	if len(entries) > 100 {
		entries = entries[len(entries)-100:]
	}
	text := ""
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		encoded, _ := entry["data"].(string)
		chunk, e := base64.StdEncoding.DecodeString(encoded)
		if e != nil {
			return nil, errors.New("invalid service log encoding")
		}
		text += string(chunk)
		if len(text) > 65536 {
			text = text[len(text)-65536:]
			truncated = true
		}
	}
	return map[string]any{"boxId": id, "serviceId": sid, "text": strings.ToValidUTF8(text, "�"), "truncated": truncated, "untrusted": true}, nil
}
func (c *Client) DialServicePort(ctx context.Context, id, parent string, port int) (net.Conn, ServicePort, error) {
	check, cancel := context.WithTimeout(ctx, serviceDialTimeout)
	catalog, e := c.Services(check, id, parent)
	cancel()
	if e != nil {
		return nil, ServicePort{}, e
	}
	var match *ServicePort
	for _, candidate := range catalog.Ports {
		if candidate.Port != port || candidate.Protocol == "udp" || !candidate.Reported {
			continue
		}
		if match != nil && (match.Host != candidate.Host || match.Protocol != candidate.Protocol) {
			return nil, ServicePort{}, errors.New("ambiguous reported port; choose a single TCP listener")
		}
		copy := candidate
		match = &copy
	}
	if match == nil {
		return nil, ServicePort{}, errors.New("port is not currently reported or declared as TCP in this box")
	}
	conn, e := c.dialTCP(ctx, id, match.Host, port)
	return conn, *match, e
}
