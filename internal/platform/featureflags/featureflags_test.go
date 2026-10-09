package featureflags

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

type row struct {
	on  bool
	err error
}

func (r row) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*(dest[0].(*bool)) = r.on
	return nil
}

type fakeQ struct {
	rows map[string]row
	hits int
}

func (f *fakeQ) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	f.hits++
	if r, ok := f.rows[args[0].(string)]; ok {
		return r
	}
	return row{err: pgx.ErrNoRows}
}

func TestEnabled(t *testing.T) {
	boom := errors.New("db down")
	q := &fakeQ{rows: map[string]row{IndividualForOthers: {on: false}, "ops.thing.enabled": {on: true}, "ops.broken.flag": {err: boom}}}
	ctx := context.Background()
	if on, err := Enabled(ctx, q, IndividualForOthers); on || err != nil {
		t.Fatalf("disabled flag: %v %v", on, err)
	}
	if on, err := Enabled(ctx, q, "ops.thing.enabled"); !on || err != nil {
		t.Fatalf("enabled flag: %v %v", on, err)
	}
	if on, err := Enabled(ctx, q, "ops.missing.flag"); on || !errors.Is(err, ErrUnknownFlag) {
		t.Fatalf("unknown flag must be off with ErrUnknownFlag: %v %v", on, err)
	}
	if on, err := Enabled(ctx, q, "ops.broken.flag"); on || !errors.Is(err, boom) {
		t.Fatalf("db error must be off with the error: %v %v", on, err)
	}
	before := q.hits
	for _, k := range []string{"", "NoDots", "a.b; DROP", "Upper.Case"} {
		if on, err := Enabled(ctx, q, k); on || !errors.Is(err, ErrUnknownFlag) {
			t.Errorf("%q: %v %v", k, on, err)
		}
	}
	if q.hits != before {
		t.Fatal("malformed keys must not reach the database")
	}
}
