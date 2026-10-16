package payments

import (
	"context"
	"errors"
	"testing"

	"github.com/Fatifizo/fundzim/internal/risk"
)

type holdsStub struct {
	hs  []risk.Hold
	err error
}

func (h holdsStub) DonationHolds(context.Context, string, string) ([]risk.Hold, error) {
	return h.hs, h.err
}

// Stage 13: an enforced hold refuses a donation; an unreachable hold registry fails closed (the error stops the intent).
func TestCheckHoldsFailsClosed(t *testing.T) {
	ctx := context.Background()
	if err := (&Service{Deps: Deps{Holds: holdsStub{}}}).checkHolds(ctx, "c", "d"); err != nil {
		t.Fatalf("no hold: %v", err)
	}
	if err := (&Service{Deps: Deps{Holds: holdsStub{hs: []risk.Hold{{HoldType: risk.HoldDonation}}}}}).checkHolds(ctx, "c", "d"); !errors.Is(err, ErrDonationsOnHold) {
		t.Fatalf("hold not enforced: %v", err)
	}
	outage := errors.New("database unavailable")
	if err := (&Service{Deps: Deps{Holds: holdsStub{err: outage}}}).checkHolds(ctx, "c", "d"); !errors.Is(err, outage) {
		t.Fatalf("registry outage did not stop the donation: %v", err)
	}
}
