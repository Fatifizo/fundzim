package fees

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Fee quotes (ADR-044 §9). A quote is not a charge and not revenue. It is owned by the requesting user, expires,
// is invalidated when a policy or provider schedule in its currency changes state, and is bound at most once to one
// donation intent — only if the campaign, amount, currency and the policies in force still match.

// Quote statuses.
const (
	QActive      = "ACTIVE"
	QBound       = "BOUND"
	QExpired     = "EXPIRED"
	QInvalidated = "INVALIDATED"
)

// QuoteRequest asks for a donation fee quote. The client never supplies a rate or a fee.
type QuoteRequest struct {
	CampaignID    string  `json:"campaign_id"`
	AmountMinor   string  `json:"amount_minor"`
	Currency      string  `json:"currency"`
	PaymentMethod *string `json:"payment_method"`
}

// QuoteComponent is one fee line of a quote.
type QuoteComponent struct {
	Category           string  `json:"category"`
	Basis              string  `json:"basis"`
	AmountMinor        *string `json:"amount_minor"`
	Responsibility     string  `json:"responsibility"`
	DonorPartMinor     *string `json:"donor_part_minor"`
	OrganiserPartMinor *string `json:"organiser_part_minor"`
	PlatformPartMinor  *string `json:"platform_part_minor"`
	RoundingMode       string  `json:"rounding_mode"`
	PolicyVersionID    string  `json:"policy_version_id"`
	ProviderScheduleID string  `json:"provider_schedule_id"`
	Note               string  `json:"note"`
}

// Quote is a fee quote (owner view).
type Quote struct {
	ID                      string           `json:"id"`
	CampaignID              string           `json:"campaign_id"`
	TransactionType         string           `json:"transaction_type"`
	Currency                string           `json:"currency"`
	AmountMinor             string           `json:"amount_minor"`
	PaymentMethod           *string          `json:"payment_method"`
	PlatformFeeMinor        string           `json:"platform_fee_minor"`
	PlatformFeeBasis        string           `json:"platform_fee_basis"`
	ProviderFeeMinor        *string          `json:"provider_fee_minor"`
	ProviderFeeBasis        string           `json:"provider_fee_basis"`
	DonorChargeMinor        *string          `json:"donor_charge_minor"`
	CampaignAllocationMinor *string          `json:"campaign_allocation_minor"`
	PlatformNetMinor        *string          `json:"platform_net_minor"`
	Components              []QuoteComponent `json:"components"`
	Status                  string           `json:"status"`
	ExpiresAt               time.Time        `json:"expires_at"`
	DonationIntentID        *string          `json:"donation_intent_id"`
	CalculationID           string           `json:"calculation_id"`
	AlgorithmVersion        string           `json:"algorithm_version"`
	SystemGenerated         bool             `json:"system_generated"`
	FeeEnvironment          string           `json:"fee_environment"`
	Label                   string           `json:"label"`
	Notice                  string           `json:"notice"`
	CreatedAt               time.Time        `json:"created_at"`
	userID                  string
	policyVersionIDs        []string
	scheduleIDs             []string
	amount                  int64
	donorCharge             *int64
}

const quoteNotice = "A fee quote is an estimate for a TEST donation. It is not a charge, and nothing has been paid."

