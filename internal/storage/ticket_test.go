package storage

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/clock"
)

const (
	tObj  = "0192f5a1-0000-7000-8000-000000000001"
	tUser = "0192f5a1-0000-7000-8000-000000000002"
	tSess = "0192f5a1-0000-7000-8000-000000000003"
)

func newTickets(t *testing.T, c *clock.Fake) *Tickets {
	t.Helper()
	tk, err := NewTickets([]byte(strings.Repeat("k", 32)), 0, c.Now)
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func TestTicketRoundTripAndDefaultTTL(t *testing.T) {
	c := clock.NewFake(time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC))
	tk := newTickets(t, c)
	ticket, exp := tk.Issue(tObj, tUser, tSess, 0)
	if got := exp.Sub(c.Now()); got != DefaultTicketTTL {
		t.Fatalf("default ttl %v", got)
	}
	if err := tk.Verify(ticket, tObj, tUser, tSess); err != nil {
		t.Fatalf("valid ticket rejected: %v", err)
	}
	c.Advance(59 * time.Second)
	if err := tk.Verify(ticket, tObj, tUser, tSess); err != nil {
		t.Fatalf("ticket rejected before expiry: %v", err)
	}
	c.Advance(time.Second)
	if err := tk.Verify(ticket, tObj, tUser, tSess); !errors.Is(err, ErrTicketExpired) {
		t.Fatalf("expired ticket: %v", err)
	}
}

func TestTicketBindingAndTampering(t *testing.T) {
	c := clock.NewFake(time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC))
	tk := newTickets(t, c)
	ticket, _ := tk.Issue(tObj, tUser, tSess, time.Minute)
	other := "0192f5a1-0000-7000-8000-0000000000ff"
	for name, err := range map[string]error{
		"wrong object":  tk.Verify(ticket, other, tUser, tSess),
		"wrong user":    tk.Verify(ticket, tObj, other, tSess),
		"wrong session": tk.Verify(ticket, tObj, tUser, other),
		"empty":         tk.Verify("", tObj, tUser, tSess),
		"garbage":       tk.Verify("v1.x.y", tObj, tUser, tSess),
		"other version": tk.Verify("v2"+ticket[2:], tObj, tUser, tSess),
	} {
		if !errors.Is(err, ErrTicketInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// extend the expiry without re-signing
	parts := strings.Split(ticket, ".")
	forged := parts[0] + ".9999999999." + parts[2]
	if err := tk.Verify(forged, tObj, tUser, tSess); !errors.Is(err, ErrTicketInvalid) {
		t.Errorf("forged expiry accepted: %v", err)
	}
	// flip one MAC character
	mac := []byte(parts[2])
	mac[0] ^= 1
	if err := tk.Verify(parts[0]+"."+parts[1]+"."+string(mac), tObj, tUser, tSess); !errors.Is(err, ErrTicketInvalid) {
		t.Errorf("tampered MAC accepted: %v", err)
	}
	// a ticket from another key
	tk2, _ := NewTickets([]byte(strings.Repeat("z", 32)), 0, c.Now)
	t2, _ := tk2.Issue(tObj, tUser, tSess, time.Minute)
	if err := tk.Verify(t2, tObj, tUser, tSess); !errors.Is(err, ErrTicketInvalid) {
		t.Errorf("foreign-key ticket accepted: %v", err)
	}
	// an expired AND tampered ticket reports invalid, not expired
	c.Advance(time.Hour)
	if err := tk.Verify(ticket, other, tUser, tSess); !errors.Is(err, ErrTicketInvalid) {
		t.Errorf("expired tampered: %v", err)
	}
}

func TestTicketTTLCappedAndKeyLength(t *testing.T) {
	c := clock.NewFake(time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC))
	tk := newTickets(t, c)
	_, exp := tk.Issue(tObj, tUser, tSess, 24*time.Hour)
	if exp.Sub(c.Now()) != MaxTicketTTL {
		t.Fatalf("ttl not capped: %v", exp.Sub(c.Now()))
	}
	if _, err := NewTickets([]byte("short"), 0, nil); err == nil {
		t.Fatal("short key accepted")
	}
}
