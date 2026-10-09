//go:build integration

// Stage 6 stream U: campaign updates against the REAL local stack (Postgres, Redis, mail). The API runs
// in-process with the campaigns core, media and updates as wired by internal/app; the updates service is rebuilt on
// the same core with a per-subject restriction override so RESTRICTED / SUSPENDED owners can be exercised without a
// full compliance case. Live campaigns are created through the migrator pool, walking the lifecycle edges with one
// campaign_status_history row per version (the campaign API path needs identity-verified owners and staff review,
// which the core's own tests cover).
package integration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/app"
	"github.com/Fatifizo/fundzim/internal/auth"
	"github.com/Fatifizo/fundzim/internal/campaigns"
	"github.com/Fatifizo/fundzim/internal/campaigns/updates"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// ---- restriction overrides --------------------------------------------------------------------------------

// cuCore is the real campaigns core (as wired by internal/app) with a per-subject restriction override, so RESTRICTED,
// SUSPENDED and OFFBOARDED owners can be exercised without a full compliance case. The real restriction reader
// still runs first (fail closed on error).
type cuCore struct {
	*campaigns.Service
	mu       sync.Mutex
	override map[string]string // subject id -> level
}

func (c *cuCore) Restricted(ctx context.Context, a campaigns.Access) (string, error) {
	lvl, err := c.Service.Restricted(ctx, a)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range []*string{&a.CampaignID, a.OwnerUserID, a.OwnerOrgID} {
		if id != nil {
			if o, ok := c.override[*id]; ok {
				lvl = o
			}
		}
	}
	return lvl, nil
}

func (c *cuCore) set(id, level string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if level == "" {
		delete(c.override, id)
	} else {
		c.override[id] = level
	}
}

// newCUServer is newITServer with the updates service rebuilt on the overridable core (media as wired).
func newCUServer(t *testing.T) (*itServer, *cuCore) {
	t.Helper()
	s := newITServer(t, nil)
	m := s.d.Verification
	if m == nil || m.Campaigns == nil || m.CampaignUpdates == nil {
		t.Fatal("campaigns core or updates not wired in internal/app")
	}
	core := &cuCore{Service: m.Campaigns, override: map[string]string{}}
	deps := updates.Deps{Pool: s.d.DB, Core: core, Orgs: s.d.Orgs, Clock: clock.System, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if m.CampaignMedia != nil {
		deps.Media = m.CampaignMedia
	}
	m.CampaignUpdates = updates.New(deps)
	srv := httptest.NewServer(app.PublicHandler(s.d, app.NewRouter(s.d, s.d.Checker())))
	t.Cleanup(srv.Close)
	s.srv = srv
	return s, core
}

// ---- fixtures -------------------------------------------------------------------------------------------

type cuUser struct {
	*browser
	id, name string
}

// cuOwner registers a personal account with a distinctive display name and signs in.
func cuOwner(t *testing.T, s *itServer, prefix string) cuUser {
	t.Helper()
	email := uniqueEmail(prefix)
	name := "Organiser " + strings.ToUpper(prefix[:1]) + prefix[1:] + " " + ids.New()[30:]
	b := s.browser(t)
	b.register(email, itPassword, name)
	tok := tokenFrom(t, mailTo(t, email, "Confirm your FundZim email"))
	b.expect(b.do("POST", "/auth/verify-email", map[string]string{"token": tok}), 200, "")
	b.mustLogin(email, itPassword)
	var me auth.Me
	_ = json.Unmarshal(b.do("GET", "/me", nil).Data, &me)
	if me.ID == "" {
		t.Fatal("no user id")
	}
	return cuUser{browser: b, id: me.ID, name: name}
}

type cuStaffSet struct{ mod1, mod2 staffCred }

var (
	cuStaffOnce sync.Once
	cuStaff     *cuStaffSet
)

// cuModerators returns two ADMIN staff (ADMIN holds content.moderate), created once per test binary.
func cuModerators(t *testing.T, s *itServer) *cuStaffSet {
	t.Helper()
	f := staffFixture(t, s)
	cuStaffOnce.Do(func() {
		a, b := s.staff(t, f.adminA), s.staff(t, f.adminB)
		cuStaff = &cuStaffSet{mod1: inviteStaff(t, s, a, b, "Update Moderator One", "ADMIN"),
			mod2: inviteStaff(t, s, a, b, "Update Moderator Two", "ADMIN")}
	})
	if cuStaff == nil {
		t.Fatal("moderator fixture setup failed in an earlier test")
	}
	return cuStaff
}

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

func cuPublicCode() string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = crockford[int(b[i])%32]
	}
	return string(b)
}

