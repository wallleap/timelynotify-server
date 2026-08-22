// Package metrics exposes a small Prometheus registry for the server:
// standard Go/process collectors plus HTTP request counters and latency. A
// data-exporter registers request instrumentation middleware and a /metrics
// handler on the fiber router.
package metrics

import (
	"github.com/gofiber/adaptor/v2"
	"github.com/gofiber/fiber/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry bundles the collectors exposed on /metrics.
type Registry struct {
	// Registry is the underlying prometheus registry.
	r *prometheus.Registry
	// requests counts total HTTP requests by method and status code class.
	requests *prometheus.CounterVec
	// activeStreams is the number of open /stream WebSocket connections.
	activeStreams prometheus.Gauge
}

// New builds a Registry with default collectors, initially marked for the
// given registry. err is always nil under the standard constructor.
func New() *Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
		prometheus.NewGoCollector(),
	)

	requests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "timelynotify",
		Name:      "http_requests_total",
		Help:      "Total HTTP requests by method and status.",
	}, []string{"method", "status"})
	reg.MustRegister(requests)

	activeStreams := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "timelynotify",
		Name:      "active_streams",
		Help:      "Number of active gotify-compatible /stream WebSocket connections.",
	})
	reg.MustRegister(activeStreams)

	return &Registry{r: reg, requests: requests, activeStreams: activeStreams}
}

// Middleware returns a fiber middleware that counts requests by method and
// status (computed in After). It must be registered before the endpoints so
// the counters cover them.
func (reg *Registry) Middleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		err := c.Next()
		reg.requests.WithLabelValues(c.Method(), statusClass(c.Response().StatusCode())).Inc()
		return err
	}
}

// Handler returns a fiber handler serving the Prometheus exposition format.
func (reg *Registry) Handler() fiber.Handler {
	h := promhttp.HandlerFor(reg.r, promhttp.HandlerOpts{})
	return adaptor.HTTPHandler(h)
}

// SetActiveStreams records the current number of live /stream subscribers.
func (reg *Registry) SetActiveStreams(n float64) {
	reg.activeStreams.Set(n)
}

// statusClass buckets a status code into a coarse family.
func statusClass(code int) string {
	switch {
	case code >= 500:
		return "5xx"
	case code >= 400:
		return "4xx"
	case code >= 300:
		return "3xx"
	case code >= 200:
		return "2xx"
	default:
		return "other"
	}
}
