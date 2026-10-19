package notifications

import "time"

// Stage 15 safety constants (ADR-047). Changing either needs a provider selection ADR, legal review (LR-010, LR-011,
// LR-023) and stage acceptance.
const (
	// LiveMessagingEnabled false: in production every external delivery (email, SMS, WhatsApp) is SUPPRESSED with
	// LIVE_MESSAGING_DISABLED. Only simulators and the development senders (Mailpit) run in development and test.
	LiveMessagingEnabled = false
	// MarketingEnabled false: marketing is never delivered, whatever the consent records say (LR-023).
	MarketingEnabled = false
	// ConsentPolicyVersion is the consent wording a GRANTED record must have been given under; a record under an older
	// version REQUIRES_RECONFIRMATION. Engineering placeholder until counsel approves consent wording (LR-023).
	ConsentPolicyVersion = "consent-v1-engineering-placeholder"
)

// Channels.
const (
	ChEmail    = "EMAIL"
	ChSMS      = "SMS"
	ChWhatsApp = "WHATSAPP"
	ChInApp    = "IN_APP"
)

// Channels lists every channel in delivery order.
var Channels = []string{ChInApp, ChEmail, ChSMS, ChWhatsApp}

// Categories.
const (
	CatSecurity      = "SECURITY"
	CatTransactional = "TRANSACTIONAL"
	CatCampaign      = "CAMPAIGN"
	CatVerification  = "VERIFICATION"
	CatPayment       = "PAYMENT"
	CatWithdrawal    = "WITHDRAWAL"
	CatPayout        = "PAYOUT"
	CatRisk          = "RISK"
	CatCompliance    = "COMPLIANCE"
	CatSupport       = "SUPPORT"
	CatAdmin         = "ADMINISTRATIVE"
	CatMarketing     = "MARKETING"
)

// Categories lists every category.
var Categories = []string{CatSecurity, CatTransactional, CatCampaign, CatVerification, CatPayment, CatWithdrawal, CatPayout, CatRisk,
	CatCompliance, CatSupport, CatAdmin, CatMarketing}

// Per-template exceptions (code-reviewed, ADR-047 §5):
//   - ACCOUNT_SUSPENDED reaches a suspended account (it is the suspension notice);
//   - REGISTRATION_EXISTING_ACCOUNT may use an unverified sign-in e-mail: it is Stage 4's anti-enumeration notice about
//     the address itself, sent to the address the registrant typed (as the verification e-mail is). No other message
//     goes to an unverified destination.
var (
	inactiveRecipientOK     = map[string]bool{"ACCOUNT_SUSPENDED": true}
	unverifiedSignInEmailOK = map[string]bool{"REGISTRATION_EXISTING_ACCOUNT": true}
)

// Consent purposes.
const (
	PurposeService   = "SERVICE_MESSAGES"
	PurposeMarketing = "MARKETING"
)

// CategoryPolicy is the code-reviewed delivery rule of a category. It is the approved routing policy: there is no
// channel fallback in Stage 15 (Fallback is empty for every category), so a suppressed channel is never silently
// replaced by another.
type CategoryPolicy struct {
	// Channels a notification of this category may use, for a USER recipient. STAFF recipients get IN_APP only.
	Channels []string
	// DefaultOn: channels on unless the user turns them off (the rest are off unless the user turns them on).
	DefaultOn []string
	// Mandatory: channels the user cannot turn off (database CHECK too for SECURITY).
	Mandatory []string
	// Purpose of the consent an external channel needs; MARKETING needs marketing consent on every channel.
	Purpose string
	// StaffOnly categories never reach a user account (risk and compliance outcomes never notify their subject, LR-008).
	StaffOnly bool
	// Fallback: approved channel substitutions (none in Stage 15).
	Fallback map[string]string
}

