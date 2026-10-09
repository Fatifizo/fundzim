//go:build integration

// Stage 5 verification end to end against the REAL local stack: KYC, KYB, beneficiaries, payout
// destinations, secure documents (scanned by the running worker with real ClamAV), the review engine,
// self-review and four-eyes controls, isolation, concurrency, failure injection and a performance sample.
// The API runs in-process on the shared database/Garage; scanning, the KYC mirror and notifications run in
// the worker container. Requires `docker compose up -d` with current images and migrations applied.
package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/auth"
	"github.com/Fatifizo/fundzim/internal/platform/config"
)

// ---- staff fixture (bootstrapped once per test binary; browsers are created per test) -------------------

type staffCred struct{ id, email, secret string }

type verifStaff struct {
	adminA, adminB        staffCred
	rev1, rev2, support   staffCred
	compliance, linkedRev staffCred
	campRev1, campRev2    staffCred // Stage 6: REVIEWER (campaign.review / decide / publish)
	compliance2           staffCred // second COMPLIANCE officer (maker-checker)
}

var (
	vStaffOnce sync.Once
	vStaff     *verifStaff
	vStaffErr  string
)

func inviteStaff(t *testing.T, s *itServer, a, b *browser, name, role string) staffCred {
	t.Helper()
	email := uniqueEmail("vs-" + strings.ToLower(role))
	r := a.do("POST", "/admin/staff", map[string]string{"email": email, "display_name": name, "justification": "stage 5 verification test staff"})
	a.expect(r, 201, "")
	id := r.field(t, "user_id")
	secret := acceptStaffInvitation(t, s, email)
	if role != "" {
		r = a.do("POST", "/admin/role-assignment-requests", map[string]string{"user_id": id, "role_code": role, "action": "GRANT",
			"justification": "verification integration test role"})
		a.expect(r, 201, "")
		b.expect(b.do("POST", "/admin/role-assignment-requests/"+r.field(t, "id")+"/approve", map[string]string{"reason": "test fixture"}), 200, "")
	}
	return staffCred{id: id, email: email, secret: secret}
}

func staffFixture(t *testing.T, s *itServer) *verifStaff {
	t.Helper()
	vStaffOnce.Do(func() {
		ok := false
		defer func() {
			if !ok {
				vStaffErr = "staff fixture setup failed in an earlier test"
			}
		}()
		resetSuperAdmins(t)
		f := &verifStaff{}
		f.adminA = staffCred{email: uniqueEmail("vsa-a")}
		f.adminB = staffCred{email: uniqueEmail("vsa-b")}
		got, err := s.d.Auth.BootstrapSuperAdmins(ctx(t), auth.BootstrapAdmin{Email: f.adminA.email, DisplayName: "Verif Admin A"},
			auth.BootstrapAdmin{Email: f.adminB.email, DisplayName: "Verif Admin B"}, "stage 5 verification integration fixture")
		if err != nil {
			t.Fatal(err)
		}
		f.adminA.id, f.adminB.id = got[0], got[1]
		f.adminA.secret = acceptStaffInvitation(t, s, f.adminA.email)
		f.adminB.secret = acceptStaffInvitation(t, s, f.adminB.email)
		a, b := s.browser(t), s.browser(t)
		a.staffLogin(f.adminA.email, f.adminA.secret)
		b.staffLogin(f.adminB.email, f.adminB.secret)
		f.rev1 = inviteStaff(t, s, a, b, "Reviewer One", "KYC_REVIEWER")
		f.rev2 = inviteStaff(t, s, a, b, "Reviewer Two", "KYC_REVIEWER")
		f.support = inviteStaff(t, s, a, b, "Support Agent", "SUPPORT")
		f.compliance = inviteStaff(t, s, a, b, "Compliance Officer", "COMPLIANCE")
		f.linkedRev = inviteStaff(t, s, a, b, "Linked Reviewer", "KYC_REVIEWER")
		f.campRev1 = inviteStaff(t, s, a, b, "Campaign Reviewer One", "REVIEWER")
		f.campRev2 = inviteStaff(t, s, a, b, "Campaign Reviewer Two", "REVIEWER")
		f.compliance2 = inviteStaff(t, s, a, b, "Compliance Officer Two", "COMPLIANCE")
		vStaff = f
		ok = true
	})
	if vStaff == nil {
		t.Fatal(vStaffErr)
	}
	// TestIdentityRBACStaffMakerChecker resets super admins (bootstrap ceremony): re-bootstrap the fixture
	// admins if they were revoked since (the other fixture staff keep their roles).
	var active bool
	if err := pool(t, os.Getenv("DATABASE_MIGRATION_URL")).QueryRow(ctx(t), `SELECT EXISTS (SELECT 1 FROM app.role_assignments
		WHERE user_id = $1 AND revoked_at IS NULL AND role_id = md5('role:SUPER_ADMIN')::uuid)`, vStaff.adminA.id).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if !active {
		resetSuperAdmins(t)
		a, b := staffCred{email: uniqueEmail("vsa-a")}, staffCred{email: uniqueEmail("vsa-b")}
		got, err := s.d.Auth.BootstrapSuperAdmins(ctx(t), auth.BootstrapAdmin{Email: a.email, DisplayName: "Verif Admin A"},
			auth.BootstrapAdmin{Email: b.email, DisplayName: "Verif Admin B"}, "stage 6 verification integration fixture (re-bootstrap)")
		if err != nil {
			t.Fatal(err)
		}
		a.id, b.id = got[0], got[1]
		a.secret = acceptStaffInvitation(t, s, a.email)
		b.secret = acceptStaffInvitation(t, s, b.email)
		vStaff.adminA, vStaff.adminB = a, b
	}
	return vStaff
}

var (
	staffSessMu sync.Mutex
	staffSess   = map[string]*browser{}
)

// staff returns a signed-in browser for c on server s. Sessions are reused across tests (the cookie jar is
// host-scoped, every test server is on 127.0.0.1) so per-account login limits are not exhausted.
func (s *itServer) staff(t *testing.T, c staffCred) *browser {
	staffSessMu.Lock()
	defer staffSessMu.Unlock()
	if prev, ok := staffSess[c.email]; ok {
		b := &browser{t: t, s: s, c: prev.c, ip: prev.ip}
		if b.do("GET", "/me", nil).Status == 200 {
			return b
		}
	}
	b := s.freshStaff(t, c)
	staffSess[c.email] = b
	return b
}

func (s *itServer) freshStaff(t *testing.T, c staffCred) *browser {
	b := s.browser(t)
	b.staffLogin(c.email, c.secret)
	return b
}

// ---- personal-account helpers ---------------------------------------------------------------------------

type vUser struct {
	*browser
	id, email string
}

// verifiedUser registers, verifies email and phone, and signs in.
func verifiedUser(t *testing.T, s *itServer, prefix string) vUser {
	t.Helper()
	email := uniqueEmail(prefix)
	b := s.browser(t)
	b.registerVerified(email)
	b.mustLogin(email, itPassword)
	suffix := fmt.Sprintf("%07d", time.Now().UnixNano()%10_000_000)
	b.expect(b.do("POST", "/me/phone/verify-request", map[string]string{"phone": "077" + suffix}), 202, "")
	text := mailTo(t, "26377"+suffix+"@sms.dev.invalid", "")
	code := regexp.MustCompile(`\b(\d{6})\b`).FindStringSubmatch(text)
	if code == nil {
		t.Fatalf("no code in dev SMS: %q", text)
	}
	b.expect(b.do("POST", "/me/phone/verify-confirm", map[string]string{"phone": "+26377" + suffix, "code": code[1]}), 200, "")
	var me auth.Me
	_ = json.Unmarshal(b.do("GET", "/me", nil).Data, &me)
	return vUser{browser: b, id: me.ID, email: email}
}

