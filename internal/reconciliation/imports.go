package reconciliation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
	"github.com/Fatifizo/fundzim/internal/storage"
)

// Imports (docs/stage-17/settlement-management.md §2). A statement file is hashed first, deduplicated by hash (never by
// filename), stored once as private C2 evidence, referenced by an audit evidence record with the same hash, scanned, and
// parsed only when the scan is CLEAN — all or nothing. Parsed lines are immutable; a corrected re-issue must supersede
// the earlier import explicitly, and a line that comes back with different content without one is CONFLICTING_EVIDENCE.

// DefaultMaxFileBytes caps a statement file.
const DefaultMaxFileBytes = 10 << 20

// Source is a configured statement stream.
type Source struct {
	ID            string  `json:"id"`
	Code          string  `json:"code"`
	Kind          string  `json:"source_kind"`
	ProviderCode  *string `json:"provider_code"`
	AuthorityRank int     `json:"authority_rank"`
	Format        string  `json:"format"`
	Environment   string  `json:"environment"`
	Description   string  `json:"description"`
	Enabled       bool    `json:"enabled"`
}

// Import is one statement file as staff see it.
type Import struct {
	ID                 string            `json:"id"`
	SourceID           string            `json:"source_id"`
	SourceCode         string            `json:"source_code"`
	SourceKind         string            `json:"source_kind"`
	Origin             string            `json:"origin"`
	ContentSHA256      string            `json:"content_sha256"`
	SizeBytes          int64             `json:"size_bytes"`
	MediaType          string            `json:"media_type"`
	OriginalFilename   *string           `json:"original_filename"`
	StoredObjectID     string            `json:"stored_object_id"`
	EvidenceRecordID   string            `json:"evidence_record_id"`
	StatementRef       *string           `json:"statement_ref"`
	PeriodStart        *time.Time        `json:"period_start"`
	PeriodEnd          *time.Time        `json:"period_end"`
	Currency           *string           `json:"currency"`
	OpeningBalance     *string           `json:"opening_balance_minor"`
	ClosingBalance     *string           `json:"closing_balance_minor"`
	AccountRefHint     *string           `json:"account_ref_hint"`
	DeclaredLines      *int              `json:"declared_lines"`
	LinesParsed        int               `json:"lines_parsed"`
	LinesDuplicate     int               `json:"lines_duplicate"`
	LinesConflicting   int               `json:"lines_conflicting"`
	BatchesParsed      int               `json:"batches_parsed"`
	Status             string            `json:"status"`
	FailureCode        *string           `json:"failure_code"`
	ValidationReport   []ValidationError `json:"validation_report"`
	SupersedesImportID *string           `json:"supersedes_import_id"`
	Environment        string            `json:"environment"`
	UploadedBy         *string           `json:"uploaded_by"`
	ReceivedAt         time.Time         `json:"received_at"`
	ParsedAt           *time.Time        `json:"parsed_at"`
	Label              string            `json:"label"` // always TEST_ONLY in Stage 17
}

// UploadInput is POST /admin/reconciliation/imports.
type UploadInput struct {
	SourceCode         string
	MediaType          string
	Filename           string
	SupersedesImportID string
	Content            []byte
}