// Policies is the routing policy table (docs/stage-15/notification-architecture.md).
var Policies = map[string]CategoryPolicy{
	CatSecurity:      {Channels: []string{ChInApp, ChEmail, ChSMS}, DefaultOn: []string{ChInApp, ChEmail}, Mandatory: []string{ChInApp, ChEmail}, Purpose: PurposeService},
	CatTransactional: {Channels: []string{ChInApp, ChEmail}, DefaultOn: []string{ChInApp, ChEmail}, Mandatory: []string{ChInApp}, Purpose: PurposeService},
	CatCampaign:      {Channels: []string{ChInApp, ChEmail, ChSMS, ChWhatsApp}, DefaultOn: []string{ChInApp, ChEmail}, Mandatory: []string{ChInApp}, Purpose: PurposeService},
	CatVerification:  {Channels: []string{ChInApp, ChEmail, ChSMS, ChWhatsApp}, DefaultOn: []string{ChInApp, ChEmail}, Mandatory: []string{ChInApp}, Purpose: PurposeService},
	CatPayment:       {Channels: []string{ChInApp, ChEmail, ChSMS, ChWhatsApp}, DefaultOn: []string{ChInApp, ChEmail}, Mandatory: []string{ChInApp}, Purpose: PurposeService},
	CatWithdrawal:    {Channels: []string{ChInApp, ChEmail, ChSMS, ChWhatsApp}, DefaultOn: []string{ChInApp, ChEmail}, Mandatory: []string{ChInApp}, Purpose: PurposeService},
	CatPayout:        {Channels: []string{ChInApp, ChEmail, ChSMS, ChWhatsApp}, DefaultOn: []string{ChInApp, ChEmail}, Mandatory: []string{ChInApp}, Purpose: PurposeService},
	CatSupport:       {Channels: []string{ChInApp, ChEmail}, DefaultOn: []string{ChInApp, ChEmail}, Mandatory: []string{ChInApp}, Purpose: PurposeService},
	CatRisk:          {Channels: []string{ChInApp}, DefaultOn: []string{ChInApp}, Mandatory: []string{ChInApp}, StaffOnly: true},
	CatCompliance:    {Channels: []string{ChInApp}, DefaultOn: []string{ChInApp}, Mandatory: []string{ChInApp}, StaffOnly: true},
	CatAdmin:         {Channels: []string{ChInApp}, DefaultOn: []string{ChInApp}, Mandatory: []string{ChInApp}, StaffOnly: true},
	CatMarketing:     {Channels: []string{ChInApp, ChEmail, ChSMS, ChWhatsApp}, Purpose: PurposeMarketing},
}

// Suppression reasons of a delivery (database CHECK).
const (
	SupPreference        = "PREFERENCE_DISABLED"
	SupConsentMissing    = "CONSENT_MISSING"
	SupConsentWithdrawn  = "CONSENT_WITHDRAWN"
	SupConsentExpired    = "CONSENT_EXPIRED"
	SupConsentReconfirm  = "CONSENT_RECONFIRMATION_REQUIRED"
	SupMarketing         = "MARKETING_DISABLED"
	SupContactMissing    = "CONTACT_MISSING"
	SupContactUnverified = "CONTACT_UNVERIFIED"
	SupDestination       = "DESTINATION_SUPPRESSED"
	SupInactive          = "RECIPIENT_INACTIVE"
	SupSynthetic         = "SYNTHETIC_SOURCE"
	SupLiveDisabled      = "LIVE_MESSAGING_DISABLED"
	SupChannelDisabled   = "CHANNEL_DISABLED"
	SupNoProvider        = "PROVIDER_NOT_CONFIGURED"
	SupTemplateChannel   = "TEMPLATE_CHANNEL_MISSING"
	SupTemplateProvider  = "TEMPLATE_NOT_PROVIDER_APPROVED"
)

// Consent is one consent record as the router sees it (nil: NOT_REQUESTED).
type Consent struct {
	Status        string
	PolicyVersion string
	ExpiresAt     *time.Time
}

// RouteInput is everything the routing decision depends on (pure function input).
type RouteInput struct {
	Category                 string
	Channel                  string
	StaffRecipient           bool
	Synthetic                bool     // TEST payment / payout / simulated source
	Production               bool     // APP_ENV=production
	Active                   bool     // recipient account ACTIVE (or the template allows an inactive recipient)
	Preference               *bool    // explicit user preference for (category, channel); nil = default
	Consent                  *Consent // for (channel, policy purpose)
	ContactKnown             bool
	ContactVerified          bool
	Suppressed               bool // active suppression of the destination
	ProviderReady            bool // a provider is configured for the channel in this process
	TemplateHasChannel       bool // the active template version has content for the channel
	TemplateProviderApproved bool // WhatsApp: the template is accepted by the (simulated) provider
	Now                      time.Time
}