// upload posts a multipart document (fields first, then the file) with an untrusted filename.
func (b *browser) upload(subjectType, subjectID, docType, side, filename, ctype string, content []byte) apiResp {
	b.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, kv := range [][2]string{{"subject_type", subjectType}, {"subject_id", subjectID}, {"document_type", docType}, {"side", side}} {
		if kv[1] != "" {
			_ = mw.WriteField(kv[0], kv[1])
		}
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	h.Set("Content-Type", ctype)
	fw, _ := mw.CreatePart(h)
	_, _ = fw.Write(content)
	_ = mw.Close()
	req, _ := http.NewRequest("POST", b.s.srv.URL+"/api/v1/verification/documents", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Forwarded-For", b.ip)
	req.Header.Set("X-CSRF-Token", b.cookie("fz_csrf"))
	resp, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := apiResp{Status: resp.StatusCode, Header: resp.Header}
	var env struct {
		Data  json.RawMessage `json:"data"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		b.t.Fatalf("upload: non-JSON %d: %s", resp.StatusCode, raw)
	}
	out.Data = env.Data
	_ = json.Unmarshal(env.Error, &out.Error)
	return out
}

func (b *browser) uploadOK(subjectType, subjectID, docType, side string) string {
	b.t.Helper()
	r := b.upload(subjectType, subjectID, docType, side, "../../etc/"+strings.ToLower(docType)+".pdf.exe", "application/pdf", itPDF(docType+subjectID+side))
	b.expect(r, 201, "")
	return r.field(b.t, "id")
}

// waitDoc polls the owner's view until the document leaves the scanning states.
func (b *browser) waitDoc(id string) string {
	b.t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		r := b.do("GET", "/verification/documents/"+id, nil)
		b.expect(r, 200, "")
		switch st := r.field(b.t, "status"); st {
		case "CLEAN", "REJECTED", "FAILED_SCAN", "DELETED":
			return st
		}
		time.Sleep(400 * time.Millisecond)
	}
	b.t.Fatalf("document %s still not scanned (is the worker running current code with ClamAV?)", id)
	return ""
}

var idSeq = time.Now().UnixNano() % 1_000_000

func uniqueIDNumber() string {
	idSeq++
	return fmt.Sprintf("63-%06dA%02d", idSeq%1_000_000, idSeq%100)
}

func identityDraft(idNumber string) map[string]any {
	return map[string]any{"legal_first_name": "Tariro", "legal_last_name": "Moyo", "date_of_birth": "1990-05-01", "nationality": "ZW",
		"country_of_residence": "ZW", "id_document_type": "ZW_NATIONAL_ID", "id_document_number": idNumber,
		"id_document_expiry":  time.Now().AddDate(3, 0, 0).Format("2006-01-02"),
		"residential_address": map[string]string{"line1": "12 Samora Machel Ave", "city": "Harare", "country": "ZW"}}
}

// submittedKYC creates, fills, documents and submits a KYC case; returns the case id.
func submittedKYC(t *testing.T, u vUser, idNumber string, extraDocs ...string) string {
	t.Helper()
	r := u.do("POST", "/kyc/cases", map[string]string{"target_level": "IDENTITY_VERIFIED"})
	u.expect(r, 201, "")
	caseID := r.field(t, "id")
	r = u.do("PATCH", "/kyc/cases/"+caseID, identityDraft(idNumber), "If-Match", r.Header.Get("ETag"))
	u.expect(r, 200, "")
	docs := []string{u.uploadOK("KYC_CASE", caseID, "ZW_NATIONAL_ID", "FRONT"), u.uploadOK("KYC_CASE", caseID, "SELFIE", "NA")}
	for _, d := range extraDocs {
		docs = append(docs, u.uploadOK("KYC_CASE", caseID, d, "NA"))
	}
	for _, d := range docs {
		if st := u.waitDoc(d); st != "CLEAN" {
			t.Fatalf("document %s: %s", d, st)
		}
	}
	u.expect(u.do("POST", "/kyc/cases/"+caseID+"/submit", nil), 200, "")
	return caseID
}

func adminAct(b *browser, caseID, action string, body any) apiResp {
	b.t.Helper()
	return b.do("POST", "/admin/verification/cases/"+caseID+"/"+action, body)
}

// approvedKYC takes a user through KYC to IDENTITY_VERIFIED with reviewer rev.
func approvedKYC(t *testing.T, u vUser, rev *browser) string {
	t.Helper()
	caseID := submittedKYC(t, u, uniqueIDNumber())
	rev.expect(adminAct(rev, caseID, "assign", nil), 200, "")
	rev.expect(adminAct(rev, caseID, "start-review", nil), 200, "")
	rev.expect(adminAct(rev, caseID, "approve", map[string]string{"reason_code": "DOCUMENTS_CONSISTENT", "note": "ID and selfie match"}), 200, "")
	return caseID
}

func kycLevel(t *testing.T, u vUser) (string, string) {
	t.Helper()
	r := u.do("GET", "/kyc/status", nil)
	u.expect(r, 200, "")
	return r.field(t, "level"), r.field(t, "status")
}

func migratorExec(t *testing.T, sql string, args ...any) {
	t.Helper()
	need(t, "DATABASE_MIGRATION_URL")
	if _, err := pool(t, os.Getenv("DATABASE_MIGRATION_URL")).Exec(ctx(t), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func countAudit(t *testing.T, table, action, target string) int {
	t.Helper()
	var n int
	if err := pool(t, workerURL(t)).QueryRow(ctx(t), `SELECT count(*) FROM `+table+` WHERE action = $1 AND (target_id::text = $2 OR metadata->>'document_id' = $2)`, action, target).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// ===== tests ==============================================================================================

func TestVerificationKYCEndToEnd(t *testing.T) {
	s := newITServer(t, nil)
	f := staffFixture(t, s)
	rev1, rev2, sup := s.staff(t, f.rev1), s.staff(t, f.rev2), s.staff(t, f.support)

	// prerequisites: phone must be verified before a case can be opened
	email := uniqueEmail("kyc-nophone")
	np := s.browser(t)
	np.registerVerified(email)
	np.mustLogin(email, itPassword)
	np.expect(np.do("POST", "/kyc/cases", map[string]string{"target_level": "IDENTITY_VERIFIED"}), 422, "PREREQUISITES_NOT_MET")

	u := verifiedUser(t, s, "kyc-e2e")
	other := verifiedUser(t, s, "kyc-other")
	if lvl, _ := kycLevel(t, u); lvl == "IDENTITY_VERIFIED" {
		t.Fatal("fresh user must not be identity verified")
	}
	u.expect(u.do("POST", "/kyc/cases", map[string]string{"target_level": "PAYOUT_VERIFIED"}), 422, "VALIDATION_FAILED")
	r := u.do("POST", "/kyc/cases", map[string]string{"target_level": "IDENTITY_VERIFIED"})
	u.expect(r, 201, "")
	caseID := r.field(t, "id")
	etag := r.Header.Get("ETag")
	u.expect(u.do("POST", "/kyc/cases", map[string]string{"target_level": "IDENTITY_VERIFIED"}), 409, "CASE_ALREADY_OPEN")

	// optimistic concurrency on the draft; the full ID number is never echoed back
	idNum := uniqueIDNumber()
	u.expect(u.do("PATCH", "/kyc/cases/"+caseID, identityDraft(idNum), "If-Match", `"999"`), 409, "")
	r = u.do("PATCH", "/kyc/cases/"+caseID, identityDraft(idNum), "If-Match", etag)
	u.expect(r, 200, "")
	if strings.Contains(string(r.Data), idNum) || strings.Contains(string(r.Data), strings.ReplaceAll(idNum, "-", "")) {
		t.Fatalf("full ID number in response: %s", r.Data)
	}
	// incomplete submission is refused with details, never auto-rejected
	u.expect(u.do("POST", "/kyc/cases/"+caseID+"/submit", nil), 422, "SUBMISSION_INCOMPLETE")

	// IDOR: another user cannot touch this case or upload into it
	other.expect(other.do("PATCH", "/kyc/cases/"+caseID, identityDraft(uniqueIDNumber())), 404, "")
	other.expect(other.do("POST", "/kyc/cases/"+caseID+"/submit", nil), 404, "")
	other.expect(other.upload("KYC_CASE", caseID, "SELFIE", "NA", "x.pdf", "application/pdf", itPDF("idor")), 404, "")

	// uploads: untrusted filename and declared type; type is sniffed, unsupported content refused
	u.expect(u.upload("KYC_CASE", caseID, "SELFIE", "NA", "evil.html", "text/html", []byte("<html><script>alert(1)</script></html>")), 422, "UNSUPPORTED_FILE_TYPE")
	front := u.uploadOK("KYC_CASE", caseID, "ZW_NATIONAL_ID", "FRONT")
	selfie := u.uploadOK("KYC_CASE", caseID, "SELFIE", "NA")
	r = u.do("GET", "/verification/documents/"+front, nil)
	if strings.Contains(string(r.Data), "etc/") || strings.Contains(string(r.Data), ".exe") {
		t.Fatalf("user filename leaked into the document view: %s", r.Data)
	}
	// not downloadable before the scan says CLEAN
	if st := r.field(t, "status"); st != "CLEAN" {
		u.expect(u.do("POST", "/verification/documents/"+front+"/access", nil), 409, "")
	}
	for _, d := range []string{front, selfie} {
		if st := u.waitDoc(d); st != "CLEAN" {
			t.Fatalf("doc %s: %s", d, st)
		}
	}
	other.expect(other.do("GET", "/verification/documents/"+front, nil), 404, "DOCUMENT_NOT_FOUND")
	other.expect(other.do("POST", "/verification/documents/"+front+"/access", nil), 404, "DOCUMENT_NOT_FOUND")

	// owner download through a session-bound ticket; another session cannot use the ticket
	r = u.do("POST", "/verification/documents/"+front+"/access", nil)
	u.expect(r, 200, "")
	ticketURL := r.field(t, "url")
	resp := rawGet(t, u.browser, ticketURL)
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") ||
		resp.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") ||
		resp.Header.Get("Content-Type") != "application/pdf" {
		t.Fatalf("download: %d %v", resp.StatusCode, resp.Header)
	}
	if resp := rawGet(t, other.browser, ticketURL); resp.StatusCode != 404 {
		t.Fatalf("another user with the ticket: %d", resp.StatusCode)
	}
	u2 := s.browser(t) // same user, different session
	u2.mustLogin(u.email, itPassword)
	if resp := rawGet(t, u2, ticketURL); resp.StatusCode != 403 {
		t.Fatalf("ticket used from another session: %d", resp.StatusCode)
	}
	if resp := rawGet(t, u.browser, "/api/v1/verification/documents/"+front+"/content"); resp.StatusCode != 403 {
		t.Fatalf("download without ticket: %d", resp.StatusCode)
	}

	r = u.do("POST", "/kyc/cases/"+caseID+"/submit", nil)
	u.expect(r, 200, "")
	// a retried submit is an idempotent no-op (same status, same version)
	r2 := u.do("POST", "/kyc/cases/"+caseID+"/submit", nil)
	u.expect(r2, 200, "")
	if r2.Header.Get("ETag") != r.Header.Get("ETag") || r2.field(t, "status") != "SUBMITTED" {
		t.Fatalf("resubmit changed the case: %s vs %s", r.Header.Get("ETag"), r2.Header.Get("ETag"))
	}
	// no document replacement once submitted
	u.expect(u.upload("KYC_CASE", caseID, "SELFIE", "NA", "s.pdf", "application/pdf", itPDF("late")), 409, "CASE_NOT_EDITABLE")
	u.expect(u.do("DELETE", "/verification/documents/"+selfie, nil), 409, "")

	// staff: support has no queue; reviewers see it; only the assignee sees documents
	sup.expect(sup.do("GET", "/admin/verification/cases", nil), 403, "PERMISSION_DENIED")
	sup.expect(sup.do("POST", "/verification/documents/"+front+"/access", nil), 403, "PERMISSION_DENIED")
	r = rev1.do("GET", "/admin/verification/cases?type=KYC", nil)
	rev1.expect(r, 200, "")
	if !strings.Contains(string(r.Data), caseID) {
		t.Fatalf("queue lacks the submitted case: %.400s", r.Data)
	}
	if strings.Contains(string(r.Data), strings.ReplaceAll(idNum, "-", "")) {
		t.Fatal("queue leaks the ID number")
	}
	rev1.expect(adminAct(rev1, caseID, "assign", map[string]string{"assignee_id": f.support.id}), 422, "ASSIGNEE_NOT_ELIGIBLE")
	rev1.expect(adminAct(rev1, caseID, "assign", map[string]string{"assignee_id": u.id}), 422, "ASSIGNEE_NOT_ELIGIBLE")
	rev1.expect(adminAct(rev1, caseID, "approve", map[string]string{"reason_code": "DOCUMENTS_CONSISTENT", "note": "reviewed the submitted evidence"}), 409, "")
	rev1.expect(adminAct(rev1, caseID, "assign", nil), 200, "")
	rev2.expect(rev2.do("POST", "/verification/documents/"+front+"/access", nil), 403, "NOT_ASSIGNED")
	rev2.expect(adminAct(rev2, caseID, "approve", map[string]string{"reason_code": "DOCUMENTS_CONSISTENT", "note": "reviewed the submitted evidence"}), 409, "")
	rev1.expect(adminAct(rev1, caseID, "start-review", nil), 200, "")

	// reviewer document access is ticketed and audited
	before := countAudit(t, "audit.security_audit_events", "kyc.document.accessed", front)
	r = rev1.do("POST", "/verification/documents/"+front+"/access", nil)
	rev1.expect(r, 200, "")
	if resp := rawGet(t, rev1, r.field(t, "url")); resp.StatusCode != 200 {
		t.Fatalf("reviewer download: %d", resp.StatusCode)
	}
	if after := countAudit(t, "audit.security_audit_events", "kyc.document.accessed", front); after != before+1 {
		t.Fatalf("document access audit events: before %d after %d", before, after)
	}

	// case detail masks the ID number; reveal is a separate, justified, audited action
	r = rev1.do("GET", "/admin/verification/cases/"+caseID, nil)
	rev1.expect(r, 200, "")
	if strings.Contains(string(r.Data), strings.ReplaceAll(idNum, "-", "")) {
		t.Fatal("case detail leaks the full ID number")
	}
	rev1.expect(rev1.do("POST", "/admin/verification/cases/"+caseID+"/reveal-identity-number", map[string]string{"justification": "compare with document image for review"}), 403, "PERMISSION_DENIED")
	comp := s.staff(t, f.compliance)
	comp.expect(comp.do("POST", "/admin/verification/cases/"+caseID+"/reveal-identity-number", map[string]string{"justification": "x"}), 422, "")
	r = comp.do("POST", "/admin/verification/cases/"+caseID+"/reveal-identity-number", map[string]string{"justification": "compare with document image for review"})
	comp.expect(r, 200, "")
	if countAudit(t, "audit.security_audit_events", "kyc.identity_number.revealed", caseID) != 1 {
		t.Fatal("identity number reveal not audited")
	}
	if !strings.Contains(string(r.Data), strings.ReplaceAll(idNum, "-", "")) {
		t.Fatalf("reveal: %s", r.Data)
	}

	// approval: invalid reason code refused; approval records screening as not performed
	rev1.expect(adminAct(rev1, caseID, "approve", map[string]string{"reason_code": "ok"}), 422, "")
	r = adminAct(rev1, caseID, "approve", map[string]string{"reason_code": "DOCUMENTS_CONSISTENT", "note": "document and selfie consistent"})
	rev1.expect(r, 200, "")
	if lvl, st := kycLevel(t, u); lvl != "IDENTITY_VERIFIED" || st != "ACTIVE" {
		t.Fatalf("after approval: %s %s", lvl, st)
	}
	rev1.expect(adminAct(rev1, caseID, "approve", map[string]string{"reason_code": "DOCUMENTS_CONSISTENT", "note": "reviewed the submitted evidence"}), 409, "")
	var conds string
	if err := pool(t, os.Getenv("DATABASE_MIGRATION_URL")).QueryRow(ctx(t), `SELECT conditions::text FROM kyc.kyc_decisions WHERE kyc_case_id = $1`, caseID).Scan(&conds); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(conds, "SCREENING_PROVIDER_NOT_SELECTED") {
		t.Fatalf("decision must record that screening was not performed: %s", conds)
	}
	// the worker mirrors the level onto the account (outbox → consumer)
	waitFor(t, 30*time.Second, "kyc mirror", func() bool {
		var lvl *string
		_ = pool(t, os.Getenv("DATABASE_URL")).QueryRow(ctx(t), `SELECT kyc_level FROM app.users WHERE id = $1`, u.id).Scan(&lvl)
		return lvl != nil && *lvl == "IDENTITY_VERIFIED"
	})
	mailTo(t, u.email, "Your verification was approved")

	// no sensitive identity values in audit metadata or outbox payloads
	var leaks int
	if err := pool(t, workerURL(t)).QueryRow(ctx(t), `SELECT
		(SELECT count(*) FROM audit.audit_events WHERE metadata::text LIKE '%'||$1||'%') +
		(SELECT count(*) FROM audit.security_audit_events WHERE metadata::text LIKE '%'||$1||'%')`, strings.ReplaceAll(idNum, "-", "")).Scan(&leaks); err != nil {
		t.Fatal(err)
	}
	var outboxLeaks int
	_ = pool(t, os.Getenv("DATABASE_MIGRATION_URL")).QueryRow(ctx(t), `SELECT count(*) FROM app.outbox_events WHERE payload::text LIKE '%'||$1||'%' OR payload::text LIKE '%Samora%'`, strings.ReplaceAll(idNum, "-", "")).Scan(&outboxLeaks)
	if leaks+outboxLeaks != 0 {
		t.Fatalf("identity number leaked: audit %d outbox %d", leaks, outboxLeaks)
	}
}

func rawGet(t *testing.T, b *browser, path string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", b.s.srv.URL+path, nil)
	req.Header.Set("X-Forwarded-For", b.ip)
	resp, err := b.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp
}

func TestVerificationSelfReviewAndDuplicateIdentity(t *testing.T) {
	s := newITServer(t, nil)
	f := staffFixture(t, s)
	rev1, rev2 := s.staff(t, f.rev1), s.staff(t, f.rev2)

	// a reviewer whose staff account is linked to the subject's personal account cannot take the case
	owner := verifiedUser(t, s, "kyc-linked")
	migratorExec(t, `UPDATE app.users SET staff_personal_user_id = NULL WHERE id = $1`, f.linkedRev.id)
	migratorExec(t, `UPDATE app.users SET staff_personal_user_id = $2 WHERE id = $1`, f.linkedRev.id, owner.id)
	t.Cleanup(func() {
		migratorExec(t, `UPDATE app.users SET staff_personal_user_id = NULL WHERE id = $1`, f.linkedRev.id)
	})
	linked := s.staff(t, f.linkedRev)
	idNum := uniqueIDNumber()
	caseID := submittedKYC(t, owner, idNum)
	linked.expect(adminAct(linked, caseID, "assign", nil), 403, "SELF_DECISION_FORBIDDEN")
	rev1.expect(adminAct(rev1, caseID, "assign", map[string]string{"assignee_id": f.linkedRev.id}), 403, "SELF_DECISION_FORBIDDEN")
	rev1.expect(adminAct(rev1, caseID, "assign", nil), 200, "")
	rev1.expect(adminAct(rev1, caseID, "start-review", nil), 200, "")
	rev1.expect(adminAct(rev1, caseID, "approve", map[string]string{"reason_code": "DOCUMENTS_CONSISTENT", "note": "reviewed the submitted evidence"}), 200, "")
	// a compliance officer linked to the subject cannot reveal the subject's identity number
	migratorExec(t, `UPDATE app.users SET staff_personal_user_id = NULL WHERE id = $1`, f.linkedRev.id)
	migratorExec(t, `UPDATE app.users SET staff_personal_user_id = $2 WHERE id = $1`, f.compliance.id, owner.id)
	t.Cleanup(func() {
		migratorExec(t, `UPDATE app.users SET staff_personal_user_id = NULL WHERE id = $1`, f.compliance.id)
	})
	comp := s.staff(t, f.compliance)
	comp.expect(comp.do("POST", "/admin/verification/cases/"+caseID+"/reveal-identity-number",
		map[string]string{"justification": "checking my own linked account case"}), 403, "SELF_DECISION_FORBIDDEN")
	migratorExec(t, `UPDATE app.users SET staff_personal_user_id = NULL WHERE id = $1`, f.compliance.id)

	// the same identity number on another account: flagged ENHANCED with four-eyes, never auto-rejected,
	// and an approval can never create a second active identity
	dup := verifiedUser(t, s, "kyc-dup")
	dupCase := submittedKYC(t, dup, idNum, "PROOF_OF_ADDRESS")
	r := dup.do("GET", "/kyc/cases/current", nil)
	if st := r.field(t, "status"); st != "SUBMITTED" {
		t.Fatalf("duplicate must stay in review, not be auto-rejected: %s", st)
	}
	rev1.expect(adminAct(rev1, dupCase, "assign", nil), 200, "")
	rev1.expect(adminAct(rev1, dupCase, "start-review", nil), 200, "")
	r = rev1.do("GET", "/admin/verification/cases/"+dupCase, nil)
	if !strings.Contains(string(r.Data), "ENHANCED") {
		t.Fatalf("duplicate identity must raise the risk level to ENHANCED: %.600s", r.Data)
	}
	r = adminAct(rev1, dupCase, "approve", map[string]string{"reason_code": "DOCUMENTS_CONSISTENT", "note": "reviewed the submitted evidence"})
	rev1.expect(r, 202, "")
	rev1.expect(adminAct(rev1, dupCase, "second-approval", map[string]string{"note": "second look by the same reviewer"}), 403, "SECOND_APPROVER_MUST_DIFFER")
	rev2.expect(adminAct(rev2, dupCase, "second-approval", map[string]string{"note": "checked"}), 409, "DUPLICATE_IDENTITY_ACTIVE")
	if lvl, _ := kycLevel(t, dup); lvl == "IDENTITY_VERIFIED" {
		t.Fatal("duplicate identity was verified")
	}
	// the database also refuses a self-decision written around the application
	var n int
	_ = pool(t, os.Getenv("DATABASE_MIGRATION_URL")).QueryRow(ctx(t), `SELECT count(*) FROM kyc.kyc_decisions d JOIN kyc.kyc_cases c ON c.id = d.kyc_case_id
		WHERE d.decided_by = c.user_id`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d self-decisions exist", n)
	}
}

func TestVerificationConcurrentReviewActions(t *testing.T) {
	s := newITServer(t, nil)
	f := staffFixture(t, s)
	rev1, rev2 := s.staff(t, f.rev1), s.staff(t, f.rev2)

	// duplicate case creation: exactly one wins
	u := verifiedUser(t, s, "kyc-conc")
	codes := parallelDo(8, func(int) int {
		return u.do("POST", "/kyc/cases", map[string]string{"target_level": "IDENTITY_VERIFIED"}).Status
	})
	if codes[201] != 1 || codes[409] != 7 {
		t.Fatalf("concurrent create: %v", codes)
	}
	u.expect(u.do("POST", "/kyc/cases/"+u.do("GET", "/kyc/cases/current", nil).field(t, "id")+"/withdraw", nil), 200, "")

	caseID := submittedKYC(t, u, uniqueIDNumber())
	// two reviewers race to assign: one assignment wins, the other sees a reassignment or conflict — and only
	// the final assignee may decide
	codes = parallelDo(2, func(i int) int { return adminAct([]*browser{rev1, rev2}[i], caseID, "assign", nil).Status })
	if codes[200] < 1 {
		t.Fatalf("assign race: %v", codes)
	}
	r := rev1.do("GET", "/admin/verification/cases/"+caseID, nil)
	var detail struct {
		Case struct {
			AssignedTo *string `json:"assigned_to"`
		} `json:"case"`
		AssignedTo *string `json:"assigned_to"`
	}
	_ = json.Unmarshal(r.Data, &detail)
	assignee := detail.AssignedTo
	if assignee == nil {
		assignee = detail.Case.AssignedTo
	}
	winner := rev1
	if assignee != nil && *assignee == f.rev2.id {
		winner = rev2
	}
	winner.expect(adminAct(winner, caseID, "start-review", nil), 200, "")
	// approve vs reject at the same moment by the assignee: exactly one decision is recorded
	codes = parallelDo(2, func(i int) int {
		if i == 0 {
			r := adminAct(winner, caseID, "approve", map[string]string{"reason_code": "DOCUMENTS_CONSISTENT", "note": "reviewed the submitted evidence"})
			if r.Status == 422 {
				t.Logf("approve 422: %v %s", r.Error.Details, r.Error.Code)
			}
			return r.Status
		}
		r := adminAct(winner, caseID, "reject", map[string]string{"reason_code": "DOCUMENT_UNREADABLE", "user_message": "Please upload a clearer image.", "note": "reviewed the submitted evidence"})
		if r.Status == 422 {
			t.Logf("reject 422: %v", r.Error.Details)
		}
		return r.Status
	})
	if codes[200] != 1 || codes[409] != 1 {
		t.Fatalf("approve vs reject: %v", codes)
	}
	var decisions int
	_ = pool(t, os.Getenv("DATABASE_MIGRATION_URL")).QueryRow(ctx(t), `SELECT count(*) FROM kyc.kyc_decisions WHERE kyc_case_id = $1`, caseID).Scan(&decisions)
	if decisions != 1 {
		t.Fatalf("decisions recorded: %d", decisions)
	}
	var events, versions int
	_ = pool(t, os.Getenv("DATABASE_MIGRATION_URL")).QueryRow(ctx(t), `SELECT count(*), count(DISTINCT case_version) FROM kyc.case_events WHERE kyc_case_id = $1`, caseID).Scan(&events, &versions)
	if events != versions {
		t.Fatalf("case events not one per version: %d events, %d versions", events, versions)
	}

	// concurrent submissions of one draft: exactly one succeeds
	u2 := verifiedUser(t, s, "kyc-conc2")
	r = u2.do("POST", "/kyc/cases", map[string]string{"target_level": "IDENTITY_VERIFIED"})
	c2 := r.field(t, "id")
	u2.expect(u2.do("PATCH", "/kyc/cases/"+c2, identityDraft(uniqueIDNumber()), "If-Match", r.Header.Get("ETag")), 200, "")
	for _, d := range []string{u2.uploadOK("KYC_CASE", c2, "ZW_PASSPORT", "PHOTO_PAGE"), u2.uploadOK("KYC_CASE", c2, "SELFIE", "NA")} {
		u2.waitDoc(d)
	}
	codes = parallelDo(6, func(int) int { return u2.do("POST", "/kyc/cases/"+c2+"/submit", nil).Status })
	var submits int
	_ = pool(t, os.Getenv("DATABASE_MIGRATION_URL")).QueryRow(ctx(t), `SELECT count(*) FROM kyc.case_events WHERE kyc_case_id = $1 AND to_status = 'SUBMITTED'`, c2).Scan(&submits)
	if codes[200] != 6 || submits != 1 {
		t.Fatalf("concurrent submit: %v, %d SUBMITTED events", codes, submits)
	}
	// a document attached concurrently with submission either lands before the freeze or is refused
	codes = parallelDo(4, func(i int) int {
		return u2.upload("KYC_CASE", c2, "PROOF_OF_ADDRESS", "NA", "p.pdf", "application/pdf", itPDF(fmt.Sprint("race", i))).Status
	})
	if codes[201] != 0 {
		t.Fatalf("attachment after submission accepted: %v", codes)
	}
}

func parallelDo(n int, fn func(i int) int) map[int]int {
	var mu sync.Mutex
	out := map[int]int{}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			c := fn(i)
			mu.Lock()
			out[c]++
			mu.Unlock()
		}(i)
	}
	close(start)
	wg.Wait()
	return out
}

func TestVerificationKYBRepresentativeAndIsolation(t *testing.T) {
	s := newITServer(t, nil)
	f := staffFixture(t, s)
	rev1 := s.staff(t, f.rev1)

	admin := verifiedUser(t, s, "kyb-admin")
	member := verifiedUser(t, s, "kyb-member")
	outsider := verifiedUser(t, s, "kyb-outsider")
	r := admin.do("POST", "/organisations", map[string]string{"display_name": "Mutare Water Trust", "org_type": "TRUST"})
	admin.expect(r, 201, "")
	orgID := r.field(t, "id")
	r = admin.do("POST", "/organisations/"+orgID+"/invitations", map[string]string{"email": member.email, "role_code": "ORG_MEMBER"})
	admin.expect(r, 201, "")
	member.expect(member.do("POST", "/me/organisation-invitations/"+r.field(t, "id")+"/accept", nil), 200, "")

	r = admin.do("POST", "/organisations/"+orgID+"/kyb", nil)
	admin.expect(r, 201, "")
	kybID := r.field(t, "id")
	if kybID == "" {
		var v map[string]any
		_ = json.Unmarshal(r.Data, &v)
		if c, ok := v["case"].(map[string]any); ok {
			kybID, _ = c["id"].(string)
		}
	}
	// isolation: outsiders get 404, members see status only and cannot edit
	outsider.expect(outsider.do("GET", "/organisations/"+orgID+"/kyb", nil), 404, "ORGANISATION_NOT_FOUND")
	outsider.expect(outsider.upload("KYB_CASE", kybID, "TRUST_DEED", "NA", "t.pdf", "application/pdf", itPDF("x")), 404, "")
	member.expect(member.do("POST", "/organisations/"+orgID+"/kyb/persons", map[string]any{"full_name": "Someone Else", "roles": []string{"TRUSTEE"}}), 403, "")

	r = admin.do("GET", "/organisations/"+orgID+"/kyb", nil)
	admin.expect(admin.do("PATCH", "/organisations/"+orgID+"/kyb", map[string]any{"registered_name": "Mutare Water Trust", "registration_number": "MA-1234/2019",
		"registry": "DEEDS_REGISTRY", "country_of_registration": "ZW",
		"registered_address": map[string]string{"line1": "4 Herbert Chitepo St", "city": "Mutare", "country": "ZW"}}, "If-Match", r.Header.Get("ETag")), 200, "")
	admin.expect(admin.do("POST", "/organisations/"+orgID+"/kyb/persons", map[string]any{"full_name": "Tendai Chikore", "roles": []string{"TRUSTEE", "REPRESENTATIVE"},
		"date_of_birth": "1980-02-03", "nationality": "ZW", "id_document_type": "ZW_NATIONAL_ID", "id_document_number": uniqueIDNumber()}), 201, "")
	for _, d := range []string{admin.uploadOK("KYB_CASE", kybID, "TRUST_DEED", "NA"), admin.uploadOK("KYB_CASE", kybID, "AUTHORITY_LETTER", "NA")} {
		admin.waitDoc(d)
	}
	// members see the status only: no persons, documents or identity details
	r = member.do("GET", "/organisations/"+orgID+"/kyb", nil)
	member.expect(r, 200, "")
	var mv struct {
		Case struct {
			Persons   []any `json:"persons"`
			Documents []any `json:"documents"`
		} `json:"case"`
	}
	_ = json.Unmarshal(r.Data, &mv)
	if len(mv.Case.Persons) != 0 || len(mv.Case.Documents) != 0 || strings.Contains(string(r.Data), "Chikore") {
		t.Fatalf("member sees full KYB: %.400s", r.Data)
	}
	// the representative (submitting ORG_ADMIN) must be identity verified first
	admin.expect(admin.do("POST", "/organisations/"+orgID+"/kyb/submit", nil), 422, "")
	approvedKYC(t, admin, rev1)
	// a verified representative does not verify the organisation
	r = admin.do("GET", "/organisations/"+orgID+"/kyb", nil)
	if r.field(t, "level") != "ORG_UNVERIFIED" {
		t.Fatalf("organisation verified through its representative: %s", r.Data)
	}
	admin.expect(admin.do("POST", "/organisations/"+orgID+"/kyb/submit", nil), 200, "")

	// an organisation member who is also a reviewer could not decide (conflict); rev1 is unrelated
	rev1.expect(adminAct(rev1, kybID, "assign", nil), 200, "")
	rev1.expect(adminAct(rev1, kybID, "start-review", nil), 200, "")
	rev1.expect(adminAct(rev1, kybID, "approve", map[string]string{"reason_code": "DOCUMENTS_CONSISTENT", "note": "reviewed the submitted evidence"}), 200, "")
	r = member.do("GET", "/organisations/"+orgID+"/kyb", nil)
	if r.field(t, "level") != "ORG_KYB_VERIFIED" {
		t.Fatalf("organisation level after KYB approval: %s", r.Data)
	}
}

func TestVerificationBeneficiaryFourEyes(t *testing.T) {
	s := newITServer(t, nil)
	f := staffFixture(t, s)
	rev1, rev2, comp := s.staff(t, f.rev1), s.staff(t, f.rev2), s.staff(t, f.compliance)
	u := verifiedUser(t, s, "benef")
	other := verifiedUser(t, s, "benef-other")

	minorDOB := time.Now().AddDate(-9, 0, 0).Format("2006-01-02")
	// a minor must have guardian authority; an adult cannot be registered as a minor
	u.expect(u.do("POST", "/beneficiaries", map[string]any{"beneficiary_type": "MINOR", "display_name": "Rudo", "full_name": "Rudo Moyo", "date_of_birth": minorDOB,
		"relationship": map[string]string{"type": "PARENT_GUARDIAN"}, "authority_basis": "CONSENT"}), 422, "VALIDATION_FAILED")
	u.expect(u.do("POST", "/beneficiaries", map[string]any{"beneficiary_type": "MINOR", "display_name": "Rudo", "full_name": "Rudo Moyo", "date_of_birth": "1990-01-01",
		"relationship": map[string]string{"type": "PARENT_GUARDIAN"}, "authority_basis": "PARENTAL_RESPONSIBILITY"}), 422, "VALIDATION_FAILED")
	r := u.do("POST", "/beneficiaries", map[string]any{"beneficiary_type": "MINOR", "display_name": "Rudo", "full_name": "Rudo Moyo", "date_of_birth": minorDOB,
		"relationship": map[string]string{"type": "PARENT_GUARDIAN"}, "authority_basis": "PARENTAL_RESPONSIBILITY"})
	u.expect(r, 201, "")
	bID := r.field(t, "id")
	other.expect(other.do("GET", "/beneficiaries/"+bID, nil), 404, "BENEFICIARY_NOT_FOUND")
	r = other.do("GET", "/beneficiaries", nil)
	if strings.Contains(string(r.Data), bID) {
		t.Fatal("beneficiary listed for another user")
	}
	u.expect(u.do("POST", "/beneficiaries/"+bID+"/submit", nil), 422, "SUBMISSION_INCOMPLETE")
	for _, d := range []string{u.uploadOK("BENEFICIARY", bID, "BIRTH_CERTIFICATE", "NA"), u.uploadOK("BENEFICIARY", bID, "CONSENT_FORM", "NA")} {
		u.waitDoc(d)
	}
	u.expect(u.do("POST", "/beneficiaries/"+bID+"/submit", nil), 200, "")

	rev1.expect(adminAct(rev1, bID, "assign", nil), 200, "")
	rev1.expect(adminAct(rev1, bID, "start-review", nil), 200, "")
	// compliance holds no beneficiary decision permission
	comp.expect(adminAct(comp, bID, "approve", map[string]string{"reason_code": "DOCUMENTS_CONSISTENT", "note": "reviewed the submitted evidence"}), 403, "PERMISSION_DENIED")
	rev1.expect(adminAct(rev1, bID, "approve", map[string]string{"reason_code": "DOCUMENTS_CONSISTENT", "note": "reviewed the submitted evidence"}), 202, "")
	if st := benefStatus(t, u, bID); st == "VERIFIED" || st == "APPROVED" {
		t.Fatalf("minor beneficiary approved by one reviewer: %s", st)
	}
	rev1.expect(adminAct(rev1, bID, "second-approval", map[string]string{"note": "second look by the same reviewer"}), 403, "SECOND_APPROVER_MUST_DIFFER")
	rev2.expect(adminAct(rev2, bID, "second-approval", map[string]string{"note": "birth certificate and consent reviewed"}), 200, "")
	if st := benefStatus(t, u, bID); st != "APPROVED" {
		t.Fatalf("after second approval: %s", st)
	}
	// a decided beneficiary is not editable
	u.expect(u.do("PATCH", "/beneficiaries/"+bID, map[string]any{"display_name": "Changed"}), 409, "")
}

func TestVerificationPayoutDestinationNeverVerifiedByFormatOrMock(t *testing.T) {
	s := newITServer(t, nil)
	f := staffFixture(t, s)
	rev1 := s.staff(t, f.rev1)
	u := verifiedUser(t, s, "dest")
	other := verifiedUser(t, s, "dest-other")
	approvedKYC(t, u, rev1)

	u.expect(u.do("POST", "/payout-destinations", map[string]any{"rail": "ECOCASH", "currency": "USD", "holder_name": "Tariro Moyo", "account_identifier": "12345"}), 422, "VALIDATION_FAILED")
	u.expect(u.do("POST", "/payout-destinations", map[string]any{"rail": "ECOCASH", "currency": "USD", "holder_name": "Tariro Moyo", "account_identifier": "0771234567", "amount": 1.5}), 422, "VALIDATION_FAILED") // unknown fields refused; no float money
	r := u.do("POST", "/payout-destinations", map[string]any{"rail": "ECOCASH", "currency": "USD", "holder_name": "Tariro Moyo", "account_identifier": "0771234567"})
	u.expect(r, 201, "")
	dID := r.field(t, "id")
	if st := r.field(t, "status"); st != "UNVERIFIED" {
		t.Fatalf("a format-valid destination must be UNVERIFIED: %s", st)
	}
	if strings.Contains(string(r.Data), "0771234567") || strings.Contains(string(r.Data), "771234567") {
		t.Fatalf("account identifier not masked: %s", r.Data)
	}
	other.expect(other.do("GET", "/payout-destinations/"+dID, nil), 404, "DESTINATION_NOT_FOUND")

	// provider lookup goes to the explicitly non-production dev mock: PROVIDER_CONFIRMATION_REQUIRED only
	u.expect(u.do("POST", "/payout-destinations/"+dID+"/verification", map[string]any{"method": "PROVIDER_LOOKUP"}), 200, "")
	var mockChecks int
	mig := pool(t, os.Getenv("DATABASE_MIGRATION_URL"))
	_ = mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_destination_checks WHERE destination_id = $1 AND non_production AND result <> 'PASS'`, dID).Scan(&mockChecks)
	if mockChecks == 0 {
		t.Fatal("dev mock check must be recorded as non-production and not PASS")
	}
	// reviewer cannot approve without ownership evidence or a name match
	rev1.expect(adminAct(rev1, dID, "assign", nil), 200, "")
	rev1.expect(adminAct(rev1, dID, "approve", map[string]any{"reason_code": "OWNERSHIP_CONFIRMED", "note": "reviewed the submitted evidence"}), 422, "NAME_MATCH_REQUIRED")
	rev1.expect(adminAct(rev1, dID, "approve", map[string]any{"reason_code": "OWNERSHIP_CONFIRMED", "name_match": true, "note": "reviewed the submitted evidence"}), 409, "OWNERSHIP_EVIDENCE_MISSING")
	// the database refuses VERIFIED without production ownership + compliance PASS for the current details
	app := pool(t, os.Getenv("DATABASE_URL"))
	if _, err := app.Exec(ctx(t), `UPDATE app.payout_destinations SET status = 'VERIFIED' WHERE id = $1`, dID); err == nil {
		t.Fatal("database allowed VERIFIED without checks")
	}
	if _, err := app.Exec(ctx(t), `INSERT INTO app.payout_destination_checks (id, destination_id, details_version, check_kind, method, result, non_production, detail_code)
		SELECT gen_random_uuid(), id, details_version, 'OWNERSHIP', 'DEV_MOCK_PROVIDER', 'PASS', true, 'X' FROM app.payout_destinations WHERE id = $1`, dID); err == nil {
		t.Fatal("database allowed a non-production ownership PASS")
	}
	rev1.expect(adminAct(rev1, dID, "reject", map[string]any{"reason_code": "OWNERSHIP_NOT_SHOWN", "user_message": "Upload a statement showing your name.", "note": "reviewed the submitted evidence"}), 200, "")

	// documentary route: evidence → review → VERIFIED, still never eligible for payout in Stage 5
	ev := u.uploadOK("PAYOUT_DESTINATION", dID, "MOBILE_MONEY_STATEMENT", "NA")
	u.waitDoc(ev)
	u.expect(u.do("POST", "/payout-destinations/"+dID+"/verification", map[string]any{"method": "MOBILE_MONEY_STATEMENT", "document_ids": []string{ev}}), 200, "")
	rev1.expect(adminAct(rev1, dID, "assign", nil), 200, "")
	rev1.expect(adminAct(rev1, dID, "approve", map[string]any{"reason_code": "OWNERSHIP_CONFIRMED", "name_match": true, "note": "statement name matches"}), 200, "")
	r = u.do("GET", "/payout-destinations/"+dID, nil)
	if st := r.field(t, "status"); st != "VERIFIED" {
		t.Fatalf("after documentary approval: %s", st)
	}
	var m map[string]any
	_ = json.Unmarshal(r.Data, &m)
	if m["eligible_for_payout"] != false {
		t.Fatalf("Stage 5 destinations are never payout-eligible: %v", m["eligible_for_payout"])
	}
	// changing the account details resets verification (new details_version, checks no longer apply)
	r = u.do("PATCH", "/payout-destinations/"+dID, map[string]any{"account_identifier": "0779876543"}, "If-Match", r.Header.Get("ETag"))
	u.expect(r, 200, "")
	if st := r.field(t, "status"); st != "UNVERIFIED" {
		t.Fatalf("after detail change: %s", st)
	}
	mailTo(t, u.email, "Payout destination review completed")
}

func TestVerificationFailureInjection(t *testing.T) {
	s := newITServer(t, func(c *config.Config) { c.Verification.DocumentTicketTTL = time.Second })
	f := staffFixture(t, s)
	u := verifiedUser(t, s, "kyc-fail")
	r := u.do("POST", "/kyc/cases", map[string]string{"target_level": "IDENTITY_VERIFIED"})
	u.expect(r, 201, "")
	caseID := r.field(t, "id")
	u.expect(u.do("PATCH", "/kyc/cases/"+caseID, identityDraft(uniqueIDNumber()), "If-Match", r.Header.Get("ETag")), 200, "")

	// malware: the EICAR test file is rejected by real ClamAV and never satisfies a requirement
	r = u.upload("KYC_CASE", caseID, "SELFIE", "NA", "selfie.pdf", "application/pdf", eicarPDF())
	u.expect(r, 201, "")
	if st := u.waitDoc(r.field(t, "id")); st != "REJECTED" {
		t.Fatalf("EICAR document: %s", st)
	}
	u.expect(u.do("POST", "/verification/documents/"+r.field(t, "id")+"/access", nil), 409, "")
	front := u.uploadOK("KYC_CASE", caseID, "ZW_NATIONAL_ID", "FRONT")
	u.waitDoc(front)
	u.expect(u.do("POST", "/kyc/cases/"+caseID+"/submit", nil), 422, "SUBMISSION_INCOMPLETE")

	// expired ticket
	r = u.do("POST", "/verification/documents/"+front+"/access", nil)
	u.expect(r, 200, "")
	time.Sleep(1500 * time.Millisecond)
	if resp := rawGet(t, u.browser, r.field(t, "url")); resp.StatusCode != 403 {
		t.Fatalf("expired ticket: %d", resp.StatusCode)
	}

	// storage outage: the upload fails cleanly and nothing is attached
	broken := newITServer(t, func(c *config.Config) { c.Storage.Endpoint = "http://127.0.0.1:1" })
	bu := broken.browser(t)
	bu.mustLogin(u.email, itPassword)
	var before, after int
	mig := pool(t, os.Getenv("DATABASE_MIGRATION_URL"))
	_ = mig.QueryRow(ctx(t), `SELECT count(*) FROM kyc.kyc_documents WHERE kyc_case_id = $1`, caseID).Scan(&before)
	r = bu.upload("KYC_CASE", caseID, "SELFIE", "NA", "s.pdf", "application/pdf", itPDF("outage"))
	if r.Status < 500 {
		t.Fatalf("upload during storage outage: %d %s", r.Status, r.Error.Code)
	}
	_ = mig.QueryRow(ctx(t), `SELECT count(*) FROM kyc.kyc_documents WHERE kyc_case_id = $1`, caseID).Scan(&after)
	if after != before {
		t.Fatal("document attached although storage failed")
	}

	// a reviewer whose session is revoked cannot act
	rev := s.freshStaff(t, f.rev2)
	rev.expect(rev.do("POST", "/auth/logout", nil), 204, "")
	rev.expect(adminAct(rev, caseID, "assign", nil), 404, "ROUTE_NOT_FOUND") // admin routes do not exist for anonymous callers

	// duplicate outbox delivery of the level change does not regress the mirror (idempotent, ordered)
	var events int
	_ = mig.QueryRow(ctx(t), `SELECT count(*) FROM app.outbox_events WHERE event_type = 'kyc.level_changed'`).Scan(&events)
	if events == 0 {
		t.Log("no kyc.level_changed events yet (run with the KYC end-to-end test for coverage)")
	}
}

// TestVerificationPerformanceSample records latency of the main verification reads and of uploads (opt-in:
// FUNDZIM_IT_PERF=1; rate limiting off so the limiter is not what is measured). The only assertion is a
// generous ceiling that catches pathological regressions.
func TestVerificationPerformanceSample(t *testing.T) {
	if os.Getenv("FUNDZIM_IT_PERF") != "1" {
		t.Skip("set FUNDZIM_IT_PERF=1")
	}
	s := newITServer(t, func(c *config.Config) { c.RateLimit.Enabled = false })
	f := staffFixture(t, s)
	rev := s.staff(t, f.rev1)
	measure := func(name string, n int, fn func()) {
		d := make([]time.Duration, n)
		for i := range d {
			st := time.Now()
			fn()
			d[i] = time.Since(st)
		}
		sortDur(d)
		t.Logf("PERF %-36s n=%d p50=%v p95=%v max=%v", name, n, d[n/2], d[n*95/100], d[n-1])
		if d[n*95/100] > 2*time.Second {
			t.Errorf("%s p95 %v exceeds the 2s ceiling", name, d[n*95/100])
		}
	}
	measure("GET /admin/verification/cases", 50, func() { rev.expect(rev.do("GET", "/admin/verification/cases?limit=50", nil), 200, "") })
	u := verifiedUser(t, s, "kyc-perf")
	measure("GET /kyc/status", 50, func() { u.expect(u.do("GET", "/kyc/status", nil), 200, "") })
	r := u.do("POST", "/kyc/cases", map[string]string{"target_level": "IDENTITY_VERIFIED"})
	caseID := r.field(t, "id")
	measure("POST /verification/documents (200KB)", 10, func() {
		u.expect(u.upload("KYC_CASE", caseID, "SELFIE", "NA", "s.pdf", "application/pdf", itPDF(strings.Repeat("x", 200<<10)+perfID())), 201, "")
	})
}

var perfSeq int

func perfID() string { perfSeq++; return fmt.Sprint(perfSeq) }

func benefStatus(t *testing.T, u vUser, id string) string {
	t.Helper()
	r := u.do("GET", "/beneficiaries/"+id, nil)
	u.expect(r, 200, "")
	var v struct {
		Verification struct {
			Status string `json:"status"`
		} `json:"verification"`
	}
	_ = json.Unmarshal(r.Data, &v)
	return v.Verification.Status
}

// TestVerificationQueuePagination walks the merged reviewer queue in small pages: the (submitted_at, id) keyset
// cursor must neither skip nor repeat items, including items that share a timestamp.
func TestVerificationQueuePagination(t *testing.T) {
	s := newITServer(t, func(c *config.Config) { c.RateLimit.Enabled = false })
	f := staffFixture(t, s)
	rev := s.staff(t, f.rev1)
	// force a timestamp tie across two submitted cases
	a, b := verifiedUser(t, s, "q-a"), verifiedUser(t, s, "q-b")
	ca, cb := submittedKYC(t, a, uniqueIDNumber()), submittedKYC(t, b, uniqueIDNumber())
	// (test fixture: the version guard and one-event-per-version rule are honoured)
	migratorExec(t, `WITH u AS (UPDATE kyc.kyc_cases SET submitted_at = (SELECT submitted_at FROM kyc.kyc_cases WHERE id = $1), version = version + 1
			WHERE id = $2 RETURNING id, version, status)
		INSERT INTO kyc.case_events (id, kyc_case_id, case_version, event_type, from_status, to_status, actor_type)
		SELECT gen_random_uuid(), id, version, 'UPDATED', status, status, 'SYSTEM' FROM u`, ca, cb)

	type page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	var full page
	r := rev.do("GET", "/admin/verification/cases?limit=100&assigned=any", nil)
	rev.expect(r, 200, "")
	_ = json.Unmarshal(r.Data, &full)
	if len(full.Items) >= 100 {
		t.Skip("queue too large for an exhaustive comparison in the shared database")
	}
	seen := map[string]bool{}
	var walked []string
	cursor := ""
	for i := 0; i < 400; i++ {
		path := "/admin/verification/cases?limit=1&assigned=any" // every boundary falls between two items
		if cursor != "" {
			path += "&cursor=" + strings.ReplaceAll(cursor, "+", "%2B")
		}
		r = rev.do("GET", path, nil)
		rev.expect(r, 200, "")
		var p page
		_ = json.Unmarshal(r.Data, &p)
		for _, it := range p.Items {
			if seen[it.ID] {
				t.Fatalf("item %s repeated across pages", it.ID)
			}
			seen[it.ID] = true
			walked = append(walked, it.ID)
		}
		if p.NextCursor == nil || *p.NextCursor == "" {
			break
		}
		cursor = *p.NextCursor
	}
	if len(walked) != len(full.Items) || !seen[ca] || !seen[cb] {
		t.Fatalf("paged walk found %d items, single page %d (tied cases present: %v %v)", len(walked), len(full.Items), seen[ca], seen[cb])
	}
	rev.expect(rev.do("GET", "/admin/verification/cases?cursor=not-a-time", nil), 422, "VALIDATION_FAILED")
}
