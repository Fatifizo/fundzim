package payments

import "github.com/Fatifizo/fundzim/internal/psp"

// Edge is one allowed payment_intent transition with its Stage 1 code (P1–P20, P18b). This list must equal the
// app.status_transitions rows for machine 'payment_intent' (TestPaymentEdgesMatchMigration diffs them).
type Edge struct{ From, To, Code string }

// PaymentEdges are the 42 edges of payment-state-machine §2.
var PaymentEdges = []Edge{
	{"", psp.StatusCreated, "P1"},
	{psp.StatusCreated, psp.StatusPending, "P2"},
	{psp.StatusCreated, psp.StatusUnknown, "P3"},
	{psp.StatusCreated, psp.StatusFailed, "P4"},
	{psp.StatusCreated, psp.StatusCancelled, "P5"},
	{psp.StatusPending, psp.StatusRequiresAction, "P6"},
	{psp.StatusRequiresAction, psp.StatusPending, "P7"},
	{psp.StatusPending, psp.StatusAuthorised, "P8"},
	{psp.StatusRequiresAction, psp.StatusAuthorised, "P8"},
	{psp.StatusUnknown, psp.StatusAuthorised, "P8"},
	{psp.StatusPending, psp.StatusSucceeded, "P9"},
	{psp.StatusRequiresAction, psp.StatusSucceeded, "P9"},
	{psp.StatusAuthorised, psp.StatusSucceeded, "P9"},
	{psp.StatusUnknown, psp.StatusSucceeded, "P9"},
	{psp.StatusPending, psp.StatusFailed, "P10"},
	{psp.StatusRequiresAction, psp.StatusFailed, "P10"},
	{psp.StatusAuthorised, psp.StatusFailed, "P10"},
	{psp.StatusUnknown, psp.StatusFailed, "P10"},
	{psp.StatusPending, psp.StatusExpired, "P11"},
	{psp.StatusRequiresAction, psp.StatusExpired, "P11"},
	{psp.StatusAuthorised, psp.StatusExpired, "P11"},
	{psp.StatusUnknown, psp.StatusExpired, "P11"},
	{psp.StatusPending, psp.StatusCancelled, "P12"},
	{psp.StatusRequiresAction, psp.StatusCancelled, "P12"},
	{psp.StatusAuthorised, psp.StatusCancelled, "P12"},
	{psp.StatusUnknown, psp.StatusCancelled, "P12"},
	{psp.StatusPending, psp.StatusUnknown, "P13"},
	{psp.StatusUnknown, psp.StatusPending, "P14"},
	{psp.StatusUnknown, psp.StatusRequiresAction, "P14"},
	{psp.StatusFailed, psp.StatusSucceeded, "P15"},
	{psp.StatusExpired, psp.StatusSucceeded, "P15"},
	{psp.StatusCancelled, psp.StatusSucceeded, "P15"},
	{psp.StatusSucceeded, psp.StatusPartiallyRefunded, "P16"},
	{psp.StatusSucceeded, psp.StatusRefunded, "P16"},
	{psp.StatusPartiallyRefunded, psp.StatusRefunded, "P16"},
	{psp.StatusSucceeded, psp.StatusDisputed, "P17"},
	{psp.StatusPartiallyRefunded, psp.StatusDisputed, "P17"},
	{psp.StatusDisputed, psp.StatusSucceeded, "P18"},
	{psp.StatusDisputed, psp.StatusPartiallyRefunded, "P18b"},
	{psp.StatusDisputed, psp.StatusChargedBack, "P19"},
	{psp.StatusSucceeded, psp.StatusChargedBack, "P20"},
	{psp.StatusPartiallyRefunded, psp.StatusChargedBack, "P20"},
}

var edgeIndex = func() map[[2]string]string {
	m := make(map[[2]string]string, len(PaymentEdges))
	for _, e := range PaymentEdges {
		m[[2]string{e.From, e.To}] = e.Code
	}
	return m
}()

// EdgeCode returns the transition code of an allowed edge.
func EdgeCode(from, to string) (string, bool) {
	c, ok := edgeIndex[[2]string{from, to}]
	return c, ok
}

// Rank is app.payment_status_rank (transaction-lifecycle §5): precedence for out-of-order events.
var Rank = map[string]int{
	psp.StatusCreated: 0, psp.StatusUnknown: 5, psp.StatusPending: 10, psp.StatusRequiresAction: 20, psp.StatusAuthorised: 30,
	psp.StatusFailed: 50, psp.StatusCancelled: 50, psp.StatusExpired: 50, psp.StatusSucceeded: 60, psp.StatusPartiallyRefunded: 70,
	psp.StatusDisputed: 80, psp.StatusRefunded: 90, psp.StatusChargedBack: 95,
}

// exception reports the listed lower-or-equal-rank moves (payment-state-machine §3.1).
func exception(from, to, source string) bool {
	switch {
	case from == psp.StatusPending && to == psp.StatusUnknown:
		return source == SrcInternal
	case from == psp.StatusRequiresAction && to == psp.StatusPending,
		from == psp.StatusDisputed && to == psp.StatusSucceeded,
		from == psp.StatusDisputed && to == psp.StatusPartiallyRefunded:
		return true
	}
	return false
}

// sourceAllowed mirrors the ck_payment_events_* checks so a forbidden source is refused in Go before the
// database does it (both layers exist on purpose).
func sourceAllowed(from, to, source string) bool {
	switch to {
	case psp.StatusSucceeded:
		return source == SrcSync || source == SrcWebhook || source == SrcPoll || source == SrcRecon
	case psp.StatusUnknown:
		return source == SrcInternal
	case psp.StatusFailed:
		return source == SrcSync || source == SrcWebhook || source == SrcPoll || source == SrcRecon ||
			(source == SrcInternal && from == psp.StatusCreated)
	}
	return source != SrcDonor
}

// terminal statuses stop polling.
func terminal(st string) bool {
	switch st {
	case psp.StatusSucceeded, psp.StatusFailed, psp.StatusExpired, psp.StatusCancelled, psp.StatusPartiallyRefunded,
		psp.StatusRefunded, psp.StatusDisputed, psp.StatusChargedBack:
		return true
	}
	return false
}

// live statuses block a new payment attempt on the same donation intent.
func inProgress(st string) bool {
	return st == psp.StatusCreated || st == psp.StatusPending || st == psp.StatusRequiresAction || st == psp.StatusAuthorised
}

// paid statuses mean the donation intent has been paid (and stays so, even if later refunded).
func paid(st string) bool {
	return Rank[st] >= Rank[psp.StatusSucceeded]
}

// needsSuccessFirst are targets whose prerequisite is SUCCEEDED (parked when they arrive early).
func needsSuccessFirst(to string) bool {
	return to == psp.StatusPartiallyRefunded || to == psp.StatusRefunded || to == psp.StatusDisputed || to == psp.StatusChargedBack
}
