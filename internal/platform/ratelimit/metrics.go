package ratelimit

import "github.com/prometheus/client_golang/prometheus"

// Metrics are the limiter's Prometheus counters. Labels are policy names (a fixed, small set) and the
// result; never keys.
type Metrics struct {
	decisions *prometheus.CounterVec
	degraded  *prometheus.CounterVec
}

// NewMetrics registers fundzim_ratelimit_decisions_total{policy,result} and
// fundzim_ratelimit_degraded_total{policy} on reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		decisions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "fundzim_ratelimit_decisions_total", Help: "Rate-limit decisions by policy and result (allowed|denied).",
		}, []string{"policy", "result"}),
		degraded: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "fundzim_ratelimit_degraded_total", Help: "Rate-limit decisions made by the in-process fallback because Valkey was unavailable.",
		}, []string{"policy"}),
	}
	reg.MustRegister(m.decisions, m.degraded)
	return m
}

func (m *Metrics) decision(policy string, allowed bool) {
	if m == nil {
		return
	}
	r := "allowed"
	if !allowed {
		r = "denied"
	}
	m.decisions.WithLabelValues(policy, r).Inc()
}

func (m *Metrics) degradedInc(policy string) {
	if m == nil {
		return
	}
	m.degraded.WithLabelValues(policy).Inc()
}