type cuCampaign struct{ id, slug string }

// cuMakeCampaign inserts a campaign owned by ownerID and walks it DRAFT → SUBMITTED → UNDER_REVIEW → APPROVED →
// ACTIVE (→ SUSPENDED when suspended), one campaign_status_history row per version, in one transaction.
func cuMakeCampaign(t *testing.T, ownerID, category string, suspended bool) cuCampaign {
	t.Helper()
	need(t, "DATABASE_MIGRATION_URL")
	p := pool(t, os.Getenv("DATABASE_MIGRATION_URL"))
	c := cuCampaign{id: ids.New()}
	code := cuPublicCode()
	c.slug = "it-updates-" + strings.ToLower(code)
	title, summary, story := "Integration updates campaign", "A synthetic campaign used by the campaign updates tests.", strings.Repeat("Synthetic story. ", 10)
	err := pgx.BeginFunc(ctx(t), p, func(tx pgx.Tx) error {
		hist := func(v int, from, to string) error {
			var f any
			if from != "" {
				f = from
			}
			_, err := tx.Exec(ctx(t), `INSERT INTO app.campaign_status_history (id, campaign_id, campaign_version, event_type, from_status, to_status,
				actor_type, reason_code) VALUES ($1, $2, $3, 'IT_FIXTURE', $4, $5, 'SYSTEM', 'IT_FIXTURE')`, ids.New(), c.id, v, f, to)
			return err
		}
		if _, err := tx.Exec(ctx(t), `INSERT INTO app.campaigns (id, public_code, slug, owner_user_id, created_by, category_code, title, summary, story,
			goal_amount_minor, goal_currency, status, risk_tier, policy_version)
			SELECT $1, $2, $3, $4, $4, $5, $6, $7, $8, 50000, 'USD', 'DRAFT', k.default_risk_tier, 'campaign-v1'
			FROM app.campaign_categories k WHERE k.code = $5`, c.id, code, c.slug, ownerID, category, title, summary, story); err != nil {
			return err
		}
		if err := hist(1, "", "DRAFT"); err != nil {
			return err
		}
		vid := ids.New()
		sum := sha256.Sum256([]byte(title + summary + story))
		steps := []struct{ to, set string }{
			{"SUBMITTED", "submitted_at = now(), fundraising_basis = 'SELF_FUNDRAISING'"},
			{"UNDER_REVIEW", "updated_at = now()"},
			{"APPROVED", "approved_at = now(), approved_version_id = '" + vid + "'"},
			{"ACTIVE", "published_at = now()"},
		}
		if suspended {
			steps = append(steps, struct{ to, set string }{"SUSPENDED", "suspended_at = now()"})
		}
		from := "DRAFT"
		for i, st := range steps {
			if st.to == "APPROVED" {
				if _, err := tx.Exec(ctx(t), `INSERT INTO app.campaign_versions (id, campaign_id, version_number, change_kind, title, summary, story,
					category_code, goal_amount_minor, goal_currency, content_sha256, created_by)
					VALUES ($1, $2, 1, 'SUBMISSION', $3, $4, $5, $6, 50000, 'USD', $7, $8)`, vid, c.id, title, summary, story, category, sum[:], ownerID); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx(t), `UPDATE app.campaigns SET status = $2, `+st.set+` WHERE id = $1`, c.id, st.to); err != nil {
				return fmt.Errorf("%s: %w", st.to, err)
			}
			if err := hist(i+2, from, st.to); err != nil {
				return err
			}
			from = st.to
		}
		return nil
	})
	if err != nil {
		t.Fatalf("campaign fixture: %v", err)
	}
	return c
}

