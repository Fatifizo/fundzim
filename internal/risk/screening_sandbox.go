package risk

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// The screening simulator (TEST only; brief §30). It is the only ScreeningProvider in Stage 13 and produces
// synthetic, TEST_ONLY results from the scenario named in the synthetic reference ("TEST-<SCENARIO>-<suffix>"). It is
// refused in production by configuration (RISK_SCREENING_SANDBOX_ENABLED) and its results are never presented as
// screening of a real person. Its state lives in risk.sandbox_screenings (like the payment simulator's tables).

// SandboxScenarios are the simulator scenarios (ck_sandbox_screenings_scenario).
var SandboxScenarios = map[string]bool{"NO_MATCH": true, "POTENTIAL_MATCH": true, "MULTIPLE_MATCHES": true, "PROVIDER_TIMEOUT": true,
	"PROVIDER_ERROR": true, "DUPLICATE_CALLBACK": true, "OUT_OF_ORDER_CALLBACK": true, "INVALID_SIGNATURE": true, "DELAYED_RESULT": true}

// SandboxCode is the simulator's provider code.
const SandboxCode = "sandbox"

// ScreeningSignatureHeader carries the callback signature.
const ScreeningSignatureHeader = "FundZim-Screening-Signature"

// SandboxDelays (engineering defaults for the simulator).
const (
	SandboxReadyDelay   = 2 * time.Second
	SandboxDelayedReady = 3 * time.Minute
	CallbackReplayWin   = 5 * time.Minute
)

// SandboxScreening is the simulator.
type SandboxScreening struct {
	Pool  *pgxpool.Pool
	Clock clock.Clock
	Key   []byte // callback signing key (derived from the platform key)
	// Deliver sends a signed callback (the risk service's HandleScreeningCallback, or an HTTP client in tests).
	Deliver func(ctx context.Context, header string, body []byte) error
}

var scenarioRE = regexp.MustCompile(`^TEST-([A-Z_]+)-`)

// GetProviderCapabilities implements ScreeningProvider.
func (p *SandboxScreening) GetProviderCapabilities() ProviderCapabilities {
	return ProviderCapabilities{Code: SandboxCode, Environment: "TEST", Webhooks: true, Polling: true, SyntheticOnly: true}
}

// SubmitScreening implements ScreeningProvider.
func (p *SandboxScreening) SubmitScreening(ctx context.Context, sub ScreeningSubmission) (string, error) {
	m := scenarioRE.FindStringSubmatch(sub.SyntheticReference)
	if m == nil || !SandboxScenarios[m[1]] {
		return "", errors.New("sandbox screening: unknown scenario")
	}
	now := p.Clock.Now().UTC()
	ready := now.Add(SandboxReadyDelay)
	switch m[1] {
	case "DELAYED_RESULT":
		ready = now.Add(SandboxDelayedReady)
	case "PROVIDER_TIMEOUT":
		ready = now.Add(100 * 365 * 24 * time.Hour) // never answers
	}
	ref := "SBXS-" + ids.New()
	_, err := p.Pool.Exec(ctx, `INSERT INTO risk.sandbox_screenings (provider_reference, scenario, request_id, status, ready_at)
		VALUES ($1, $2, $3, 'PENDING', $4)`, ref, m[1], sub.RequestID, ready)
	return ref, err
}

type sandboxRow struct {
	scenario string
	ready    time.Time
	sent     int
}

func (p *SandboxScreening) row(ctx context.Context, ref string) (sandboxRow, error) {
	var r sandboxRow
	err := p.Pool.QueryRow(ctx, `SELECT scenario, ready_at, callbacks_sent FROM risk.sandbox_screenings WHERE provider_reference = $1`, ref).
		Scan(&r.scenario, &r.ready, &r.sent)
	return r, err
}

// result is the scenario's final result.
func sandboxResult(ref, scenario string) ProviderResult {
	r := ProviderResult{EventID: "sbx:" + ref + ":final", Sequence: 2, Status: ScrNoMatch, Matches: []ScreeningMatch{}}
	switch scenario {
	case "POTENTIAL_MATCH", "OUT_OF_ORDER_CALLBACK", "DUPLICATE_CALLBACK":
		r.Status = ScrPotentialMatch
		r.Matches = []ScreeningMatch{{MatchID: "TEST-MATCH-1", List: "TEST_SYNTHETIC_LIST", Score: 72, Label: "TEST_ONLY"}}
	case "MULTIPLE_MATCHES":
		r.Status = ScrPotentialMatch
		for i := 1; i <= 3; i++ {
			r.Matches = append(r.Matches, ScreeningMatch{MatchID: "TEST-MATCH-" + strconv.Itoa(i), List: "TEST_SYNTHETIC_LIST", Score: 60 + 10*i,
				Label: "TEST_ONLY"})
		}
	case "PROVIDER_ERROR":
		r.Status = ScrError
	}
	return r
}