// storeCalculation persists a computed calculation and its components; it returns their ids.
func storeCalculation(ctx context.Context, tx pgx.Tx, out Outcome, basis int64, at time.Time) (string, []string, error) {
	id := ids.New()
	if _, err := tx.Exec(ctx, `INSERT INTO app.fee_calculations (id, algorithm_version, transaction_type, currency, basis_minor, inputs,
		inputs_hash, result, result_hash, calculated_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		id, out.Inputs.AlgorithmVersion, out.Inputs.TransactionType, out.Inputs.Currency, basis, out.InputsJSON, out.InputsHash[:],
		out.ResultJSON, out.ResultHash[:], at); err != nil {
		return "", nil, err
	}
	cids := make([]string, 0, len(out.Components))
	for i, c := range out.Components {
		cid := ids.New()
		if _, err := tx.Exec(ctx, `INSERT INTO app.fee_components (id, calculation_id, seq, category, policy_version_id, provider_schedule_id, basis,
			amount_minor, responsibility, donor_part_minor, organiser_part_minor, platform_part_minor, rounding_mode, exact_value)
			VALUES ($1, $2, $3, $4, nullif($5, '')::uuid, nullif($6, '')::uuid, $7, $8, nullif($9, ''), $10, $11, $12, nullif($13, ''), nullif($14, ''))`,
			cid, id, i+1, c.Category, c.PolicyVersionID, c.ProviderScheduleID, c.Basis, c.Amount, c.Responsibility, partOrNil(c, c.Parts.Donor),
			partOrNil(c, c.Parts.Organiser), partOrNil(c, c.Parts.Platform), c.Rounding, c.Exact); err != nil {
			return "", nil, err
		}
		cids = append(cids, cid)
	}
	return id, cids, nil
}

// partOrNil stores parts only for a known amount (ck_fee_components_parts).
func partOrNil(c Component, p *int64) *int64 {
	if c.Amount == nil {
		return nil
	}
	return p
}

// donationInputs selects the policies and provider evidence in force for a donation and returns canonical inputs.
func (s *Service) donationInputs(ctx context.Context, q querier, sc CampaignScope, currency, method, providerID string, amount int64, at time.Time) (Inputs, error) {
	scope := Scope{TransactionType: TxDonation, Currency: currency, PaymentMethod: method, ProviderID: providerID,
		CampaignCategory: sc.CategoryCode, CampaignID: sc.ID, At: at}
	scope.Category = CatPlatform
	pv, err := s.Resolve(ctx, q, scope)
	if err != nil {
		return Inputs{}, err
	}
	scope.Category = CatProcessing
	cv, err := s.Resolve(ctx, q, scope)
	if err != nil {
		return Inputs{}, err
	}
	sch, err := s.resolveSchedule(ctx, q, providerID, FeeTypeTransaction, currency, method, at)
	if err != nil {
		return Inputs{}, err
	}
	var platform, processing *PolicyInput
	var schedule *ScheduleInput
	if pv != nil {
		if platform, err = pv.input(); err != nil {
			return Inputs{}, err
		}
	}
	if cv != nil {
		if processing, err = cv.input(); err != nil {
			return Inputs{}, err
		}
	}
	if sch != nil {
		if schedule, err = sch.input(); err != nil {
			return Inputs{}, err
		}
		if !sch.RoundingKnown {
			schedule.Terms.Rounding = "" // unknown provider rounding: recorded as such
		}
	}
	return NewInputs(TxDonation, currency, amount, method, providerID, sc.ID, sc.CategoryCode, at.Format(time.RFC3339Nano),
		platform, processing, schedule), nil
}

func versionIDs(in Inputs) (pol, sch []string) {
	pol, sch = []string{}, []string{}
	if in.Platform != nil {
		pol = append(pol, in.Platform.VersionID)
	}
	if in.Provider != nil {
		pol = append(pol, in.Provider.VersionID)
	}
	if in.Schedule != nil {
		sch = append(sch, in.Schedule.ScheduleID)
	}
	return pol, sch
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, x := range a {
		m[x]++
	}
	for _, x := range b {
		if m[x] == 0 {
			return false
		}
		m[x]--
	}
	return true
}

func parseAmount(s string) (int64, error) {
	if !digitsRE.MatchString(s) {
		return 0, validation("amount_minor", "INVALID_FORMAT")
	}
	v, _ := strconv.ParseInt(s, 10, 64)
	if v <= 0 {
		return 0, validation("amount_minor", "INVALID_VALUE")
	}
	return v, nil
}

// routeMethod validates a method and returns the provider it would route to ("" when no method is chosen).
func (s *Service) routeMethod(ctx context.Context, method *string, currency string, amount int64) (string, string, error) {
	if method == nil || *method == "" {
		return "", "", nil
	}
	if !methodRE.MatchString(*method) {
		return "", "", validation("payment_method", "INVALID_FORMAT")
	}
	if s.Routing == nil {
		return "", "", ErrMethodUnavailable
	}
	methods, err := s.Routing.AvailableMethods(ctx, currency)
	if err != nil {
		return "", "", err
	}
	found := false
	for _, m := range methods {
		found = found || m == *method
	}
	if !found {
		return "", "", ErrMethodUnavailable
	}
	provider, err := s.Routing.RouteMethod(ctx, *method, currency, amount)
	if err != nil {
		return "", "", ErrMethodUnavailable
	}
	return *method, provider, nil
}

// CreateQuote computes and stores a donation fee quote for userID. idemKey (optional, a UUID) makes the request
// idempotent: the same key with the same request returns the same quote.
func (s *Service) CreateQuote(ctx context.Context, userID, idemKey string, in QuoteRequest) (Quote, error) {
	if !ids.Valid(in.CampaignID) {
		return Quote{}, validation("campaign_id", "INVALID_FORMAT")
	}
	amount, err := parseAmount(in.AmountMinor)
	if err != nil {
		return Quote{}, err
	}
	if idemKey != "" && !ids.Valid(idemKey) {
		return Quote{}, validation("Idempotency-Key", "INVALID_FORMAT")
	}
	if idemKey != "" {
		q, err := s.quoteByKey(ctx, userID, idemKey)
		if err == nil {
			m := ""
			if in.PaymentMethod != nil {
				m = *in.PaymentMethod
			}
			qm := ""
			if q.PaymentMethod != nil {
				qm = *q.PaymentMethod
			}
			if q.CampaignID != in.CampaignID || q.amount != amount || q.Currency != in.Currency || qm != m {
				return Quote{}, errs.New(errs.Conflict, "IDEMPOTENCY_KEY_REUSED", "This Idempotency-Key was already used for a different request.")
			}
			return q, nil
		} else if !errors.Is(err, ErrNotFound) {
			return Quote{}, err
		}
	}
	if err := s.currencyUsable(ctx, in.Currency); err != nil {
		return Quote{}, err
	}
	sc, err := s.Campaigns.FeeScope(ctx, in.CampaignID)
	if err != nil {
		return Quote{}, err
	}
	if !sc.AcceptsDonations {
		return Quote{}, ErrCampaignClosed
	}
	method, provider, err := s.routeMethod(ctx, in.PaymentMethod, in.Currency, amount)
	if err != nil {
		return Quote{}, err
	}
	var id string
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		id, _, err = s.insertQuote(ctx, tx, userID, idemKey, sc, in.Currency, method, provider, amount, false)
		return err
	})
	if err != nil {
		if idemKey != "" && isUnique(err, "uq_fee_quotes_idempotency") {
			return s.CreateQuote(ctx, userID, idemKey, in) // a concurrent duplicate won: replay it
		}
		return Quote{}, err
	}
	return s.quote(ctx, s.Pool, id)
}

// insertQuote computes and stores a quote in tx.
func (s *Service) insertQuote(ctx context.Context, tx pgx.Tx, userID, idemKey string, sc CampaignScope, currency, method, provider string,
	amount int64, system bool) (string, *int64, error) {
	if err := lockCurrency(ctx, tx, currency, false); err != nil {
		return "", nil, err
	}
	now := s.now()
	in, err := s.donationInputs(ctx, tx, sc, currency, method, provider, amount, now)
	if err != nil {
		return "", nil, err
	}
	out, err := Compute(in)
	if err != nil {
		return "", nil, calcError(err)
	}
	calcID, _, err := storeCalculation(ctx, tx, out, amount, now)
	if err != nil {
		return "", nil, err
	}
	d := out.Donation
	pol, sch := versionIDs(in)
	platformBasis := d.Platform.Basis
	if platformBasis != BasisNotConfigured {
		platformBasis = BasisQuoted
	}
	providerBasis := d.Provider.Basis
	id := ids.New()
	var idem any
	if idemKey != "" {
		idem = idemKey
	}
	var m, p any
	if method != "" {
		m = method
	}
	if provider != "" {
		p = provider
	}
	if _, err := tx.Exec(ctx, `INSERT INTO app.fee_quotes (id, user_id, campaign_id, currency, amount_minor, payment_method, provider_id,
		calculation_id, policy_version_ids, provider_schedule_ids, platform_fee_minor, platform_fee_basis, provider_fee_minor, provider_fee_basis,
		donor_charge_minor, campaign_allocation_minor, platform_net_minor, status, system_generated, idempotency_key, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, 'ACTIVE', $18, $19, $20, $21)`,
		id, userID, sc.ID, currency, amount, m, p, calcID, pol, sch, *d.Platform.Amount, platformBasis, d.ProviderFee, providerBasis,
		d.DonorCharge, d.CampaignAllocation, d.PlatformNet, system, idem, now.Add(s.quoteTTL()), now); err != nil {
		return "", nil, err
	}
	actor := Actor{Type: "USER", ID: userID}
	if err := event(ctx, tx, "fee_quote", id, "QUOTE_CREATED", actor, map[string]any{"calculation_id": calcID, "system_generated": system,
		"policy_version_ids": pol, "provider_schedule_ids": sch}, now); err != nil {
		return "", nil, err
	}
	return id, d.DonorCharge, nil
}

const quoteCols = `q.id, q.user_id, q.campaign_id, q.transaction_type, q.currency, q.amount_minor, q.payment_method, q.platform_fee_minor,
	q.platform_fee_basis, q.provider_fee_minor, q.provider_fee_basis, q.donor_charge_minor, q.campaign_allocation_minor, q.platform_net_minor,
	q.status, q.expires_at, q.donation_intent_id::text, q.calculation_id, c.algorithm_version, q.system_generated, q.fee_environment,
	q.created_at, q.policy_version_ids::text[], q.provider_schedule_ids::text[], c.result`

func (s *Service) scanQuote(r pgx.Row) (Quote, error) {
	var q Quote
	var platform int64
	var provider, charge, alloc, net *int64
	var result []byte
	err := r.Scan(&q.ID, &q.userID, &q.CampaignID, &q.TransactionType, &q.Currency, &q.amount, &q.PaymentMethod, &platform, &q.PlatformFeeBasis,
		&provider, &q.ProviderFeeBasis, &charge, &alloc, &net, &q.Status, &q.ExpiresAt, &q.DonationIntentID, &q.CalculationID, &q.AlgorithmVersion,
		&q.SystemGenerated, &q.FeeEnvironment, &q.CreatedAt, &q.policyVersionIDs, &q.scheduleIDs, &result)
	if errors.Is(err, pgx.ErrNoRows) {
		return q, ErrNotFound
	}
	if err != nil {
		return q, err
	}
	q.AmountMinor, q.PlatformFeeMinor = amt(q.amount), amt(platform)
	q.ProviderFeeMinor, q.DonorChargeMinor, q.CampaignAllocationMinor, q.PlatformNetMinor = amtP(provider), amtP(charge), amtP(alloc), amtP(net)
	q.donorCharge = charge
	q.Label, q.Notice = TestLabel, quoteNotice
	if q.Status == QActive && !q.ExpiresAt.After(s.now()) {
		q.Status = QExpired // the expiry job persists it
	}
	var doc ResultDoc
	if err := jsonUnmarshal(result, &doc); err != nil {
		return q, err
	}
	q.Components = []QuoteComponent{}
	for _, c := range doc.Components {
		q.Components = append(q.Components, QuoteComponent{Category: c.Category, Basis: c.Basis, AmountMinor: c.Amount, Responsibility: c.Responsibility,
			DonorPartMinor: c.Donor, OrganiserPartMinor: c.Organiser, PlatformPartMinor: c.Platform, RoundingMode: c.Rounding,
			PolicyVersionID: c.PolicyVersionID, ProviderScheduleID: c.ProviderScheduleID, Note: c.Note})
	}
	return q, nil
}

func (s *Service) quote(ctx context.Context, q querier, id string) (Quote, error) {
	if !ids.Valid(id) {
		return Quote{}, ErrNotFound
	}
	return s.scanQuote(q.QueryRow(ctx, `SELECT `+quoteCols+` FROM app.fee_quotes q JOIN app.fee_calculations c ON c.id = q.calculation_id
		WHERE q.id = $1`, id))
}

func (s *Service) quoteByKey(ctx context.Context, userID, key string) (Quote, error) {
	return s.scanQuote(s.Pool.QueryRow(ctx, `SELECT `+quoteCols+` FROM app.fee_quotes q JOIN app.fee_calculations c ON c.id = q.calculation_id
		WHERE q.user_id = $1 AND q.idempotency_key = $2`, userID, key))
}

// GetQuote returns a quote to its owner (anyone else: not found).
func (s *Service) GetQuote(ctx context.Context, userID, id string) (Quote, error) {
	q, err := s.quote(ctx, s.Pool, id)
	if err != nil {
		return Quote{}, err
	}
	if q.userID != userID {
		return Quote{}, ErrNotFound
	}
	return q, nil
}

// StaffQuote returns any quote (fee.view).
func (s *Service) StaffQuote(ctx context.Context, id string) (Quote, error) {
	return s.quote(ctx, s.Pool, id)
}

// Quotes lists quotes for staff (newest first).
func (s *Service) Quotes(ctx context.Context, status string, limit int) ([]Quote, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+quoteCols+` FROM app.fee_quotes q JOIN app.fee_calculations c ON c.id = q.calculation_id
		WHERE ($1 = '' OR q.status = $1) ORDER BY q.created_at DESC, q.id DESC LIMIT $2`, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Quote{}
	for rows.Next() {
		q, err := s.scanQuote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// ---- donation intent integration (called by payments inside its transaction) -------------------------------------

// IntentQuote is the quote a donation intent is bound to.
type IntentQuote struct {
	QuoteID          string
	DonorChargeMinor *int64 // nil when the donor charge depends on an UNKNOWN provider fee
}

// QuoteForIntent validates the caller's quote (quoteID non-empty) or creates a system quote, inside the payments
// transaction. A given quote must be ACTIVE, owned by userID, unexpired, for the same campaign, amount and currency,
// and the policies and provider evidence in force must be the ones it was computed with (otherwise FEE_QUOTE_STALE).
func (s *Service) QuoteForIntent(ctx context.Context, tx pgx.Tx, userID, quoteID, campaignID, currency string, amount int64) (IntentQuote, error) {
	sc, err := s.Campaigns.FeeScope(ctx, campaignID)
	if err != nil {
		return IntentQuote{}, err
	}
	if quoteID == "" {
		id, charge, err := s.insertQuote(ctx, tx, userID, "", sc, currency, "", "", amount, true)
		if err != nil {
			return IntentQuote{}, err
		}
		return IntentQuote{QuoteID: id, DonorChargeMinor: charge}, nil
	}
	if !ids.Valid(quoteID) {
		return IntentQuote{}, validation("fee_quote_id", "INVALID_FORMAT")
	}
	if err := lockCurrency(ctx, tx, currency, false); err != nil {
		return IntentQuote{}, err
	}
	q, err := s.scanQuote(tx.QueryRow(ctx, `SELECT `+quoteCols+` FROM app.fee_quotes q JOIN app.fee_calculations c ON c.id = q.calculation_id
		WHERE q.id = $1 FOR UPDATE OF q`, quoteID))
	if err != nil {
		return IntentQuote{}, err
	}
	if q.userID != userID {
		return IntentQuote{}, ErrNotFound
	}
	switch q.Status {
	case QBound:
		return IntentQuote{}, ErrQuoteUsed
	case QInvalidated:
		return IntentQuote{}, ErrQuoteStale
	case QExpired:
		return IntentQuote{}, ErrQuoteExpired
	}
	if q.CampaignID != campaignID || q.amount != amount || q.Currency != currency {
		return IntentQuote{}, ErrQuoteStale
	}
	method, provider := "", ""
	if q.PaymentMethod != nil {
		if method, provider, err = s.routeMethod(ctx, q.PaymentMethod, currency, amount); err != nil {
			return IntentQuote{}, ErrQuoteStale
		}
	}
	in, err := s.donationInputs(ctx, tx, sc, currency, method, provider, amount, s.now())
	if err != nil {
		return IntentQuote{}, err
	}
	pol, sch := versionIDs(in)
	if !sameSet(pol, q.policyVersionIDs) || !sameSet(sch, q.scheduleIDs) {
		return IntentQuote{}, ErrQuoteStale
	}
	return IntentQuote{QuoteID: q.ID, DonorChargeMinor: q.donorCharge}, nil
}

// MarkQuoteBound binds a quote to the donation intent just inserted in the same transaction.
func (s *Service) MarkQuoteBound(ctx context.Context, tx pgx.Tx, quoteID, intentID, userID string) error {
	now := s.now()
	tag, err := tx.Exec(ctx, `UPDATE app.fee_quotes SET status = 'BOUND', donation_intent_id = $2, bound_at = $3
		WHERE id = $1 AND status = 'ACTIVE'`, quoteID, intentID, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrQuoteUsed
	}
	return event(ctx, tx, "fee_quote", quoteID, "QUOTE_BOUND", Actor{Type: "USER", ID: userID}, map[string]any{"donation_intent_id": intentID}, now)
}

// ExpireQuotes marks ACTIVE quotes past their expiry EXPIRED (job).
func (s *Service) ExpireQuotes(ctx context.Context) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `UPDATE app.fee_quotes SET status = 'EXPIRED' WHERE status = 'ACTIVE' AND expires_at <= $1`, s.now())
	return tag.RowsAffected(), err
}
