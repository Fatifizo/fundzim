package notifications

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Template is a template with its versions (staff view).
type Template struct {
	ID               string            `json:"id"`
	Key              string            `json:"key"`
	Category         string            `json:"category"`
	Sensitive        bool              `json:"sensitive"`
	AllowedVariables []string          `json:"allowed_variables"`
	Origin           string            `json:"origin"`
	ActiveVersion    *int              `json:"active_version"`
	PendingStatus    *string           `json:"pending_status"`
	Versions         []TemplateVersion `json:"versions,omitempty"`
}

// TemplateVersion is one immutable (after submission) version.
type TemplateVersion struct {
	ID               string     `json:"id"`
	VersionNo        int        `json:"version_no"`
	Locale           string     `json:"locale"`
	Status           string     `json:"status"`
	EmailSubject     *string    `json:"email_subject"`
	EmailText        *string    `json:"email_text"`
	SMSText          *string    `json:"sms_text"`
	WhatsAppName     *string    `json:"whatsapp_template_name"`
	WhatsAppLanguage *string    `json:"whatsapp_language"`
	WhatsAppStatus   string     `json:"whatsapp_provider_status"`
	InAppTitle       string     `json:"inapp_title"`
	InAppBody        string     `json:"inapp_body"`
	ContentHash      string     `json:"content_hash"`
	ApprovalSource   string     `json:"approval_source"`
	CreatedBy        *string    `json:"created_by"`
	SubmittedBy      *string    `json:"submitted_by"`
	ApprovedBy       *string    `json:"approved_by"`
	DecisionReason   *string    `json:"decision_reason"`
	ActivatedAt      *time.Time `json:"activated_at"`
	SMSSegments      *int       `json:"sms_segments"`
	Version          int        `json:"version"`
	CreatedAt        time.Time  `json:"created_at"`
}

const versionCols = `id::text, version_no, locale, status, email_subject, email_text, sms_text, whatsapp_template_name, whatsapp_language,
	whatsapp_provider_status, inapp_title, inapp_body, encode(content_hash, 'hex'), approval_source, created_by::text, submitted_by::text,
	approved_by::text, decision_reason, activated_at, version, created_at`

func scanVersion(r pgx.Row) (TemplateVersion, error) {
	var v TemplateVersion
	err := r.Scan(&v.ID, &v.VersionNo, &v.Locale, &v.Status, &v.EmailSubject, &v.EmailText, &v.SMSText, &v.WhatsAppName, &v.WhatsAppLanguage,
		&v.WhatsAppStatus, &v.InAppTitle, &v.InAppBody, &v.ContentHash, &v.ApprovalSource, &v.CreatedBy, &v.SubmittedBy, &v.ApprovedBy,
		&v.DecisionReason, &v.ActivatedAt, &v.Version, &v.CreatedAt)
	if v.SMSText != nil {
		n := EstimateSMS(*v.SMSText).Segments
		v.SMSSegments = &n
	}
	return v, err
}

// Templates lists templates with their active version number and any pending version.
func (s *Service) Templates(ctx context.Context, category string) ([]Template, error) {
	rows, err := s.Pool.Query(ctx, `SELECT t.id::text, t.template_key, t.category, t.sensitive, t.allowed_variables, t.origin,
		(SELECT version_no FROM app.notification_template_versions v WHERE v.template_id = t.id AND v.status = 'ACTIVE' AND v.locale = 'en'),
		(SELECT status FROM app.notification_template_versions v WHERE v.template_id = t.id AND v.status IN ('DRAFT','PENDING_APPROVAL','APPROVED') LIMIT 1)
		FROM app.notification_templates t WHERE ($1 = '' OR t.category = $1) ORDER BY t.category, t.template_key`, category)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Template, error) {
		var t Template
		err := r.Scan(&t.ID, &t.Key, &t.Category, &t.Sensitive, &t.AllowedVariables, &t.Origin, &t.ActiveVersion, &t.PendingStatus)
		return t, err
	})
}

