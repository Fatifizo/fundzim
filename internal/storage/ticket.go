package storage

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"time"
)

// DefaultTicketTTL is the access-ticket lifetime when none is configured (ADR-035 §5).
const DefaultTicketTTL = 60 * time.Second

// MaxTicketTTL bounds any requested lifetime.
const MaxTicketTTL = 5 * time.Minute

const ticketVersion = "v1"

// Tickets issues and verifies document access tickets: HMAC-SHA-256 over (object id, user id, session id,
// expiry) with a dedicated key. The ticket carries only the version and expiry plus the MAC; the object,
// user and session are supplied again by the verifier (from the URL and the authenticated session), so a
// ticket is useless for any other document, user or session.
type Tickets struct {
	key        []byte
	defaultTTL time.Duration
	now        func() time.Time
}

// NewTickets builds a ticket issuer. key must be at least 32 bytes.
func NewTickets(key []byte, defaultTTL time.Duration, now func() time.Time) (*Tickets, error) {
	if len(key) < 32 {
		return nil, invalidInput("document ticket key must be at least 32 bytes")
	}
	if defaultTTL <= 0 {
		defaultTTL = DefaultTicketTTL
	}
	if now == nil {
		now = time.Now
	}
	return &Tickets{key: append([]byte(nil), key...), defaultTTL: min(defaultTTL, MaxTicketTTL), now: now}, nil
}

func (t *Tickets) mac(objectID, userID, sessionID string, exp int64) []byte {
	m := hmac.New(sha256.New, t.key)
	// length-prefixed fields: no ambiguity between field boundaries
	for _, f := range []string{"fundzim-document-ticket", ticketVersion, objectID, userID, sessionID, strconv.FormatInt(exp, 10)} {
		m.Write([]byte(strconv.Itoa(len(f))))
		m.Write([]byte{':'})
		m.Write([]byte(f))
	}
	return m.Sum(nil)
}

// Issue returns a ticket for (objectID, userID, sessionID) valid for ttl (≤ 0: the default; capped at
// MaxTicketTTL) and its expiry.
func (t *Tickets) Issue(objectID, userID, sessionID string, ttl time.Duration) (string, time.Time) {
	if ttl <= 0 {
		ttl = t.defaultTTL
	}
	ttl = min(ttl, MaxTicketTTL)
	exp := t.now().Add(ttl).Truncate(time.Second)
	e := exp.Unix()
	return ticketVersion + "." + strconv.FormatInt(e, 10) + "." + base64.RawURLEncoding.EncodeToString(t.mac(objectID, userID, sessionID, e)), exp.UTC()
}

// Verify checks a ticket against the expected object, user and session. The MAC is compared in constant time
// before the expiry is looked at, so ErrTicketExpired is only returned for genuine tickets.
func (t *Tickets) Verify(ticket, objectID, userID, sessionID string) error {
	if len(ticket) > 200 || objectID == "" || userID == "" || sessionID == "" {
		return ErrTicketInvalid
	}
	parts := strings.Split(ticket, ".")
	if len(parts) != 3 || parts[0] != ticketVersion {
		return ErrTicketInvalid
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || exp <= 0 {
		return ErrTicketInvalid
	}
	got, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return ErrTicketInvalid
	}
	if !hmac.Equal(got, t.mac(objectID, userID, sessionID, exp)) {
		return ErrTicketInvalid
	}
	if !t.now().Before(time.Unix(exp, 0)) {
		return ErrTicketExpired
	}
	return nil
}