// Sources lists the configured sources.
func (s *Service) Sources(ctx context.Context) ([]Source, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id::text, code, source_kind, provider_code, authority_rank, format, environment, description, enabled
		FROM recon.sources ORDER BY authority_rank, code`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Source, error) {
		var x Source
		err := r.Scan(&x.ID, &x.Code, &x.Kind, &x.ProviderCode, &x.AuthorityRank, &x.Format, &x.Environment, &x.Description, &x.Enabled)
		return x, err
	})
}

func (s *Service) sourceByCode(ctx context.Context, code string) (Source, error) {
	list, err := s.Sources(ctx)
	if err != nil {
		return Source{}, err
	}
	for _, x := range list {
		if x.Code == code && x.Enabled {
			return x, nil
		}
	}
	return Source{}, httpx.Validation(errs.Detail{Field: "source", Code: "UNKNOWN_SOURCE"})
}

func (s *Service) maxBytes() int64 {
	if s.MaxFileBytes > 0 {
		return s.MaxFileBytes
	}
	return DefaultMaxFileBytes
}

// cleanFilename keeps a display-only filename: printable, no path, at most 200 characters.
func cleanFilename(name string) *string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) && r != '"' && r != '<' && r != '>' {
			return r
		}
		return -1
	}, strings.TrimSpace(name))
	if r := []rune(name); len(r) > 200 {
		name = string(r[:200])
	}
	if name == "" {
		return nil
	}
	return &name
}

// Upload records a staff-supplied TEST statement (step-up enforced by the route permission).
func (s *Service) Upload(ctx context.Context, staffID string, in UploadInput) (Import, error) {
	return s.receive(ctx, "STAFF_UPLOAD", staffID, in)
}

func (s *Service) receive(ctx context.Context, origin, staffID string, in UploadInput) (Import, error) {
	if s.Production && !ProductionImportsEnabled {
		return Import{}, ErrProductionImport
	}
	src, err := s.sourceByCode(ctx, in.SourceCode)
	if err != nil {
		return Import{}, err
	}
	if in.MediaType != storage.TypeCSV && in.MediaType != storage.TypeJSON {
		return Import{}, httpx.Validation(errs.Detail{Field: "file", Code: "UNSUPPORTED_MEDIA_TYPE"})
	}
	if len(in.Content) == 0 {
		return Import{}, httpx.Validation(errs.Detail{Field: "file", Code: "EMPTY_FILE"})
	}
	if int64(len(in.Content)) > s.maxBytes() {
		return Import{}, httpx.Validation(errs.Detail{Field: "file", Code: "FILE_TOO_LARGE"})
	}
	sum := sha256.Sum256(in.Content)
	var existing string
	if err := s.Pool.QueryRow(ctx, `SELECT id::text FROM recon.imports WHERE content_sha256 = $1`, sum[:]).Scan(&existing); err == nil {
		return Import{}, duplicateImport(existing)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Import{}, err
	}
	var supersedes *string
	if in.SupersedesImportID != "" {
		if !ids.Valid(in.SupersedesImportID) {
			return Import{}, httpx.Validation(errs.Detail{Field: "supersedes_import_id", Code: "INVALID_FORMAT"})
		}
		var srcID, st string
		err := s.Pool.QueryRow(ctx, `SELECT source_id::text, status FROM recon.imports WHERE id = $1`, in.SupersedesImportID).Scan(&srcID, &st)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && (srcID != src.ID || st != "PARSED")) {
			return Import{}, httpx.Validation(errs.Detail{Field: "supersedes_import_id", Code: "NOT_A_PARSED_IMPORT_OF_THIS_SOURCE"})
		}
		if err != nil {
			return Import{}, err
		}
		supersedes = &in.SupersedesImportID
	}
	uploader := staffID
	if origin != "STAFF_UPLOAD" {
		uploader = ""
	}
	obj, err := s.Storage.Upload(ctx, storage.UploadInput{BucketClass: storage.BucketPrivateEvidence, Purpose: "RECONCILIATION_REPORT",
		OwnerModule: "reconciliation", UploaderUserID: uploader, DeclaredContentType: in.MediaType, Body: bytes.NewReader(in.Content),
		MaxBytes: s.maxBytes()})
	if err != nil {
		return Import{}, err
	}
	id := ids.New()
	now := s.now()
	evType := map[string]string{KindTransactionReport: "PSP_TRANSACTION_REPORT", KindSettlementReport: "PSP_SETTLEMENT_REPORT",
		KindBankStatement: "BANK_STATEMENT"}[src.Kind]
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		actorType, actorID := "staff", staffID
		evid, err := audit.Append(ctx, tx, audit.Event{Action: "reconciliation.import.received", ActorType: actorType, ActorID: actorID,
			TargetType: "reconciliation_import", TargetID: id, Metadata: map[string]any{"source": src.Code, "origin": origin,
				"content_sha256": hex.EncodeToString(sum[:]), "size_bytes": len(in.Content), "media_type": in.MediaType,
				"stored_object_id": obj.ID, "supersedes_import_id": supersedes, "environment": "TEST"}})
		if err != nil {
			return err
		}
		collected := "staff"
		if origin != "STAFF_UPLOAD" {
			collected = "system"
		}
		evidenceID, err := audit.RecordEvidence(ctx, tx, audit.Evidence{Type: evType, SubjectType: "reconciliation_import", SubjectID: id,
			RelatedRefs: map[string]any{"source": src.Code, "stored_object_id": obj.ID, "environment": "TEST"}, CollectedAt: now,
			CollectedByType: collected, CollectedByID: map[bool]string{true: staffID, false: ""}[collected == "staff"],
			Source: map[bool]string{true: "upload", false: "system_snapshot"}[collected == "staff"], StorageRef: obj.ID, SHA256: sum[:],
			SizeBytes: int64(len(in.Content)), MediaType: in.MediaType, Classification: "C2", RetentionClass: "FINANCIAL", AuditEventID: evid})
		if err != nil {
			return err
		}
		var up any
		if uploader != "" {
			up = uploader
		}
		_, err = tx.Exec(ctx, `INSERT INTO recon.imports (id, source_id, origin, content_sha256, size_bytes, media_type, original_filename,
			stored_object_id, evidence_record_id, status, supersedes_import_id, uploaded_by, received_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'RECEIVED', $10, $11, $12)`,
			id, src.ID, origin, sum[:], len(in.Content), in.MediaType, cleanFilename(in.Filename), obj.ID, evidenceID, supersedes, up, now)
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return duplicateImport("")
		}
		return err
	})
	if err != nil {
		return Import{}, err
	}
	return s.ImportByID(ctx, id)
}

func duplicateImport(existing string) error {
	e := errs.New(errs.Conflict, "DUPLICATE_IMPORT", "This exact file was already imported.")
	if existing != "" {
		e.Details = append(e.Details, errs.Detail{Field: "existing_import_id", Code: existing})
	}
	return e
}

const importCols = `i.id::text, i.source_id::text, s.code, s.source_kind, i.origin, i.content_sha256, i.size_bytes, i.media_type, i.original_filename,
	i.stored_object_id::text, i.evidence_record_id::text, i.statement_ref, i.period_start, i.period_end, i.currency,
	i.opening_balance_minor::text, i.closing_balance_minor::text, i.account_ref_hint, i.declared_lines, i.lines_parsed, i.lines_duplicate,
	i.lines_conflicting, i.batches_parsed, i.status, i.failure_code, i.validation_report, i.supersedes_import_id::text, i.environment,
	i.uploaded_by::text, i.received_at, i.parsed_at`

func scanImport(r pgx.Row) (Import, error) {
	var x Import
	var sum []byte
	var report []byte
	err := r.Scan(&x.ID, &x.SourceID, &x.SourceCode, &x.SourceKind, &x.Origin, &sum, &x.SizeBytes, &x.MediaType, &x.OriginalFilename,
		&x.StoredObjectID, &x.EvidenceRecordID, &x.StatementRef, &x.PeriodStart, &x.PeriodEnd, &x.Currency, &x.OpeningBalance, &x.ClosingBalance,
		&x.AccountRefHint, &x.DeclaredLines, &x.LinesParsed, &x.LinesDuplicate, &x.LinesConflicting, &x.BatchesParsed, &x.Status, &x.FailureCode,
		&report, &x.SupersedesImportID, &x.Environment, &x.UploadedBy, &x.ReceivedAt, &x.ParsedAt)
	if err != nil {
		return x, err
	}
	x.ContentSHA256 = hex.EncodeToString(sum)
	x.ValidationReport = []ValidationError{}
	_ = json.Unmarshal(report, &x.ValidationReport)
	x.Label = "TEST_ONLY"
	return x, nil
}

// ImportByID returns one import.
func (s *Service) ImportByID(ctx context.Context, id string) (Import, error) {
	if !ids.Valid(id) {
		return Import{}, ErrNotFound
	}
	x, err := scanImport(s.Pool.QueryRow(ctx, `SELECT `+importCols+` FROM recon.imports i JOIN recon.sources s ON s.id = i.source_id WHERE i.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Import{}, ErrNotFound
	}
	return x, err
}