// cuSuspend moves an ACTIVE campaign to SUSPENDED (with its history row).
func cuSuspend(t *testing.T, campaignID string) {
	t.Helper()
	p := pool(t, os.Getenv("DATABASE_MIGRATION_URL"))
	err := pgx.BeginFunc(ctx(t), p, func(tx pgx.Tx) error {
		var v int
		if err := tx.QueryRow(ctx(t), `UPDATE app.campaigns SET status = 'SUSPENDED', suspended_at = now() WHERE id = $1 RETURNING version`, campaignID).Scan(&v); err != nil {
			return err
		}
		_, err := tx.Exec(ctx(t), `INSERT INTO app.campaign_status_history (id, campaign_id, campaign_version, event_type, from_status, to_status,
			actor_type, reason_code) VALUES ($1, $2, $3, 'IT_FIXTURE', 'ACTIVE', 'SUSPENDED', 'SYSTEM', 'IT_FIXTURE')`, ids.New(), campaignID, v)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

type cuUpdate struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Body         string   `json:"body"`
	Status       string   `json:"status"`
	Version      int      `json:"version"`
	MediaIDs     []string `json:"media_ids"`
	HiddenReason *string  `json:"hidden_reason"`
}

func cuDecode[T any](t *testing.T, r apiResp) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(r.Data, &v); err != nil {
		t.Fatalf("decode %s: %v", r.Data, err)
	}
	return v
}

func cuPost(t *testing.T, u cuUser, campaignID, title, body string, publish bool) apiResp {
	t.Helper()
	return u.do("POST", "/campaigns/"+campaignID+"/updates", map[string]any{"title": title, "body": body, "publish": publish})
}

func cuCreate(t *testing.T, u cuUser, campaignID, title string, publish bool, wantStatus string) cuUpdate {
	t.Helper()
	r := cuPost(t, u, campaignID, title, "Thank you everyone, here is our news for this week.", publish)
	u.expect(r, 201, "")
	up := cuDecode[cuUpdate](t, r)
	if up.Status != wantStatus || r.Header.Get("ETag") != fmt.Sprintf(`"%d"`, up.Version) {
		t.Fatalf("created %s: status %s (want %s), etag %q", title, up.Status, wantStatus, r.Header.Get("ETag"))
	}
	return up
}

type cuPublicList struct {
	Updates []map[string]any `json:"updates"`
	Next    string           `json:"next_cursor"`
}

func cuPublic(t *testing.T, s *itServer, slug, query string) (apiResp, cuPublicList) {
	t.Helper()
	b := s.browser(t) // anonymous
	r := b.do("GET", "/public/campaigns/"+slug+"/updates"+query, nil)
	var l cuPublicList
	if r.Status == 200 {
		l = cuDecode[cuPublicList](t, r)
	}
	return r, l
}

func cuPublicIDs(l cuPublicList) []string {
	var out []string
	for _, u := range l.Updates {
		out = append(out, u["id"].(string))
	}
	return out
}

func cuDecision(code string) map[string]string {
	return map[string]string{"reason_code": code, "note": "integration test moderation decision"}
}

func cuCount(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool(t, os.Getenv("DATABASE_MIGRATION_URL")).QueryRow(ctx(t), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// ===== tests ==============================================================================================

func TestCampaignUpdatesPublishDirectAndPreModerated(t *testing.T) {
	s, restr := newCUServer(t)
	mods := cuModerators(t, s)
	owner := cuOwner(t, s, "cu-pub")
	std := cuMakeCampaign(t, owner.id, "EDUCATION", false) // STANDARD moderation, STANDARD tier
	med := cuMakeCampaign(t, owner.id, "MEDICAL", false)   // PRE_MODERATE_UPDATES

	// direct publication
	pub := cuCreate(t, owner, std.id, "School fees paid", true, "PUBLISHED")
	if n := cuCount(t, `SELECT count(*) FROM audit.audit_events WHERE action = 'campaign.update_published' AND target_id = $1`, pub.ID); n != 1 {
		t.Fatalf("audit campaign.update_published: %d", n)
	}
	if n := cuCount(t, `SELECT count(*) FROM app.outbox_events WHERE event_type = 'campaigns.update_published' AND aggregate_id = $1`, pub.ID); n != 1 {
		t.Fatalf("outbox campaigns.update_published: %d", n)
	}
	if n := cuCount(t, `SELECT count(*) FROM app.campaign_update_events WHERE update_id = $1 AND to_status = 'PUBLISHED'`, pub.ID); n != 1 {
		t.Fatalf("history: %d", n)
	}

	// draft, then publish through PATCH (If-Match required and checked)
	draft := cuCreate(t, owner, std.id, "Draft news item", false, "DRAFT")
	path := "/campaigns/" + std.id + "/updates/" + draft.ID
	owner.expect(owner.do("PATCH", path, map[string]any{"publish": true}), 422, "VALIDATION_FAILED")
	owner.expect(owner.do("PATCH", path, map[string]any{"publish": true}, "If-Match", `"9"`), 409, "VERSION_CONFLICT")
	r := owner.do("PATCH", path, map[string]any{"title": "Draft news, now final", "publish": true}, "If-Match", fmt.Sprintf(`"%d"`, draft.Version))
	owner.expect(r, 200, "")
	if up := cuDecode[cuUpdate](t, r); up.Status != "PUBLISHED" || up.Title != "Draft news, now final" || up.Version != draft.Version+1 {
		t.Fatalf("patched: %+v", up)
	}
	owner.expect(owner.do("PATCH", path, map[string]any{"title": "Too late to edit"}, "If-Match", fmt.Sprintf(`"%d"`, draft.Version+1)), 409, "UPDATE_NOT_EDITABLE")

	// content rules (§9)
	owner.expect(cuPost(t, owner, std.id, "Look here", `Hello <img src=x onerror=alert(1)> friends`, true), 422, "HTML_NOT_ALLOWED")
	owner.expect(cuPost(t, owner, std.id, "<script>alert(1)</script>", "A perfectly normal body text.", true), 422, "HTML_NOT_ALLOWED")
	owner.expect(cuPost(t, owner, std.id, "Click this", "Please visit javascript:alert(document.cookie) today", true), 422, "UNSAFE_LINK")
	owner.expect(cuPost(t, owner, std.id, "Hi", "A perfectly normal body text.", true), 422, "VALIDATION_FAILED")
	owner.expect(cuPost(t, owner, std.id, "Fine title", "short", true), 422, "VALIDATION_FAILED")
	r = cuPost(t, owner, std.id, "Bidi\u202e trick title", "Body with a NUL\x00 and bell\x07 characters inside.", false)
	owner.expect(r, 201, "")
	if up := cuDecode[cuUpdate](t, r); up.Title != "Bidi trick title" || up.Body != "Body with a NUL and bell characters inside." {
		t.Fatalf("control characters not stripped: %q / %q", up.Title, up.Body)
	}
	owner.expect(owner.do("POST", "/campaigns/"+std.id+"/updates", map[string]any{"title": "Unknown field", "body": "A perfectly normal body text.",
		"status": "PUBLISHED"}), 422, "VALIDATION_FAILED")
	// media references are refused while no approved media exist (media not wired: fail closed)
	owner.expect(owner.do("POST", "/campaigns/"+std.id+"/updates", map[string]any{"title": "With a photo", "body": "A perfectly normal body text.",
		"publish": true, "media_ids": []string{ids.New()}}), 422, "MEDIA_NOT_APPROVED")

	// category pre-moderation
	pend := cuCreate(t, owner, med.id, "Operation went well", true, "PENDING_MODERATION")
	if n := cuCount(t, `SELECT count(*) FROM app.outbox_events WHERE event_type = 'campaigns.update_submitted_for_moderation' AND aggregate_id = $1`, pend.ID); n != 1 {
		t.Fatalf("outbox submitted: %d", n)
	}
	if _, l := cuPublic(t, s, med.slug, ""); len(l.Updates) != 0 {
		t.Fatalf("pending update is public: %v", l.Updates)
	}
	m1 := s.staff(t, mods.mod1)
	inQueue, cursor := false, ""
	for page := 0; page < 50 && !inQueue; page++ {
		q := m1.do("GET", "/admin/campaigns/updates/moderation?limit=100&cursor="+cursor, nil)
		m1.expect(q, 200, "")
		if strings.Contains(string(q.Data), pub.ID) {
			t.Fatal("a published update is in the moderation queue")
		}
		var ql struct {
			Updates []struct {
				ID       string `json:"id"`
				AuthorID string `json:"author_id"`
			} `json:"updates"`
			Next string `json:"next_cursor"`
		}
		_ = json.Unmarshal(q.Data, &ql)
		for _, it := range ql.Updates {
			inQueue = inQueue || (it.ID == pend.ID && it.AuthorID == owner.id)
		}
		if ql.Next == "" {
			break
		}
		cursor = ql.Next
	}
	if !inQueue {
		t.Fatal("pending update not in the moderation queue")
	}
	owner.expect(owner.do("GET", "/admin/campaigns/updates/moderation", nil), 404, "") // personal session: not a staff route
	m1.expect(m1.do("POST", "/admin/campaigns/updates/"+pend.ID+"/approve", map[string]string{"reason_code": "ok", "note": "x"}), 422, "VALIDATION_FAILED")
	r = m1.do("POST", "/admin/campaigns/updates/"+pend.ID+"/approve", cuDecision("CONTENT_OK"))
	m1.expect(r, 200, "")
	if up := cuDecode[cuUpdate](t, r); up.Status != "PUBLISHED" {
		t.Fatalf("approved: %+v", up)
	}
	m1.expect(m1.do("POST", "/admin/campaigns/updates/"+pend.ID+"/approve", cuDecision("CONTENT_OK")), 409, "INVALID_STATUS")
	if _, l := cuPublic(t, s, med.slug, ""); len(l.Updates) != 1 || l.Updates[0]["id"] != pend.ID {
		t.Fatalf("approved update not public: %v", l.Updates)
	}
	if n := cuCount(t, `SELECT count(*) FROM audit.audit_events WHERE action = 'campaign.update_published' AND target_id = $1 AND actor_type = 'staff'`, pend.ID); n != 1 {
		t.Fatalf("staff audit: %d", n)
	}

	// RESTRICTED owner: forced moderation; SUSPENDED/OFFBOARDED: refused, and pending content cannot be approved
	restr.set(owner.id, campaigns.RestrictionRestricted)
	held := cuCreate(t, owner, std.id, "News while restricted", true, "PENDING_MODERATION")
	if n := cuCount(t, `SELECT count(*) FROM app.campaign_updates WHERE id = $1 AND 'OWNER_RESTRICTED' = ANY (moderation_reasons)`, held.ID); n != 1 {
		t.Fatal("moderation reason not recorded")
	}
	if strings.Contains(string(owner.do("GET", "/campaigns/"+std.id+"/updates", nil).Data), "OWNER_RESTRICTED") {
		t.Fatal("restriction reason disclosed to the owner")
	}
	for _, lvl := range []string{campaigns.RestrictionSuspended, campaigns.RestrictionOffboarded} {
		restr.set(owner.id, lvl)
		owner.expect(cuPost(t, owner, std.id, "News while suspended", "This must be refused for the owner.", true), 403, "ACCOUNT_RESTRICTED")
		owner.expect(cuPost(t, owner, std.id, "Draft while suspended", "This must be refused for the owner.", false), 403, "ACCOUNT_RESTRICTED")
		m1.expect(m1.do("POST", "/admin/campaigns/updates/"+held.ID+"/approve", cuDecision("CONTENT_OK")), 409, "ACCOUNT_RESTRICTED")
	}
	restr.set(owner.id, "")
	m1.expect(m1.do("POST", "/admin/campaigns/updates/"+held.ID+"/approve", cuDecision("CONTENT_OK")), 200, "")
}

func TestCampaignUpdatesSuspendedCampaignRefused(t *testing.T) {
	s, _ := newCUServer(t)
	mods := cuModerators(t, s)
	owner := cuOwner(t, s, "cu-susp")
	susp := cuMakeCampaign(t, owner.id, "EDUCATION", true)
	owner.expect(cuPost(t, owner, susp.id, "Refused update", "A suspended campaign takes no updates.", true), 409, "INVALID_STATUS")
	owner.expect(cuPost(t, owner, susp.id, "Refused draft", "A suspended campaign takes no updates.", false), 409, "INVALID_STATUS")
	owner.expect(owner.do("GET", "/campaigns/"+susp.id+"/updates", nil), 200, "") // the owner can still read
	if r, _ := cuPublic(t, s, susp.slug, ""); r.Status != 404 || r.Error.Code != "CAMPAIGN_NOT_FOUND" {
		t.Fatalf("public listing of a suspended campaign: %d %s", r.Status, r.Error.Code)
	}

	// a live campaign that gets suspended: existing updates disappear publicly; owner writes and approval refused
	live := cuMakeCampaign(t, owner.id, "MEDICAL", false)
	pend := cuCreate(t, owner, live.id, "Pending before suspension", true, "PENDING_MODERATION")
	draft := cuCreate(t, owner, live.id, "Draft before suspension", false, "DRAFT")
	cuSuspend(t, live.id)
	if r, _ := cuPublic(t, s, live.slug, ""); r.Status != 404 {
		t.Fatalf("public listing after suspension: %d", r.Status)
	}
	owner.expect(owner.do("PATCH", "/campaigns/"+live.id+"/updates/"+draft.ID, map[string]any{"title": "Edited after suspension"},
		"If-Match", fmt.Sprint(draft.Version)), 409, "INVALID_STATUS")
	owner.expect(owner.do("DELETE", "/campaigns/"+live.id+"/updates/"+draft.ID, nil), 409, "INVALID_STATUS")
	m1 := s.staff(t, mods.mod1)
	m1.expect(m1.do("POST", "/admin/campaigns/updates/"+pend.ID+"/approve", cuDecision("CONTENT_OK")), 409, "INVALID_STATUS")
	// staff can still hide content on a suspended campaign
	m1.expect(m1.do("POST", "/admin/campaigns/updates/"+pend.ID+"/hide", cuDecision("CAMPAIGN_SUSPENDED")), 200, "")
}

func TestCampaignUpdatesIDOR(t *testing.T) {
	s, _ := newCUServer(t)
	owner := cuOwner(t, s, "cu-own")
	other := cuOwner(t, s, "cu-oth")
	mine := cuMakeCampaign(t, owner.id, "EDUCATION", false)
	theirs := cuMakeCampaign(t, other.id, "EDUCATION", false)
	draft := cuCreate(t, owner, mine.id, "Owner's private draft", false, "DRAFT")
	pub := cuCreate(t, owner, mine.id, "Owner's public news", true, "PUBLISHED")

	// another user cannot see or touch the owner's campaign updates (404, existence not revealed)
	other.expect(cuPost(t, other, mine.id, "Hijack attempt", "Writing into someone else's campaign.", true), 404, "CAMPAIGN_NOT_FOUND")
	other.expect(other.do("GET", "/campaigns/"+mine.id+"/updates", nil), 404, "CAMPAIGN_NOT_FOUND")
	for _, id := range []string{draft.ID, pub.ID} {
		other.expect(other.do("PATCH", "/campaigns/"+mine.id+"/updates/"+id, map[string]any{"title": "Hijacked title"}, "If-Match", "1"), 404, "CAMPAIGN_NOT_FOUND")
		other.expect(other.do("DELETE", "/campaigns/"+mine.id+"/updates/"+id, nil), 404, "CAMPAIGN_NOT_FOUND")
		// through their own campaign id: the update is not found there
		other.expect(other.do("PATCH", "/campaigns/"+theirs.id+"/updates/"+id, map[string]any{"title": "Hijacked title"}, "If-Match", "1"), 404, "UPDATE_NOT_FOUND")
		other.expect(other.do("DELETE", "/campaigns/"+theirs.id+"/updates/"+id, nil), 404, "UPDATE_NOT_FOUND")
	}
	// the owner's own path mixing campaigns is refused too
	owner.expect(owner.do("DELETE", "/campaigns/"+theirs.id+"/updates/"+draft.ID, nil), 404, "CAMPAIGN_NOT_FOUND")
	// anonymous callers get 401 on owner routes
	anon := s.browser(t)
	if r := anon.do("GET", "/campaigns/"+mine.id+"/updates", nil); r.Status != 401 {
		t.Fatalf("anonymous owner list: %d", r.Status)
	}
	// nothing changed
	if n := cuCount(t, `SELECT count(*) FROM app.campaign_updates WHERE id = ANY($1::uuid[]) AND version = 1 AND status IN ('DRAFT','PUBLISHED')`,
		[]string{draft.ID, pub.ID}); n != 2 {
		t.Fatalf("updates changed: %d", n)
	}
	// the owner's soft delete keeps the row
	owner.expect(owner.do("DELETE", "/campaigns/"+mine.id+"/updates/"+draft.ID, nil, "If-Match", "1"), 204, "")
	owner.expect(owner.do("DELETE", "/campaigns/"+mine.id+"/updates/"+draft.ID, nil), 404, "UPDATE_NOT_FOUND")
	if n := cuCount(t, `SELECT count(*) FROM app.campaign_updates WHERE id = $1 AND status = 'DELETED' AND deleted_by = $2`, draft.ID, owner.id); n != 1 {
		t.Fatal("soft delete not recorded")
	}
	// the database itself refuses a hard delete
	if _, err := pool(t, os.Getenv("DATABASE_URL")).Exec(ctx(t), `DELETE FROM app.campaign_updates WHERE id = $1`, draft.ID); err == nil {
		t.Fatal("hard delete succeeded")
	}
}

func TestCampaignUpdatesPublicListingAndHide(t *testing.T) {
	s, _ := newCUServer(t)
	mods := cuModerators(t, s)
	owner := cuOwner(t, s, "cu-list")
	c := cuMakeCampaign(t, owner.id, "EDUCATION", false)
	first := cuCreate(t, owner, c.id, "First public news", true, "PUBLISHED")
	second := cuCreate(t, owner, c.id, "Second public news", true, "PUBLISHED")
	draft := cuCreate(t, owner, c.id, "Unpublished draft", false, "DRAFT")
	gone := cuCreate(t, owner, c.id, "Published then deleted", true, "PUBLISHED")
	owner.expect(owner.do("DELETE", "/campaigns/"+c.id+"/updates/"+gone.ID, nil), 204, "")

	r, l := cuPublic(t, s, c.slug, "")
	if r.Status != 200 {
		t.Fatalf("public list: %d %s", r.Status, r.Error.Code)
	}
	if got := cuPublicIDs(l); len(got) != 2 || got[0] != second.ID || got[1] != first.ID {
		t.Fatalf("public list (newest first, published only): %v", got)
	}
	raw := string(r.Data)
	for _, leak := range []string{owner.id, draft.ID, gone.ID, "author", "approved_by", "hidden", "moderation", "status", "version"} {
		if strings.Contains(raw, leak) {
			t.Fatalf("public listing leaks %q: %s", leak, raw)
		}
	}
	if l.Updates[0]["organiser_name"] != owner.name {
		t.Fatalf("organiser name: %v (want %q)", l.Updates[0]["organiser_name"], owner.name)
	}
	// cursor pagination
	_, p1 := cuPublic(t, s, c.slug, "?limit=1")
	if len(p1.Updates) != 1 || p1.Next != second.ID {
		t.Fatalf("page 1: %v next %q", cuPublicIDs(p1), p1.Next)
	}
	_, p2 := cuPublic(t, s, c.slug, "?limit=1&cursor="+p1.Next)
	if got := cuPublicIDs(p2); len(got) != 1 || got[0] != first.ID {
		t.Fatalf("page 2: %v", got)
	}
	if r, _ := cuPublic(t, s, c.slug, "?cursor=not-a-uuid"); r.Status != 422 {
		t.Fatalf("bad cursor: %d", r.Status)
	}
	if r, _ := cuPublic(t, s, "no-such-campaign-0000000000", ""); r.Status != 404 {
		t.Fatalf("unknown slug: %d", r.Status)
	}

	// hiding removes the update from the public listing; the owner sees HIDDEN with the reason code only
	m1 := s.staff(t, mods.mod1)
	m1.expect(m1.do("POST", "/admin/campaigns/updates/"+first.ID+"/hide", map[string]string{"reason_code": "MISLEADING_CONTENT"}), 422, "VALIDATION_FAILED")
	m1.expect(m1.do("POST", "/admin/campaigns/updates/"+first.ID+"/hide", cuDecision("MISLEADING_CONTENT")), 200, "")
	m1.expect(m1.do("POST", "/admin/campaigns/updates/"+first.ID+"/hide", cuDecision("MISLEADING_CONTENT")), 409, "INVALID_STATUS")
	m1.expect(m1.do("POST", "/admin/campaigns/updates/"+draft.ID+"/hide", cuDecision("MISLEADING_CONTENT")), 409, "INVALID_STATUS")
	if _, l := cuPublic(t, s, c.slug, ""); len(l.Updates) != 1 || l.Updates[0]["id"] != second.ID {
		t.Fatalf("hidden update still public: %v", cuPublicIDs(l))
	}
	if n := cuCount(t, `SELECT count(*) FROM audit.audit_events WHERE action = 'campaign.update_hidden' AND target_id = $1`, first.ID); n != 1 {
		t.Fatalf("audit campaign.update_hidden: %d", n)
	}
	ol := owner.do("GET", "/campaigns/"+c.id+"/updates", nil)
	owner.expect(ol, 200, "")
	var own struct {
		Updates []cuUpdate `json:"updates"`
	}
	_ = json.Unmarshal(ol.Data, &own)
	found := false
	for _, u := range own.Updates {
		if u.ID == first.ID {
			found = u.Status == "HIDDEN" && u.HiddenReason != nil && *u.HiddenReason == "MISLEADING_CONTENT"
		}
		if u.ID == gone.ID {
			t.Fatal("deleted update in the owner list")
		}
	}
	if !found || strings.Contains(string(ol.Data), mods.mod1.id) || strings.Contains(string(ol.Data), "integration test moderation decision") {
		t.Fatalf("owner view of the hidden update: %s", ol.Data)
	}
	owner.expect(owner.do("PATCH", "/campaigns/"+c.id+"/updates/"+first.ID, map[string]any{"title": "Trying to edit hidden"}, "If-Match", "2"), 409, "UPDATE_NOT_EDITABLE")
	// the history is append-only for the runtime role
	if _, err := pool(t, os.Getenv("DATABASE_URL")).Exec(ctx(t), `UPDATE app.campaign_update_events SET note = 'rewritten' WHERE update_id = $1`, first.ID); err == nil {
		t.Fatal("history rewrite succeeded")
	}
}

func TestCampaignUpdatesModeratorCannotApproveOwnLinkedContent(t *testing.T) {
	s, _ := newCUServer(t)
	mods := cuModerators(t, s)
	owner := cuOwner(t, s, "cu-self")
	c := cuMakeCampaign(t, owner.id, "MEDICAL", false)
	pend := cuCreate(t, owner, c.id, "Please approve me", true, "PENDING_MODERATION")

	migratorExec(t, `UPDATE app.users SET staff_personal_user_id = NULL WHERE id = $1`, mods.mod2.id)
	migratorExec(t, `UPDATE app.users SET staff_personal_user_id = $2 WHERE id = $1`, mods.mod2.id, owner.id)
	t.Cleanup(func() {
		migratorExec(t, `UPDATE app.users SET staff_personal_user_id = NULL WHERE id = $1`, mods.mod2.id)
	})
	m2 := s.staff(t, mods.mod2)
	m2.expect(m2.do("POST", "/admin/campaigns/updates/"+pend.ID+"/approve", cuDecision("CONTENT_OK")), 403, "SELF_DECISION_FORBIDDEN")
	m2.expect(m2.do("POST", "/admin/campaigns/updates/"+pend.ID+"/hide", cuDecision("SPAM")), 403, "SELF_DECISION_FORBIDDEN")

	// the database refuses it too, whatever the application does
	_, err := pool(t, os.Getenv("DATABASE_URL")).Exec(ctx(t), `UPDATE app.campaign_updates SET status = 'PUBLISHED', published_at = now(),
		approved_by = $2, approved_at = now() WHERE id = $1`, pend.ID, mods.mod2.id)
	if err == nil || sqlState(err) != "23514" || !strings.Contains(err.Error(), "may not moderate content that concerns them") {
		t.Fatalf("linked moderator approval in SQL: %v", err)
	}
	if n := cuCount(t, `SELECT count(*) FROM app.campaign_updates WHERE id = $1 AND status = 'PENDING_MODERATION'`, pend.ID); n != 1 {
		t.Fatal("update changed")
	}

	// an unrelated moderator can approve
	m1 := s.staff(t, mods.mod1)
	m1.expect(m1.do("POST", "/admin/campaigns/updates/"+pend.ID+"/approve", cuDecision("CONTENT_OK")), 200, "")
	if n := cuCount(t, `SELECT count(*) FROM app.campaign_updates WHERE id = $1 AND approved_by = $2`, pend.ID, mods.mod1.id); n != 1 {
		t.Fatal("approver not recorded")
	}
}
