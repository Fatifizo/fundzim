// Package opsview holds the shared read-model types every domain module uses to contribute to the operations portal
// (Stage 14, ADR-046): metrics, pending approvals, open exceptions and search hits. A module builds its snapshot from
// its own tables only; the operations module aggregates snapshots and filters them by the viewer's permissions. These
// are read models: they never carry a decision and never change state.
package opsview

import "time"

// Data natures (brief §11): what a figure is.
const (
	Real         = "REAL"          // a count of records in this environment (operational, not financial performance)
	Simulated    = "SIMULATED"     // produced by a simulator (sandbox provider, screening simulator)
	TestOnly     = "TEST_ONLY"     // TEST money or synthetic fixtures — never business performance
	Unknown      = "UNKNOWN"       // no evidence either way
	NotAvailable = "NOT_AVAILABLE" // the source does not exist yet
	Blocked      = "BLOCKED"       // the capability is deliberately disabled
)

// Operational statuses (brief §12).
const (
	Healthy       = "HEALTHY"        // evidence of correct operation within its freshness window
	Degraded      = "DEGRADED"       // working with errors, backlog or stale evidence
	Unavailable   = "UNAVAILABLE"    // a check failed
	StatusBlocked = "BLOCKED"        // disabled on purpose (production gates); never reported as HEALTHY
	StatusUnknown = "UNKNOWN"        // no evidence
	NotConfigured = "NOT_CONFIGURED" // nothing configured to measure or run
)

// Metric is one counted figure with its documented source.
type Metric struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	Module     string `json:"module"`
	Value      *int64 `json:"value"` // nil: not measurable (Nature says why)
	Nature     string `json:"nature"`
	Status     string `json:"status,omitempty"`
	Source     string `json:"source"`     // where the number comes from (table/query, in words)
	Permission string `json:"permission"` // who may see it
	Href       string `json:"href,omitempty"`
	State      string `json:"state,omitempty"` // a named condition behind Status (e.g. a reconciliation sweep's health state)
}

// Approval is a pending maker-checker decision owned by a module.
type Approval struct {
	ID                 string    `json:"id"` // module:record
	SourceModule       string    `json:"source_module"`
	SourceRecordID     string    `json:"source_record_id"`
	Action             string    `json:"action"`
	Summary            string    `json:"summary"`
	RequestedBy        string    `json:"requested_by"`
	RequestedAt        time.Time `json:"requested_at"`
	RequiredPermission string    `json:"required_permission"`
	Status             string    `json:"status"`
	RiskLevel          string    `json:"risk_level,omitempty"`
	AssignedApprover   string    `json:"assigned_approver,omitempty"`
	// Makers are the people who may NOT decide (requester, submitter, proposer); linked accounts are checked by the module.
	Makers []string `json:"-"`
	Href   string   `json:"href"` // the owning module's page where the decision is taken
}

// Exception is an unresolved operational problem owned by a module.
type Exception struct {
	ID            string    `json:"id"` // module:record
	SourceModule  string    `json:"source_module"`
	SourceRef     string    `json:"source_ref"`
	Severity      string    `json:"severity"` // SEV1..SEV4
	Category      string    `json:"category"`
	Summary       string    `json:"summary"`
	CreatedAt     time.Time `json:"created_at"`
	DomainStatus  string    `json:"domain_status"`
	Permission    string    `json:"permission"`
	CorrelationID string    `json:"correlation_id,omitempty"`
	EvidenceRef   string    `json:"evidence_ref,omitempty"`
	Href          string    `json:"href"`
}

// Snapshot is a module's contribution.
type Snapshot struct {
	Module     string      `json:"module"`
	Metrics    []Metric    `json:"metrics"`
	Approvals  []Approval  `json:"approvals"`
	Exceptions []Exception `json:"exceptions"`
}

// SearchHit is one search result (identifiers and a short label only).
type SearchHit struct {
	Kind       string `json:"kind"`
	ID         string `json:"id"`
	Label      string `json:"label"`
	Status     string `json:"status,omitempty"`
	Permission string `json:"permission"`
	Href       string `json:"href"`
}

// Int64 returns a pointer (metric values).
func Int64(v int64) *int64 { return &v }

// Count builds a REAL count metric.
func Count(module, key, label string, v int64, perm, source, href string) Metric {
	return Metric{Key: key, Label: label, Module: module, Value: Int64(v), Nature: Real, Source: source, Permission: perm, Href: href}
}
