package psp

// Provider error categories (Stage 9 brief §27). Every provider call outcome that is not a plain success is
// normalised onto one category so operations, metrics and retry policy never depend on a provider's own codes.
// Retry policy is fixed by category: an outcome that may have reached the provider is NEVER retried blindly
// (UNKNOWN_OUTCOME, PROVIDER_TIMEOUT); it is resolved by status query only.
const (
	ErrCatAuthenticationFailed   = "AUTHENTICATION_FAILED"
	ErrCatInvalidRequest         = "INVALID_REQUEST"
	ErrCatUnsupportedMethod      = "UNSUPPORTED_METHOD"
	ErrCatUnsupportedCurrency    = "UNSUPPORTED_CURRENCY"
	ErrCatRateLimited            = "RATE_LIMITED"
	ErrCatProviderUnavailable    = "PROVIDER_UNAVAILABLE"
	ErrCatProviderTimeout        = "PROVIDER_TIMEOUT"
	ErrCatUnknownOutcome         = "UNKNOWN_OUTCOME"
	ErrCatPaymentDeclined        = "PAYMENT_DECLINED"
	ErrCatReconciliationRequired = "RECONCILIATION_REQUIRED"
)

// ErrorCategories lists every category (order used in docs and the operations view).
var ErrorCategories = []string{ErrCatAuthenticationFailed, ErrCatInvalidRequest, ErrCatUnsupportedMethod, ErrCatUnsupportedCurrency,
	ErrCatRateLimited, ErrCatProviderUnavailable, ErrCatProviderTimeout, ErrCatUnknownOutcome, ErrCatPaymentDeclined,
	ErrCatReconciliationRequired}

// Normalised failure codes an adapter may put in Observation.FailureCode to refine a category.
const (
	FailureDeclined            = "DECLINED"
	FailureUnsupportedCurrency = "UNSUPPORTED_CURRENCY"
)

// RetryPolicy says what the caller may do after a category.
type RetryPolicy string

const (
	// RetryNever: the request is definitively refused; repeating it cannot succeed.
	RetryNever RetryPolicy = "NEVER"
	// RetryAfterBackoff: the request was definitely not processed (not sent, or refused before processing); the
	// SAME request with the SAME reference may be repeated after a back-off.
	RetryAfterBackoff RetryPolicy = "AFTER_BACKOFF"
	// RetryStatusQueryOnly: the outcome is unknown; only a status query may follow. A new create is forbidden.
	RetryStatusQueryOnly RetryPolicy = "STATUS_QUERY_ONLY"
	// RetryOperatorReview: a person must look at the evidence first.
	RetryOperatorReview RetryPolicy = "OPERATOR_REVIEW"
)

var categoryRetry = map[string]RetryPolicy{
	ErrCatAuthenticationFailed:   RetryOperatorReview,
	ErrCatInvalidRequest:         RetryNever,
	ErrCatUnsupportedMethod:      RetryNever,
	ErrCatUnsupportedCurrency:    RetryNever,
	ErrCatRateLimited:            RetryAfterBackoff,
	ErrCatProviderUnavailable:    RetryAfterBackoff,
	ErrCatProviderTimeout:        RetryStatusQueryOnly,
	ErrCatUnknownOutcome:         RetryStatusQueryOnly,
	ErrCatPaymentDeclined:        RetryNever,
	ErrCatReconciliationRequired: RetryOperatorReview,
}

// CategoryRetry returns the retry policy of a category (OPERATOR_REVIEW for an unknown category: fail safe).
func CategoryRetry(cat string) RetryPolicy {
	if p, ok := categoryRetry[cat]; ok {
		return p
	}
	return RetryOperatorReview
}

// ValidErrorCategory reports whether c is a category.
func ValidErrorCategory(c string) bool { _, ok := categoryRetry[c]; return ok }

// NormaliseError maps one observation to its category. It returns "" for an outcome that is not an error
// (ACCEPTED, or an authoritative NOT_FOUND that the caller handles by its own finality rule). A mapped definite
// failure is PAYMENT_DECLINED only when the adapter says DECLINED; any other definitive refusal is INVALID_REQUEST.
// An unmapped provider status is RECONCILIATION_REQUIRED (never guessed).
func NormaliseError(obs Observation) string {
	switch obs.Class {
	case ClassAccepted:
		if obs.RawStatus != "" && !obs.Mapped {
			return ErrCatReconciliationRequired
		}
		return ""
	case ClassNotFound:
		return ""
	case ClassAuthConfig:
		return ErrCatAuthenticationFailed
	case ClassInvalidRequest:
		if obs.FailureCode == FailureUnsupportedCurrency {
			return ErrCatUnsupportedCurrency
		}
		return ErrCatInvalidRequest
	case ClassNotSupported:
		if obs.FailureCode == FailureUnsupportedCurrency {
			return ErrCatUnsupportedCurrency
		}
		return ErrCatUnsupportedMethod
	case ClassRateLimited:
		return ErrCatRateLimited
	case ClassRetryableBeforeSend:
		return ErrCatProviderUnavailable
	case ClassIndeterminate:
		if obs.UnknownReason == UnknownTimeout {
			return ErrCatProviderTimeout
		}
		return ErrCatUnknownOutcome
	case ClassDefiniteFailure:
		if obs.FailureCode == FailureDeclined {
			return ErrCatPaymentDeclined
		}
		if obs.FailureCode == FailureUnsupportedCurrency {
			return ErrCatUnsupportedCurrency
		}
		return ErrCatInvalidRequest
	}
	return ErrCatUnknownOutcome // an unclassified outcome is never treated as a failure
}