// TemplateDetail returns one template with every version.
func (s *Service) TemplateDetail(ctx context.Context, id string) (Template, error) {
	if !ids.Valid(id) {
		return Template{}, ErrNotFound
	}
	var t Template
	err := s.Pool.QueryRow(ctx, `SELECT id::text, template_key, category, sensitive, allowed_variables, origin FROM app.notification_templates WHERE id = $1`, id).
		Scan(&t.ID, &t.Key, &t.Category, &t.Sensitive, &t.AllowedVariables, &t.Origin)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	if err != nil {
		return t, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+versionCols+` FROM app.notification_template_versions WHERE template_id = $1 ORDER BY version_no DESC`, id)
	if err != nil {
		return t, err
	}
	t.Versions, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (TemplateVersion, error) { return scanVersion(r) })
	for _, v := range t.Versions {
		if v.Status == "ACTIVE" {
			n := v.VersionNo
			t.ActiveVersion = &n
		}
	}
	return t, err
}

// DraftInput is a new or edited draft's content.
type DraftInput struct {
	EmailSubject     *string `json:"email_subject"`
	EmailText        *string `json:"email_text"`
	SMSText          *string `json:"sms_text"`
	WhatsAppName     *string `json:"whatsapp_template_name"`
	WhatsAppLanguage *string `json:"whatsapp_language"`
	InAppTitle       string  `json:"inapp_title"`
	InAppBody        string  `json:"inapp_body"`
}

func (d DraftInput) content() Content {
	trim := func(p *string) *string {
		if p == nil {
			return nil
		}
		v := strings.TrimSpace(*p)
		if v == "" {
			return nil
		}
		return &v
	}
	return Content{EmailSubject: trim(d.EmailSubject), EmailText: trim(d.EmailText), SMSText: trim(d.SMSText), WhatsAppName: trim(d.WhatsAppName),
		WhatsAppLanguage: trim(d.WhatsAppLanguage), InAppTitle: strings.TrimSpace(d.InAppTitle), InAppBody: strings.TrimSpace(d.InAppBody)}
}

// TemplateOp is a staff action on a template version.
type TemplateOp struct {
	StaffID, TemplateID, VersionID, Reason string
	ExpectedVersion                        int
}

func mapPg(err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && (pe.Code == "23514" || pe.Code == "23505") {
		return ErrState
	}
	return err
}

// CreateDraft creates a DRAFT version (maker). One open version per template.
func (s *Service) CreateDraft(ctx context.Context, op TemplateOp, in DraftInput) (TemplateVersion, error) {
	c := in.content()
	t, err := s.TemplateDetail(ctx, op.TemplateID)
	if err != nil {
		return TemplateVersion{}, err
	}
	if c.InAppTitle == "" || c.InAppBody == "" || ((c.EmailSubject == nil) != (c.EmailText == nil)) || ((c.WhatsAppName == nil) != (c.WhatsAppLanguage == nil)) {
		return TemplateVersion{}, ErrInvalid
	}
	if err := ValidateContent(c, append(t.AllowedVariables, "display_name")); err != nil {
		return TemplateVersion{}, ErrInvalid
	}
	id := ids.New()
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var next int
		if err := tx.QueryRow(ctx, `SELECT coalesce(max(version_no), 0) + 1 FROM app.notification_template_versions WHERE template_id = $1`, op.TemplateID).
			Scan(&next); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO app.notification_template_versions (id, template_id, version_no, locale, status, email_subject, email_text,
			sms_text, whatsapp_template_name, whatsapp_language, inapp_title, inapp_body, content_hash, approval_source, created_by)
			VALUES ($1,$2,$3,'en','DRAFT',$4,$5,$6,$7,$8,$9,$10,$11,'MAKER_CHECKER',$12)`, id, op.TemplateID, next, c.EmailSubject, c.EmailText, c.SMSText,
			c.WhatsAppName, c.WhatsAppLanguage, c.InAppTitle, c.InAppBody, ContentHash(c), op.StaffID); err != nil {
			return mapPg(err)
		}
		return audit.Record(ctx, tx, audit.Event{Action: "notifications.template.drafted", ActorType: "staff", ActorID: op.StaffID,
			TargetType: "notification_template_version", TargetID: id, Metadata: map[string]any{"template": t.Key, "version_no": next}})
	})
	if err != nil {
		return TemplateVersion{}, err
	}
	return s.version(ctx, id)
}

func (s *Service) version(ctx context.Context, id string) (TemplateVersion, error) {
	v, err := scanVersion(s.Pool.QueryRow(ctx, `SELECT `+versionCols+` FROM app.notification_template_versions WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}