// ImportFilter filters the import list.
type ImportFilter struct {
	Source, Status, Currency string
	Limit                    int
}

// Imports lists imports, newest first.
func (s *Service) Imports(ctx context.Context, f ImportFilter) ([]Import, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+importCols+` FROM recon.imports i JOIN recon.sources s ON s.id = i.source_id
		WHERE ($1 = '' OR s.code = $1) AND ($2 = '' OR i.status = $2) AND ($3 = '' OR i.currency = $3)
		ORDER BY i.received_at DESC, i.id DESC LIMIT $4`, f.Source, f.Status, f.Currency, f.Limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Import, error) { return scanImport(r) })
}

// ---- parsing --------------------------------------------------------------------------------------------------------

// ParsePending parses every import whose file is scanned (the outbox consumer's work, and the sweeper's: imports stuck
// RECEIVED or PARSING after a crash). Returns how many imports reached a final state.
func (s *Service) ParsePending(ctx context.Context, limit int) (int, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id::text FROM recon.imports WHERE status = 'RECEIVED'
		OR (status = 'PARSING' AND updated_at < $1) ORDER BY received_at LIMIT $2`, s.now().Add(-5*time.Minute), limit)
	if err != nil {
		return 0, err
	}
	list, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	n := 0
	for _, id := range list {
		done, err := s.ParseImport(ctx, id)
		if err != nil {
			s.Logger.WarnContext(ctx, "reconciliation: parse failed (will retry)", "import_id", id, "err", err)
			continue
		}
		if done {
			n++
		}
	}
	return n, nil
}

// HandleObjectScanned is the outbox handler for storage.object_scanned: a reconciliation statement's scan finished.
func (s *Service) HandleObjectScanned(ctx context.Context, d outbox.Delivery) error {
	var p struct {
		ObjectID string `json:"object_id"`
		Owner    string `json:"owner_module"`
		Purpose  string `json:"purpose"`
	}
	if err := json.Unmarshal(d.Payload, &p); err != nil || p.Owner != "reconciliation" || p.Purpose != "RECONCILIATION_REPORT" {
		return nil
	}
	var id string
	err := s.Pool.QueryRow(ctx, `SELECT id::text FROM recon.imports WHERE stored_object_id = $1`, p.ObjectID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // the import row was never committed (duplicate race): the object stays as orphaned evidence
	}
	if err != nil {
		return err
	}
	_, err = s.ParseImport(ctx, id)
	return err
}

// ParseImport parses one import once its file is CLEAN. It reports whether the import reached PARSED or FAILED.
func (s *Service) ParseImport(ctx context.Context, id string) (bool, error) {
	imp, err := s.ImportByID(ctx, id)
	if err != nil {
		return false, err
	}
	if imp.Status != "RECEIVED" && imp.Status != "PARSING" {
		return true, nil
	}
	obj, err := s.Storage.Get(ctx, imp.StoredObjectID)
	if err != nil {
		return false, err
	}
	switch obj.Status {
	case storage.StatusRejected:
		return true, s.failImport(ctx, imp, "SCAN_REJECTED", []ValidationError{{Locator: "file", Code: "SCAN_" + obj.RejectReason}})
	case storage.StatusClean:
	default:
		return false, nil // still in quarantine or being scanned: the scan event (or the sweeper) brings us back
	}
	if imp.Status == "RECEIVED" {
		tag, err := s.Pool.Exec(ctx, `UPDATE recon.imports SET status = 'PARSING', version = version + 1 WHERE id = $1 AND status = 'RECEIVED'`, id)
		if err != nil {
			return false, err
		}
		if tag.RowsAffected() == 0 {
			return false, nil // another worker took it
		}
	}
	rc, _, err := s.Storage.Open(ctx, imp.StoredObjectID)
	if err != nil {
		return false, err
	}
	content, rerr := io.ReadAll(io.LimitReader(rc, s.maxBytes()+1))
	cerr := rc.Close()
	if rerr != nil {
		return false, rerr
	}
	if cerr != nil {
		return false, cerr // includes storage.ErrIntegrity: the stored content no longer matches its hash
	}
	if got := sha256.Sum256(content); hex.EncodeToString(got[:]) != imp.ContentSHA256 {
		return true, s.failImport(ctx, imp, "EVIDENCE_HASH_MISMATCH", nil)
	}
	st, perr := ParseStatement(content, imp.MediaType, imp.SourceKind, s.now())
	var fe *FormatError
	if errors.As(perr, &fe) {
		return true, s.failImport(ctx, imp, fe.Code, fe.Errors)
	}
	if perr != nil {
		return false, perr
	}
	return true, s.record(ctx, imp, st)
}

func (s *Service) failImport(ctx context.Context, imp Import, code string, report []ValidationError) error {
	if report == nil {
		report = []ValidationError{}
	}
	if len(report) > maxErrors {
		report = report[:maxErrors]
	}
	raw, _ := json.Marshal(report)
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE recon.imports SET status = 'FAILED', failure_code = $2, validation_report = $3, version = version + 1
			WHERE id = $1 AND status IN ('RECEIVED','PARSING')`, imp.ID, code, raw)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{Action: "reconciliation.import.failed", ActorType: "system", TargetType: "reconciliation_import",
			TargetID: imp.ID, Outcome: "failed", Reason: code, Metadata: map[string]any{"errors": len(report), "source": imp.SourceCode}}); err != nil {
			return err
		}
		_, err = outbox.Write(ctx, tx, outbox.Event{AggregateType: "reconciliation_import", AggregateID: imp.ID, EventType: EventImportFailed,
			OccurredAt: s.now(), Payload: map[string]any{"import_id": imp.ID, "failure_code": code, "source": imp.SourceCode}})
		return err
	})
}

