package discobox

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Only project the public health fields. The upstream pool record also carries
// provider configuration, host credentials and open-ended agent diagnostics.
func (c *Client) BackendHealth(ctx context.Context) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, err := c.requestLimited(ctx, http.MethodGet, "/projects/"+c.Session.Project+"/pools", nil, listingResponseLimit)
	if err != nil {
		var status *HTTPError
		if errors.As(err, &status) {
			if status.Status == http.StatusForbidden {
				return nil, errors.New("pool health unavailable: this connection lacks permission to list project pools (HTTP 403)")
			}
			return nil, status
		}
		return nil, errors.New("pool health unavailable: could not read the backend pool report")
	}
	items, ok := raw["pools"].([]any)
	if !ok {
		return nil, errors.New("Discobox did not return a pool list")
	}
	pools := make([]map[string]any, 0, len(items))
	for _, item := range items {
		p, ok := item.(map[string]any)
		if !ok {
			return nil, errors.New("invalid Discobox pool record")
		}
		view := map[string]any{}
		for _, field := range []string{"id", "name", "health", "resourcesReportedAt"} {
			value, _ := p[field].(string)
			if value != "" {
				view[field] = value
			}
		}
		for _, field := range []string{"ready", "schedulable", "degraded"} {
			if value, ok := p[field].(bool); ok {
				view[field] = value
			}
		}
		view["placement"] = healthMetrics(p, "availableCpuVcpus", "availableMemoryBytes", "availableStorageBytes")
		resources, _ := p["resources"].(map[string]any)
		if stamp, ok := resources["reportedAt"].(string); ok {
			view["sampledAt"] = stamp
		}
		view["totalUsage"] = healthMetrics(resources["total"], "cpuVcpus", "capacityVcpus", "memoryBytes", "capacityBytes", "sandboxCount")
		view["poolCpu"] = healthMetrics(resources["cpu"], "vcpus", "capacityVcpus", "windowSeconds")
		view["poolMemory"] = healthMetrics(resources["memory"], "currentBytes", "availableBytes", "capacityBytes", "limitBytes")
		storage, _ := resources["storage"].(map[string]any)
		disk := healthMetrics(storage["filesystem"], "totalBytes", "freeBytes", "usedBytes")
		if free, ok := disk["freeBytes"]; ok {
			disk["freeGiB"] = free / (1 << 30)
		}
		if used, ok := disk["usedBytes"]; ok && disk["totalBytes"] > 0 {
			disk["usedPercent"] = 100 * used / disk["totalBytes"]
		}
		view["disk"] = disk
		pools = append(pools, view)
	}
	return map[string]any{"project": c.Session.Project, "checkedAt": time.Now().UTC(), "pools": pools}, nil
}

func healthMetrics(value any, fields ...string) map[string]float64 {
	src, _ := value.(map[string]any)
	dst := map[string]float64{}
	for _, field := range fields {
		if n, ok := src[field].(float64); ok && n >= 0 {
			dst[field] = n
		}
	}
	return dst
}