type versionRow struct {
	templateID, status, key string
	createdBy, submittedBy  *string
	version                 int
}

func lockVersion(ctx context.Context, tx pgx.Tx, id string) (versionRow, error) {
	var r versionRow
	if !ids.Valid(id) {
		return r, ErrNotFound
	}
	err := tx.QueryRow(ctx, `SELECT v.template_id::text, v.status, t.template_key, v.created_by::text, v.submitted_by::text, v.version
		FROM app.notification_template_versions v JOIN app.notification_templates t ON t.id = v.template_id WHERE v.id = $1 FOR UPDATE OF v`, id).
		Scan(&r.templateID, &r.status, &r.key, &r.createdBy, &r.submittedBy, &r.version)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// TemplateAction applies submit (maker), approve / reject / activate / retire (checker) to a version.
func (s *Service) TemplateAction(ctx context.Context, op TemplateOp, action string, isLinked func(a, b string) bool) (TemplateVersion, error) {
	reason := strings.TrimSpace(op.Reason)
	if (action == "approve" || action == "reject" || action == "retire") && (len([]rune(reason)) < 5 || len([]rune(reason)) > 2000) {
		return TemplateVersion{}, ErrInvalid
	}
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		r, err := lockVersion(ctx, tx, op.VersionID)
		if err != nil {
			return err
		}
		if op.ExpectedVersion > 0 && r.version != op.ExpectedVersion {
			return ErrStale
		}
		maker := func() bool {
			for _, m := range []*string{r.createdBy, r.submittedBy} {
				if m != nil && (*m == op.StaffID || (isLinked != nil && isLinked(*m, op.StaffID))) {
					return true
				}
			}
			return false
		}
		now := s.now()
		switch action {
		case "submit":
			if r.status != "DRAFT" {
				return ErrState
			}
			_, err = tx.Exec(ctx, `UPDATE app.notification_template_versions SET status = 'PENDING_APPROVAL', submitted_by = $2, submitted_at = $3,
				version = version + 1 WHERE id = $1`, op.VersionID, op.StaffID, now)
		case "approve", "reject":
			if r.status != "PENDING_APPROVAL" {
				return ErrState
			}
			if maker() {
				return ErrSelfApproval
			}
			if action == "approve" {
				_, err = tx.Exec(ctx, `UPDATE app.notification_template_versions SET status = 'APPROVED', approved_by = $2, approved_at = $3, decision_reason = $4,
					version = version + 1 WHERE id = $1`, op.VersionID, op.StaffID, now, reason)
			} else {
				_, err = tx.Exec(ctx, `UPDATE app.notification_template_versions SET status = 'RETIRED', retired_at = $2, decision_reason = $3,
					version = version + 1 WHERE id = $1`, op.VersionID, now, reason)
			}
		case "activate":
			if r.status != "APPROVED" {
				return ErrState
			}
			if maker() {
				return ErrSelfApproval
			}
			// one ACTIVE version: the previous one is retired in the same transaction (notifications already created keep theirs)
			if _, err := tx.Exec(ctx, `UPDATE app.notification_template_versions SET status = 'RETIRED', retired_at = $2, version = version + 1
				WHERE template_id = $1 AND locale = 'en' AND status = 'ACTIVE'`, r.templateID, now); err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE app.notification_template_versions SET status = 'ACTIVE', activated_at = $2, version = version + 1 WHERE id = $1`,
				op.VersionID, now)
		case "retire":
			if r.status != "DRAFT" && r.status != "APPROVED" {
				return ErrState // an ACTIVE version is replaced by activating another, never left without one
			}
			_, err = tx.Exec(ctx, `UPDATE app.notification_template_versions SET status = 'RETIRED', retired_at = $2, decision_reason = $3, version = version + 1
				WHERE id = $1`, op.VersionID, now, nullStr(reason))
		default:
			return ErrInvalid
		}
		if err != nil {
			return mapPg(err)
		}
		return audit.Record(ctx, tx, audit.Event{Action: "notifications.template." + action, ActorType: "staff", ActorID: op.StaffID,
			TargetType: "notification_template_version", TargetID: op.VersionID, Reason: reason, Metadata: map[string]any{"template": r.key}})
	})
	if err != nil {
		return TemplateVersion{}, err
	}
	return s.version(ctx, op.VersionID)
}

// UpdateDraft replaces a DRAFT's content (the maker only).
func (s *Service) UpdateDraft(ctx context.Context, op TemplateOp, in DraftInput) (TemplateVersion, error) {
	c := in.content()
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		r, err := lockVersion(ctx, tx, op.VersionID)
		if err != nil {
			return err
		}
		if r.status != "DRAFT" {
			return ErrState
		}
		if r.createdBy == nil || *r.createdBy != op.StaffID {
			return ErrSelfApproval
		}
		if op.ExpectedVersion > 0 && r.version != op.ExpectedVersion {
			return ErrStale
		}
		var allowed []string
		if err := tx.QueryRow(ctx, `SELECT allowed_variables FROM app.notification_templates WHERE id = $1`, r.templateID).Scan(&allowed); err != nil {
			return err
		}
		if c.InAppTitle == "" || c.InAppBody == "" || ValidateContent(c, append(allowed, "display_name")) != nil {
			return ErrInvalid
		}
		_, err = tx.Exec(ctx, `UPDATE app.notification_template_versions SET email_subject = $2, email_text = $3, sms_text = $4, whatsapp_template_name = $5,
			whatsapp_language = $6, inapp_title = $7, inapp_body = $8, content_hash = $9, version = version + 1 WHERE id = $1`, op.VersionID, c.EmailSubject,
			c.EmailText, c.SMSText, c.WhatsAppName, c.WhatsAppLanguage, c.InAppTitle, c.InAppBody, ContentHash(c))
		if err != nil {
			return mapPg(err)
		}
		return audit.Record(ctx, tx, audit.Event{Action: "notifications.template.draft_updated", ActorType: "staff", ActorID: op.StaffID,
			TargetType: "notification_template_version", TargetID: op.VersionID})
	})
	if err != nil {
		return TemplateVersion{}, err
	}
	return s.version(ctx, op.VersionID)
}

// SubmitWhatsAppToSimulator records the TEST simulator's template review. The simulator accepts a template unless its
// name contains "reject"; acceptance is SIMULATOR_ACCEPTED — never APPROVED, which needs a real provider's evidence.
func (s *Service) SubmitWhatsAppToSimulator(ctx context.Context, op TemplateOp) (TemplateVersion, error) {
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var name *string
		var status string
		err := tx.QueryRow(ctx, `SELECT whatsapp_template_name, whatsapp_provider_status FROM app.notification_template_versions WHERE id = $1 FOR UPDATE`,
			op.VersionID).Scan(&name, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if name == nil || status == "SIMULATOR_ACCEPTED" || status == "APPROVED" {
			return ErrState
		}
		result := "SIMULATOR_ACCEPTED"
		if strings.Contains(strings.ToLower(*name), "reject") {
			result = "SIMULATOR_REJECTED"
		}
		if _, err := tx.Exec(ctx, `UPDATE app.notification_template_versions SET whatsapp_provider_status = $2, version = version + 1 WHERE id = $1`,
			op.VersionID, result); err != nil {
			return mapPg(err)
		}
		return audit.Record(ctx, tx, audit.Event{Action: "notifications.template.whatsapp_simulated", ActorType: "staff", ActorID: op.StaffID,
			TargetType: "notification_template_version", TargetID: op.VersionID, Metadata: map[string]any{"result": result}})
	})
	if err != nil {
		return TemplateVersion{}, err
	}
	return s.version(ctx, op.VersionID)
}
