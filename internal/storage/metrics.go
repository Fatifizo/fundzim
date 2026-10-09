package storage

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics are the storage pipeline metric families. Labels are bounded (bucket class, outcome, codes).
type Metrics struct {
	uploads      *prometheus.CounterVec   // bucket_class, outcome (accepted | rejected:<reason> | failed)
	scans        *prometheus.CounterVec   // bucket_class, outcome (CLEAN | REJECTED | FAILED_SCAN:<code>)
	scanDuration *prometheus.HistogramVec // outcome
	uploadBytes  prometheus.Histogram
	expired      prometheus.Counter
	stuck        prometheus.Counter
	exhausted    prometheus.Gauge
}

// NewMetrics registers the storage metric families on reg (nil: unregistered, for tests).
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		uploads: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "fundzim_storage_uploads_total",
			Help: "Document uploads by bucket class and outcome."}, []string{"bucket_class", "outcome"}),
		scans: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "fundzim_storage_scans_total",
			Help: "Malware scan attempts by bucket class and outcome."}, []string{"bucket_class", "outcome"}),
		scanDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "fundzim_storage_scan_duration_seconds",
			Help: "Duration of one scan attempt (download + scan + promotion).", Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120}},
			[]string{"outcome"}),
		uploadBytes: prometheus.NewHistogram(prometheus.HistogramOpts{Name: "fundzim_storage_upload_bytes",
			Help: "Size of accepted uploads.", Buckets: prometheus.ExponentialBuckets(1024, 4, 9)}),
		expired: prometheus.NewCounter(prometheus.CounterOpts{Name: "fundzim_storage_uploads_expired_total",
			Help: "Interrupted uploads cleaned up by the expiry job."}),
		stuck: prometheus.NewCounter(prometheus.CounterOpts{Name: "fundzim_storage_scans_interrupted_total",
			Help: "SCANNING objects whose lease expired (worker died mid-scan) and were moved to FAILED_SCAN."}),
		exhausted: prometheus.NewGauge(prometheus.GaugeOpts{Name: "fundzim_storage_scan_retries_exhausted",
			Help: "FAILED_SCAN objects that used all automatic retries (need operator attention)."}),
	}
	if reg != nil {
		reg.MustRegister(m.uploads, m.scans, m.scanDuration, m.uploadBytes, m.expired, m.stuck, m.exhausted)
	}
	return m
}

// Scans returns the scan counter for (bucket class, outcome) — tests.
func (m *Metrics) Scans(bucket, outcome string) prometheus.Counter {
	return m.scans.WithLabelValues(bucket, outcome)
}
