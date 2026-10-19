package notifications

import (
	"errors"
	"strings"

	"github.com/nyaruka/phonenumbers"
)

// ErrPhone is a number that is not a valid E.164 phone number.
var ErrPhone = errors.New("notifications: invalid phone number")

// NormalizePhone returns the E.164 form of a phone number. A number without a country code is read as Zimbabwean
// (+263). Validity comes from the maintained libphonenumber metadata (github.com/nyaruka/phonenumbers); nothing about
// the mobile operator or SMS capability is inferred from the prefix — whether an SMS can be delivered is only known
// from the provider's delivery evidence.
func NormalizePhone(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 32 {
		return "", ErrPhone
	}
	n, err := phonenumbers.Parse(raw, "ZW")
	if err != nil || !phonenumbers.IsValidNumber(n) {
		return "", ErrPhone
	}
	return phonenumbers.Format(n, phonenumbers.E164), nil
}