// record writes a parsed statement in one transaction: batches, lines (duplicates skipped, conflicting revisions flagged),
// supersession of the earlier import, and the import's metadata. Nothing is written if anything fails.
func (s *Service) record(ctx context.Context, imp Import, st Statement) error {
	var hint *string
	var hmac []byte
	if st.AccountRef != "" {
		if s.Keyed == nil {
			return s.failImport(ctx, imp, "KEYED_HASH_UNAVAILABLE", nil)
		}
		norm := strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(st.AccountRef, " ", ""), "-", ""))
		h := norm
		if len(h) > 4 {
			h = h[len(h)-4:]
		}
		hint, hmac = &h, s.Keyed.Sum("reconciliation.bank_account", norm)
	}
	var failCode string
	type conflict struct{ id, currency string }
	var conflicts []conflict
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM recon.imports WHERE id = $1 FOR UPDATE`, imp.ID).Scan(&status); err != nil {
			return err
		}
		if status != "PARSING" {
			return nil // a concurrent parser finished it
		}
		superseded := ""
		if imp.SupersedesImportID != nil {
			superseded = *imp.SupersedesImportID
		}
		// batches: an identical header already imported (overlapping reports) is reused; a different one is a conflict
		batchIDs := map[string]string{}
		for _, b := range st.Batches {
			var oldID string
			var cr, dr, fe, net int64
			err := tx.QueryRow(ctx, `SELECT b.id::text, b.gross_credits_minor, b.gross_debits_minor, b.fees_minor, b.net_minor
				FROM recon.settlement_batches b JOIN recon.imports i ON i.id = b.import_id
				WHERE b.source_id = $1 AND b.batch_ref = $2 AND b.status <> 'SUPERSEDED' AND i.id <> $3::uuid
				AND ($4 = '' OR i.id <> $4::uuid) ORDER BY b.created_at LIMIT 1`, imp.SourceID, b.Ref, imp.ID, superseded).Scan(&oldID, &cr, &dr, &fe, &net)
			switch {
			case err == nil && cr == b.Credits && dr == b.Debits && fe == b.Fees && net == b.Net:
				batchIDs[b.Ref] = oldID
				continue
			case err == nil:
				failCode = "CONFLICTING_BATCH"
				return errRollback
			case !errors.Is(err, pgx.ErrNoRows):
				return err
			}
			bid := ids.New()
			if _, err := tx.Exec(ctx, `INSERT INTO recon.settlement_batches (id, import_id, source_id, provider_code, currency, batch_ref,
				settlement_date, gross_credits_minor, gross_debits_minor, fees_minor, net_minor, declared_lines, status)
				SELECT $1, $2, $3, provider_code, $4, $5, $6, $7, $8, $9, $10, $11, 'RECEIVED' FROM recon.sources WHERE id = $3`,
				bid, imp.ID, imp.SourceID, b.Currency, b.Ref, b.SettlementDate, b.Credits, b.Debits, b.Fees, b.Net, b.DeclaredLines); err != nil {
				return err
			}
			batchIDs[b.Ref] = bid
		}
		// existing lines with the same identity (latest revision per line id)
		lineIDs := make([]string, 0, len(st.Lines))
		for _, l := range st.Lines {
			lineIDs = append(lineIDs, l.ID)
		}
		// every recorded revision of the same line ids: the latest revision decides the next number and supersession; any
		// revision with the same content makes the line a duplicate (evidence delivered again)
		type prev struct {
			rev     int
			imports map[string]bool
			hashes  [][]byte
		}
		existing := map[string]prev{}
		rows, err := tx.Query(ctx, `SELECT external_line_id, revision, raw_line_sha256, import_id::text FROM recon.items
			WHERE source_id = $1 AND external_line_id = ANY ($2::text[]) ORDER BY external_line_id, revision`, imp.SourceID, lineIDs)
		if err != nil {
			return err
		}
		for rows.Next() {
			var k, importID string
			var rev int
			var h []byte
			if err := rows.Scan(&k, &rev, &h, &importID); err != nil {
				rows.Close()
				return err
			}
			p := existing[k]
			if p.imports == nil {
				p.imports = map[string]bool{}
			}
			p.rev, p.imports[importID], p.hashes = rev, true, append(p.hashes, h)
			existing[k] = p
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		var dup, conflicting int
		copyRows := make([][]any, 0, len(st.Lines))
		lineNo := 0
		for _, l := range st.Lines {
			rev, matchStatus := 1, "UNMATCHED"
			if p, ok := existing[l.ID]; ok {
				switch {
				case superseded != "" && p.imports[superseded]:
					rev = p.rev + 1 // a corrected re-issue: the new version of the line
				case containsHash(p.hashes, l.RawHash[:]):
					dup++ // the same evidence delivered again (overlapping statements): recorded once
					continue
				default:
					rev, matchStatus = p.rev+1, "CONFLICT" // different content for a line already recorded, without a re-issue
					conflicting++
				}
			}
			lineNo++
			var batchID any
			if l.BatchRef != "" {
				batchID = batchIDs[l.BatchRef]
			}
			itemID := ids.New()
			if matchStatus == "CONFLICT" {
				conflicts = append(conflicts, conflict{itemID, l.Currency})
			}
			copyRows = append(copyRows, []any{itemID, imp.ID, imp.SourceID, l.ID, rev, lineNo, l.Kind, l.Direction, l.Status, l.Amount, l.Fee,
				l.Net, l.Currency, l.RawCurrency, nullStr(l.ProviderRef), nullStr(l.MerchantRef), nullStr(l.BatchRef), batchID,
				nullStr(l.CounterpartyRef), l.ValueDate, l.BookedAt, l.RawHash[:], l.Locator, matchStatus})
		}
		if len(copyRows) > 0 {
			if _, err := tx.CopyFrom(ctx, pgx.Identifier{"recon", "items"}, []string{"id", "import_id", "source_id", "external_line_id", "revision",
				"line_number", "item_kind", "direction", "line_status", "amount_minor", "fee_minor", "net_minor", "currency", "raw_currency_code",
				"provider_ref", "merchant_ref", "batch_ref", "settlement_batch_id", "counterparty_ref", "value_date", "booked_at", "raw_line_sha256",
				"raw_line_locator", "match_status"}, pgx.CopyFromRows(copyRows)); err != nil {
				return err
			}
		}
		for _, c := range conflicts {
			if _, err := s.openException(ctx, tx, "", ExceptionDraft{Type: "CONFLICTING_EVIDENCE", Severity: SevHigh, ItemID: c.id,
				DedupeKey: itemKey("CONFLICTING_EVIDENCE", c.id), Detail: map[string]any{"import_id": imp.ID,
					"note": "a line already recorded came back with different content and no re-issue superseding the earlier statement"}},
				imp.SourceID, c.currency); err != nil {
				return err
			}
		}
		if superseded != "" {
			if err := s.supersede(ctx, tx, superseded, imp.ID); err != nil {
				return err
			}
		}
		ccy := nullStr(st.Currency)
		_, err = tx.Exec(ctx, `UPDATE recon.imports SET statement_ref = $2, period_start = $3, period_end = $4, currency = $5,
			opening_balance_minor = $6, closing_balance_minor = $7, account_ref_hint = $8, account_ref_hmac = $9, declared_lines = $10,
			lines_parsed = $11, lines_duplicate = $12, lines_conflicting = $13, batches_parsed = $14, parsed_at = $15, version = version + 1
			WHERE id = $1`, imp.ID, st.Ref, st.PeriodStart, st.PeriodEnd, ccy, st.Opening, st.Closing, hint, hmac, st.DeclaredLines,
			lineNo-conflicting, dup, conflicting, len(st.Batches), s.now())
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE recon.imports SET status = 'PARSED', version = version + 1 WHERE id = $1`, imp.ID)
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			failCode = "DUPLICATE_STATEMENT"
			return errRollback
		}
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "reconciliation.import.parsed", ActorType: "system", TargetType: "reconciliation_import",
			TargetID: imp.ID, Metadata: map[string]any{"lines": lineNo - conflicting, "duplicates": dup, "conflicting": conflicting,
				"batches": len(st.Batches), "statement_ref": st.Ref, "supersedes_import_id": imp.SupersedesImportID}})
	})
	if errors.Is(err, errRollback) {
		err = nil
	}
	if err != nil {
		return err
	}
	if failCode != "" {
		return s.failImport(ctx, imp, failCode, []ValidationError{{Locator: "file", Code: failCode}})
	}
	return nil
}