// GetScreeningStatus implements ScreeningProvider (polling).
func (p *SandboxScreening) GetScreeningStatus(ctx context.Context, ref string) (ProviderResult, error) {
	r, err := p.row(ctx, ref)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProviderResult{}, errors.New("sandbox screening: unknown reference")
	}
	if err != nil {
		return ProviderResult{}, err
	}
	if r.scenario == "PROVIDER_TIMEOUT" {
		return ProviderResult{}, ErrProviderTimeout
	}
	if p.Clock.Now().Before(r.ready) {
		return ProviderResult{EventID: "sbx:" + ref + ":pending", Sequence: 1, Status: ScrPending, Matches: []ScreeningMatch{}}, nil
	}
	res := sandboxResult(ref, r.scenario)
	res.EventID = "sbx:" + ref + ":poll"
	return res, nil
}

// RetrieveMatches implements ScreeningProvider.
func (p *SandboxScreening) RetrieveMatches(ctx context.Context, ref string) ([]ScreeningMatch, error) {
	r, err := p.row(ctx, ref)
	if err != nil {
		return nil, err
	}
	if p.Clock.Now().Before(r.ready) {
		return []ScreeningMatch{}, nil
	}
	return sandboxResult(ref, r.scenario).Matches, nil
}

// SignScreeningCallback returns the signature header for body at t.
func SignScreeningCallback(key []byte, t time.Time, body []byte) string {
	ts := strconv.FormatInt(t.Unix(), 10)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	return "t=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

var screeningSigRE = regexp.MustCompile(`^t=([0-9]{1,12}),v1=([0-9a-f]{64})$`)

// VerifyScreeningWebhook implements ScreeningProvider: constant-time HMAC and a replay window.
func (p *SandboxScreening) VerifyScreeningWebhook(header string, body []byte, receivedAt time.Time) (ScreeningCallback, error) {
	if len(p.Key) < 32 {
		return ScreeningCallback{}, errors.New("callback key not configured")
	}
	m := screeningSigRE.FindStringSubmatch(strings.TrimSpace(header))
	if m == nil {
		return ScreeningCallback{}, errors.New("signature missing or malformed")
	}
	ts, _ := strconv.ParseInt(m[1], 10, 64)
	want := SignScreeningCallback(p.Key, time.Unix(ts, 0), body)
	if !hmac.Equal([]byte(want), []byte(strings.TrimSpace(header))) {
		return ScreeningCallback{}, errors.New("signature invalid")
	}
	if d := receivedAt.Sub(time.Unix(ts, 0)); d > CallbackReplayWin || d < -CallbackReplayWin {
		return ScreeningCallback{}, errors.New("timestamp outside the replay window")
	}
	var cb ScreeningCallback
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cb); err != nil || cb.ProviderReference == "" {
		return ScreeningCallback{}, errors.New("malformed callback body")
	}
	return cb, nil
}

// DeliverDue sends the callbacks of every ready simulated screening (job and test control). Scenario behaviour:
// DUPLICATE_CALLBACK sends the final callback twice; OUT_OF_ORDER_CALLBACK sends the final result before an older
// PENDING one; INVALID_SIGNATURE sends a badly signed callback (rejected; polling then finds the result);
// PROVIDER_TIMEOUT sends nothing.
func (p *SandboxScreening) DeliverDue(ctx context.Context, limit int) (int, error) {
	if p.Deliver == nil {
		return 0, nil
	}
	if limit <= 0 {
		limit = 50
	}
	now := p.Clock.Now().UTC()
	rows, err := p.Pool.Query(ctx, `SELECT provider_reference, scenario FROM risk.sandbox_screenings WHERE callbacks_sent = 0 AND ready_at <= $1
		AND scenario <> 'PROVIDER_TIMEOUT' ORDER BY ready_at LIMIT $2`, now, limit)
	if err != nil {
		return 0, err
	}
	type due struct{ ref, scenario string }
	var list []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.ref, &d.scenario); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, d)
	}
	rows.Close()
	n := 0
	for _, d := range list {
		tag, err := p.Pool.Exec(ctx, `UPDATE risk.sandbox_screenings SET callbacks_sent = callbacks_sent + 1, status = 'DELIVERED'
			WHERE provider_reference = $1 AND callbacks_sent = 0`, d.ref)
		if err != nil {
			return n, err
		}
		if tag.RowsAffected() == 0 {
			continue // another worker took it
		}
		final := ScreeningCallback{ProviderReference: d.ref, ProviderResult: sandboxResult(d.ref, d.scenario)}
		var sends []ScreeningCallback
		switch d.scenario {
		case "DUPLICATE_CALLBACK":
			sends = []ScreeningCallback{final, final}
		case "OUT_OF_ORDER_CALLBACK":
			older := ScreeningCallback{ProviderReference: d.ref, ProviderResult: ProviderResult{EventID: "sbx:" + d.ref + ":pending", Sequence: 1,
				Status: ScrPending, Matches: []ScreeningMatch{}}}
			sends = []ScreeningCallback{final, older}
		default:
			sends = []ScreeningCallback{final}
		}
		for _, cb := range sends {
			body, _ := json.Marshal(cb)
			key := p.Key
			if d.scenario == "INVALID_SIGNATURE" {
				key = []byte("an-invalid-signing-key-for-the-test-scenario")
			}
			// a rejected or duplicate callback is an expected scenario outcome, not a delivery failure
			_ = p.Deliver(ctx, SignScreeningCallback(key, now, body), body)
		}
		n++
	}
	return n, nil
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
