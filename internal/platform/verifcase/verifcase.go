// Package verifcase is the shared verification-case state machine (ADR-034 §1) used by the kyc (KYC and KYB)
// and beneficiaries modules. The database guards the same edges (machine `verification_case` in
// app.status_transitions, migration 20261009150200); a test keeps the two lists identical.
package verifcase

// Statuses.
const (
	Draft                  = "DRAFT"
	Submitted              = "SUBMITTED"
	UnderReview            = "UNDER_REVIEW"
	AdditionalInfoRequired = "ADDITIONAL_INFORMATION_REQUIRED"
	Escalated              = "ESCALATED"
	Approved               = "APPROVED"
	Rejected               = "REJECTED"
	Expired                = "EXPIRED"
	Suspended              = "SUSPENDED"
	Revoked                = "REVOKED"
	Withdrawn              = "WITHDRAWN"
	NotStarted             = "NOT_STARTED" // API only: no case exists
	AwaitingSecondApproval = "AWAITING_SECOND_APPROVAL"
)

// Edges is the transition list (from → allowed targets). "" is the initial state.
var Edges = map[string][]string{
	"":                     {Draft},
	Draft:                  {Submitted, Withdrawn},
	Submitted:              {UnderReview, Withdrawn},
	UnderReview:            {AdditionalInfoRequired, Escalated, Approved, Rejected},
	AdditionalInfoRequired: {Submitted, Withdrawn, Expired},
	Escalated:              {UnderReview, Approved, Rejected},
	Approved:               {Expired, Suspended, Revoked},
	Suspended:              {Approved, UnderReview, Revoked},
}

// Can reports whether from → to is allowed.
func Can(from, to string) bool {
	for _, t := range Edges[from] {
		if t == to {
			return true
		}
	}
	return false
}

// Open reports whether the case is not final (it blocks starting another case for the same subject).
func Open(s string) bool {
	switch s {
	case Draft, Submitted, UnderReview, AdditionalInfoRequired, Escalated:
		return true
	}
	return false
}

// Editable reports whether the subject may change details and documents.
func Editable(s string) bool { return s == Draft || s == AdditionalInfoRequired }

// Final reports whether no further transition exists.
func Final(s string) bool { _, ok := Edges[s]; return !ok && s != "" }

// Action is a reviewer or subject action of the shared workflow (brief §16).
type Action string

const (
	ActAssign        Action = "ASSIGN_CASE"
	ActStartReview   Action = "START_REVIEW"
	ActRequestInfo   Action = "REQUEST_INFORMATION"
	ActApprove       Action = "APPROVE"
	ActSecondApprove Action = "SECOND_APPROVAL"
	ActReject        Action = "REJECT"
	ActEscalate      Action = "ESCALATE"
	ActReturn        Action = "RETURN_FROM_ESCALATION"
	ActSuspend       Action = "SUSPEND"
	ActReinstate     Action = "REINSTATE"
	ActReopen        Action = "REOPEN"
	ActRevoke        Action = "REVOKE"
)

// Target returns the status an action leads to from status s, or "" when the action is not allowed there.
// Assign and second approval do not change status by themselves.
func Target(a Action, s string) string {
	var to string
	switch a {
	case ActStartReview:
		to = UnderReview
	case ActRequestInfo:
		to = AdditionalInfoRequired
	case ActApprove, ActSecondApprove:
		to = Approved
	case ActReject:
		to = Rejected
	case ActEscalate:
		to = Escalated
	case ActReturn:
		if s != Escalated {
			return ""
		}
		to = UnderReview
	case ActSuspend:
		to = Suspended
	case ActReinstate:
		if s != Suspended {
			return ""
		}
		to = Approved
	case ActReopen:
		if s != Suspended {
			return ""
		}
		to = UnderReview
	case ActRevoke:
		to = Revoked
	default:
		return ""
	}
	if !Can(s, to) {
		return ""
	}
	return to
}

// Decisions in which the reviewer must be different from the subject and, where four-eyes applies, a second
// person confirms are listed by the module; reason codes are UPPER_SNAKE.