// Route decides whether one channel of one notification is delivered: "" (deliver) or a suppression reason. It is
// deterministic and has no side effects; the same function runs at creation and again before every send (consent,
// preferences, suppressions and contacts may have changed in between).
func Route(in RouteInput) string {
	p, ok := Policies[in.Category]
	if !ok || !contains(p.Channels, in.Channel) {
		return SupChannelDisabled
	}
	if in.StaffRecipient {
		// staff notifications are in-app only in Stage 15, except the security notices about the staff account itself
		// (sign-in protection, account linking): those keep their mandatory e-mail like every account's
		if in.Channel != ChInApp && !(in.Category == CatSecurity && in.Channel == ChEmail) {
			return SupChannelDisabled
		}
	} else if p.StaffOnly {
		return SupChannelDisabled
	}
	if in.Category == CatMarketing && !MarketingEnabled {
		return SupMarketing
	}
	if !in.Active {
		return SupInactive
	}
	if !in.TemplateHasChannel {
		return SupTemplateChannel
	}
	if in.Channel == ChInApp {
		if in.Preference != nil && !*in.Preference && !contains(p.Mandatory, ChInApp) {
			return SupPreference
		}
		return ""
	}
	// external channels
	if in.Synthetic {
		return SupSynthetic // TEST money never produces an external message (ADR-047 §8)
	}
	if in.Production && !LiveMessagingEnabled {
		return SupLiveDisabled
	}
	if !contains(p.Mandatory, in.Channel) {
		on := contains(p.DefaultOn, in.Channel)
		if in.Preference != nil {
			on = *in.Preference
		}
		if !on {
			return SupPreference
		}
	}
	if r := consentReason(in.Category, in.Channel, p.Purpose, in.Consent, in.Now); r != "" {
		return r
	}
	if !in.ContactKnown {
		return SupContactMissing
	}
	if !in.ContactVerified {
		return SupContactUnverified
	}
	if in.Suppressed {
		return SupDestination
	}
	if !in.ProviderReady {
		return SupNoProvider
	}
	if in.Channel == ChWhatsApp && !in.TemplateProviderApproved {
		return SupTemplateProvider
	}
	return ""
}

// ConsentRequired reports whether a channel needs a GRANTED consent record for a category: marketing always; SMS and
// WhatsApp service messages until counsel decides otherwise (LR-023). Email service messages keep the Stage 4–6 basis
// (verified address; LEGAL_REVIEW_REQUIRED).
func ConsentRequired(category, channel string) bool {
	if category == CatMarketing {
		return true
	}
	return channel == ChSMS || channel == ChWhatsApp
}

func consentReason(category, channel, purpose string, c *Consent, now time.Time) string {
	if !ConsentRequired(category, channel) {
		return ""
	}
	if c == nil {
		return SupConsentMissing
	}
	switch c.Status {
	case "GRANTED":
		if c.ExpiresAt != nil && !now.Before(*c.ExpiresAt) {
			return SupConsentExpired
		}
		if c.PolicyVersion != ConsentPolicyVersion {
			return SupConsentReconfirm
		}
		return ""
	case "WITHDRAWN":
		return SupConsentWithdrawn
	case "EXPIRED":
		return SupConsentExpired
	case "REQUIRES_RECONFIRMATION":
		return SupConsentReconfirm
	}
	return SupConsentMissing
}

// PurposeFor is the consent purpose a category's external channels use.
func PurposeFor(category string) string {
	if category == CatMarketing {
		return PurposeMarketing
	}
	return PurposeService
}

// PreferenceAllowed reports whether a user may set (category, channel) to enabled.
func PreferenceAllowed(category, channel string, enabled bool) bool {
	p, ok := Policies[category]
	if !ok || p.StaffOnly || !contains(p.Channels, channel) {
		return false
	}
	return enabled || !contains(p.Mandatory, channel)
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
