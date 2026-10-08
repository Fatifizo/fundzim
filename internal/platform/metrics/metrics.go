// Package metrics exposes Prometheus metrics on the internal listener only (OBSERVABILITY.md).
// Labels are bounded: route templates, methods and status classes, never user or object IDs.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds FundZim's Stage 3 metric families.
type Metrics struct {
	Registry     *prometheus.Registry
	requests     *prometheus.CounterVec
	duration     *prometheus.HistogramVec
	panics       prometheus.Counter
	dependencyUp *prometheus.GaugeVec
}

// New registers the metric families plus Go runtime and process collectors.
func New(version, commit string) *Metrics {
	reg := prometheus.NewRegistry()
	m := &Metrics{
		Registry: reg,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "fundzim_http_requests_total", Help: "HTTP requests by method, route template and status code.",
		}, []string{"method", "route", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "fundzim_http_request_duration_seconds", Help: "HTTP request latency by method and route template.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}, []string{"method", "route"}),
		panics: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "fundzim_panics_recovered_total", Help: "Panics recovered by the HTTP middleware.",
		}),
		dependencyUp: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "fundzim_dependency_up", Help: "1 if the last readiness check of a dependency succeeded.",
		}, []string{"dependency"}),
	}
	build := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fundzim_build_info", Help: "Build metadata (value is always 1).",
	}, []string{"version", "commit"})
	build.WithLabelValues(version, commit).Set(1)
	reg.MustRegister(m.requests, m.duration, m.panics, m.dependencyUp, build,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
}

// ObserveRequest implements httpx.Observer.
func (m *Metrics) ObserveRequest(method, route string, status int, d time.Duration) {
	m.requests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
	m.duration.WithLabelValues(method, route).Observe(d.Seconds())
}

// PanicRecovered increments the panic counter.
func (m *Metrics) PanicRecovered() { m.panics.Inc() }

// SetDependency records a dependency check outcome.
func (m *Metrics) SetDependency(name string, up bool) {
	v := 0.0
	if up {
		v = 1
	}
	m.dependencyUp.WithLabelValues(name).Set(v)
}

// Handler serves the registry.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{})
}