var errRollback = errors.New("reconciliation: rollback")

// supersede retires an earlier import after its corrected re-issue was recorded: its lines and batches are SUPERSEDED,
// their active matches revoked (never deleted) so the next run decides the new lines.
func (s *Service) supersede(ctx context.Context, tx pgx.Tx, oldID, newID string) error {
	if _, err := tx.Exec(ctx, `UPDATE recon.matches SET status = 'REVOKED', revoked_at = $3, revoke_reason = $2
		WHERE status = 'ACTIVE' AND item_id IN (SELECT id FROM recon.items WHERE import_id = $1)`, oldID,
		"statement superseded by import "+newID, s.now()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE recon.items SET match_status = 'SUPERSEDED' WHERE import_id = $1 AND match_status <> 'SUPERSEDED'`, oldID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE recon.settlement_batches SET status = 'SUPERSEDED', version = version + 1 WHERE import_id = $1
		AND status <> 'SUPERSEDED'`, oldID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE recon.imports SET status = 'SUPERSEDED', version = version + 1 WHERE id = $1 AND status = 'PARSED'`, oldID)
	return err
}

func nullStr(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// ---- sandbox statements ------------------------------------------------------------------------------------------------

// SandboxInput is POST /admin/reconciliation/imports/sandbox: build a TEST statement from the simulator's own records.
type SandboxInput struct {
	SourceCode string    `json:"source"`
	Currency   string    `json:"currency"`
	From       time.Time `json:"period_start"`
	To         time.Time `json:"period_end"`
}

// GenerateSandboxStatement writes the simulator's view of a period as a canonical JSON statement and imports it like
// any other file (hashed, stored, scanned, parsed). TEST only; refused where the sandbox is not available.
func (s *Service) GenerateSandboxStatement(ctx context.Context, staffID string, in SandboxInput) (Import, error) {
	if s.Sandbox == nil || s.Production {
		return Import{}, ErrSandboxOff
	}
	src, err := s.sourceByCode(ctx, in.SourceCode)
	if err != nil {
		return Import{}, err
	}
	if src.Kind == KindBankStatement || src.ProviderCode == nil || *src.ProviderCode != "sandbox" {
		return Import{}, httpx.Validation(errs.Detail{Field: "source", Code: "NOT_A_SANDBOX_SOURCE"})
	}
	if !SupportedCurrencies[in.Currency] {
		return Import{}, httpx.Validation(errs.Detail{Field: "currency", Code: "UNSUPPORTED_CURRENCY"})
	}
	from, to := in.From.UTC().Truncate(time.Second), in.To.UTC().Truncate(time.Second)
	if !to.After(from) || to.Sub(from) > MaxPeriod || to.After(s.now().Add(time.Minute)) {
		return Import{}, httpx.Validation(errs.Detail{Field: "period_end", Code: "OUT_OF_RANGE"})
	}
	lines, err := s.Sandbox.StatementLines(ctx, in.Currency, from, to, 10_000)
	if err != nil {
		return Import{}, err
	}
	doc := map[string]any{"format": FormatCanonicalV1}
	var out []map[string]string
	var batchCredits int64
	batchRef := "SBX-" + from.Format("20060102T150405") + "-" + to.Format("20060102T150405")
	for _, l := range lines {
		if src.Kind == KindSettlementReport && (l.Kind != "PAYMENT" || l.Status != "SUCCEEDED") {
			continue // a settlement report lists settled collections only
		}
		dir := "CREDIT"
		if l.Kind == "PAYOUT" {
			dir = "DEBIT"
		}
		row := map[string]string{"ref": l.ProviderRef + ":" + strings.ToLower(l.Status), "kind": l.Kind, "direction": dir, "status": l.Status,
			"amount_minor": fmt.Sprint(l.AmountMinor), "currency": l.Currency, "provider_ref": l.ProviderRef, "merchant_ref": l.OurReference,
			"booked_at": l.BookedAt.UTC().Format(time.RFC3339Nano)}
		if src.Kind == KindSettlementReport {
			row["batch_ref"] = batchRef
			batchCredits += l.AmountMinor
		}
		out = append(out, row)
	}
	stmt := map[string]string{"ref": fmt.Sprintf("SBX-%s-%s-%d", strings.ToUpper(src.Code[:3]), in.Currency, s.now().UnixMilli()),
		"currency": in.Currency, "period_start": from.Format(time.RFC3339), "period_end": to.Format(time.RFC3339),
		"declared_lines": fmt.Sprint(len(out))}
	doc["statement"] = stmt
	if src.Kind == KindSettlementReport && len(out) > 0 {
		doc["batches"] = []map[string]string{{"ref": batchRef, "currency": in.Currency, "value_date": to.Format("2006-01-02"),
			"gross_credits_minor": fmt.Sprint(batchCredits), "gross_debits_minor": "0", "fees_minor": "0", "net_minor": fmt.Sprint(batchCredits),
			"declared_lines": fmt.Sprint(len(out))}}
	}
	if out == nil {
		out = []map[string]string{}
	}
	doc["lines"] = out
	raw, err := json.MarshalIndent(doc, "", " ")
	if err != nil {
		return Import{}, err
	}
	return s.receive(ctx, "SANDBOX_GENERATOR", staffID, UploadInput{SourceCode: src.Code, MediaType: storage.TypeJSON,
		Filename: "sandbox-" + src.Code + ".json", Content: raw})
}

func containsHash(list [][]byte, h []byte) bool {
	for _, x := range list {
		if bytes.Equal(x, h) {
			return true
		}
	}
	return false
}
