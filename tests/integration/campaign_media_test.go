//go:build integration

// Stage 6 stream M: campaign media against the REAL local stack. The API runs in-process (newITServer); uploads
// land in the Garage public-media quarantine, the RUNNING worker container scans them with real ClamAV, and its
// campaigns media consumer decodes, re-encodes and re-uploads the derivative, which is scanned again before the
// media is APPROVED. Scanner outage and malware verdicts are injected deterministically by claiming the scan
// in-process (the storage scan claim is atomic, so whoever claims first scans; the tests retry when the container
// wins the race). Campaign rows are created through the migrator pool, walking the lifecycle edges with one
// campaign_status_history row per version. Requires `docker compose up -d --build` with current images.
package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/auth"
	"github.com/Fatifizo/fundzim/internal/campaigns/media"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	pstorage "github.com/Fatifizo/fundzim/internal/platform/storage"
	"github.com/Fatifizo/fundzim/internal/storage"
)

// ---- fixtures -------------------------------------------------------------------------------------------

type cmUser struct {
	*browser
	id string
}

func cmOwner(t *testing.T, s *itServer, prefix string) cmUser {
	t.Helper()
	email := uniqueEmail(prefix)
	b := s.browser(t)
	b.register(email, itPassword, "Media Owner "+ids.New()[30:])
	b.expect(b.do("POST", "/auth/verify-email", map[string]string{"token": tokenFrom(t, mailTo(t, email, "Confirm your FundZim email"))}), 200, "")
	b.mustLogin(email, itPassword)
	var me auth.Me
	_ = json.Unmarshal(b.do("GET", "/me", nil).Data, &me)
	if me.ID == "" {
		t.Fatal("no user id")
	}
	return cmUser{browser: b, id: me.ID}
}

type cmCampaign struct{ id, slug, owner string }

func cmMigrator(t *testing.T) *pgxpool.Pool {
	t.Helper()
	need(t, "DATABASE_MIGRATION_URL")
	return pool(t, os.Getenv("DATABASE_MIGRATION_URL"))
}

// cmMakeDraft inserts a DRAFT campaign (with its history row, in one transaction).
func cmMakeDraft(t *testing.T, ownerID string) cmCampaign {
	t.Helper()
	c := cmCampaign{id: ids.New(), owner: ownerID}
	code := cuPublicCode()
	c.slug = "it-media-" + strings.ToLower(code)
	err := pgx.BeginFunc(ctx(t), cmMigrator(t), func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx(t), `INSERT INTO app.campaigns (id, public_code, slug, owner_user_id, created_by, category_code, title, summary,
			story, goal_amount_minor, goal_currency, status, risk_tier, policy_version)
			SELECT $1, $2, $3, $4, $4, 'EDUCATION', 'Integration media campaign', 'A synthetic campaign used by the media tests.',
			  'Synthetic story for media tests.', 50000, 'USD', 'DRAFT', k.default_risk_tier, 'campaign-v1'
			FROM app.campaign_categories k WHERE k.code = 'EDUCATION'`, c.id, code, c.slug, ownerID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx(t), `INSERT INTO app.campaign_status_history (id, campaign_id, campaign_version, event_type, from_status, to_status,
			actor_type, reason_code) VALUES ($1, $2, 1, 'IT_FIXTURE', NULL, 'DRAFT', 'SYSTEM', 'IT_FIXTURE')`, ids.New(), c.id)
		return err
	})
	if err != nil {
		t.Fatalf("campaign fixture: %v", err)
	}
	return c
}

// cmWalk moves a campaign through the given statuses (one history row per version). When the walk reaches
// APPROVED it pins a new approved version whose media_ids are mediaIDs.
func cmWalk(t *testing.T, c cmCampaign, mediaIDs []string, to ...string) {
	t.Helper()
	sets := map[string]string{
		"SUBMITTED": "submitted_at = now(), fundraising_basis = 'SELF_FUNDRAISING'", "UNDER_REVIEW": "updated_at = now()",
		"APPROVED": "approved_at = now(), approved_version_id = $3", "ACTIVE": "published_at = now(), paused_at = NULL, suspended_at = NULL",
		"SUSPENDED": "suspended_at = now()", "PAUSED": "paused_at = now()",
	}
	if mediaIDs == nil {
		mediaIDs = []string{}
	}
	err := pgx.BeginFunc(ctx(t), cmMigrator(t), func(tx pgx.Tx) error {
		for _, st := range to {
			var from string
			var n int
			if err := tx.QueryRow(ctx(t), `SELECT status, (SELECT coalesce(max(version_number), 0) FROM app.campaign_versions WHERE campaign_id = $1)
				FROM app.campaigns WHERE id = $1`, c.id).Scan(&from, &n); err != nil {
				return err
			}
			vid := ids.New()
			if st == "APPROVED" {
				sum := sha256.Sum256([]byte(c.id + fmt.Sprint(n)))
				if _, err := tx.Exec(ctx(t), `INSERT INTO app.campaign_versions (id, campaign_id, version_number, change_kind, title, summary, story,
					category_code, goal_amount_minor, goal_currency, media_ids, content_sha256, created_by)
					SELECT $1, id, $3, 'SUBMISSION', title, summary, story, category_code, goal_amount_minor, goal_currency, $4::uuid[], $5, created_by
					FROM app.campaigns WHERE id = $2`, vid, c.id, n+1, mediaIDs, sum[:]); err != nil {
					return err
				}
			}
			args := []any{c.id, st}
			if strings.Contains(sets[st], "$3") {
				args = append(args, vid)
			}
			var v int
			if err := tx.QueryRow(ctx(t), `UPDATE app.campaigns SET status = $2, `+sets[st]+` WHERE id = $1 RETURNING version`, args...).Scan(&v); err != nil {
				return fmt.Errorf("%s → %s: %w", from, st, err)
			}
			if _, err := tx.Exec(ctx(t), `INSERT INTO app.campaign_status_history (id, campaign_id, campaign_version, event_type, from_status,
				to_status, actor_type, reason_code) VALUES ($1, $2, $3, 'IT_FIXTURE', $4, $5, 'SYSTEM', 'IT_FIXTURE')`, ids.New(), c.id, v, from, st); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("campaign walk: %v", err)
	}
}

func cmPublish(t *testing.T, c cmCampaign, mediaIDs ...string) {
	t.Helper()
	cmWalk(t, c, mediaIDs, "SUBMITTED", "UNDER_REVIEW", "APPROVED", "ACTIVE")
}

// ---- image fixtures -------------------------------------------------------------------------------------

func cmImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(40 + x), uint8(60 + y), 120, 255})
		}
	}
	return img
}

// cmJPEGWithGPS returns a 48x24 JPEG carrying an APP1 Exif segment with a camera make, orientation 6 and a GPS IFD
// containing a recognisable location marker.
func cmJPEGWithGPS(t *testing.T) []byte {
	t.Helper()
	var j bytes.Buffer
	if err := jpeg.Encode(&j, cmImage(48, 24), &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	be := binary.BigEndian
	var tf bytes.Buffer
	tf.WriteString("MM")
	_ = binary.Write(&tf, be, uint16(42))
	_ = binary.Write(&tf, be, uint32(8))
	mk := []byte("IT-CAMERA-MAKE\x00")
	makeOff := uint32(8 + 2 + 3*12 + 4)
	gpsOff := makeOff + uint32(len(mk))
	entry := func(tag, typ uint16, count, value uint32) {
		_ = binary.Write(&tf, be, tag)
		_ = binary.Write(&tf, be, typ)
		_ = binary.Write(&tf, be, count)
		_ = binary.Write(&tf, be, value)
	}
	_ = binary.Write(&tf, be, uint16(3))
	entry(0x010F, 2, uint32(len(mk)), makeOff)
	entry(0x0112, 3, 1, 6<<16)
	entry(0x8825, 4, 1, gpsOff)
	_ = binary.Write(&tf, be, uint32(0))
	tf.Write(mk)
	area := []byte("ASCII\x00\x00\x00GPS-MARKER-HARARE-17.82S-31.05E")
	_ = binary.Write(&tf, be, uint16(2))
	entry(0x0001, 2, 2, uint32('S')<<24)
	entry(0x001C, 7, uint32(len(area)), gpsOff+2+2*12+4)
	_ = binary.Write(&tf, be, uint32(0))
	tf.Write(area)
	payload := append([]byte("Exif\x00\x00"), tf.Bytes()...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	be.PutUint16(seg[2:], uint16(len(payload)+2))
	src := j.Bytes()
	out := append(append(append([]byte{}, src[:2]...), seg...), payload...)
	return append(out, src[2:]...)
}

func cmPNGChunk(typ string, data []byte) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.BigEndian, uint32(len(data)))
	b.WriteString(typ)
	b.Write(data)
	_ = binary.Write(&b, binary.BigEndian, crc32IEEE(append([]byte(typ), data...)))
	return b.Bytes()
}

func crc32IEEE(b []byte) uint32 {
	crc := ^uint32(0)
	for _, c := range b {
		crc ^= uint32(c)
		for i := 0; i < 8; i++ {
			if crc&1 == 1 {
				crc = crc>>1 ^ 0xEDB88320
			} else {
				crc >>= 1
			}
		}
	}
	return ^crc
}

// cmPNGWithText returns a PNG with an ancillary tEXt chunk carrying text (e.g. the EICAR test string). A unique
// pixel row makes every fixture's bytes distinct.
func cmPNGWithText(t *testing.T, text []byte) []byte {
	t.Helper()
	img := cmImage(24, 12)
	seed := []byte(ids.New())
	for x := 0; x < 24; x++ {
		img.Set(x, 11, color.RGBA{seed[x%len(seed)], seed[(x+7)%len(seed)], 3, 255})
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	src := b.Bytes()
	out := append([]byte{}, src[:33]...)
	out = append(out, cmPNGChunk("tEXt", append([]byte("Comment\x00"), text...))...)
	return append(out, src[33:]...)
}

// ---- HTTP helpers ---------------------------------------------------------------------------------------

func cmUpload(t *testing.T, b *browser, campaignID string, fields [][2]string, filename, ctype string, content []byte) apiResp {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, kv := range fields {
		_ = mw.WriteField(kv[0], kv[1])
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	h.Set("Content-Type", ctype)
	fw, _ := mw.CreatePart(h)
	_, _ = fw.Write(content)
	_ = mw.Close()
	req, _ := http.NewRequest("POST", b.s.srv.URL+"/api/v1/campaigns/"+campaignID+"/media", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Forwarded-For", b.ip)
	req.Header.Set("X-CSRF-Token", b.cookie("fz_csrf"))
	resp, err := b.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := apiResp{Status: resp.StatusCode, Header: resp.Header}
	var env struct {
		Data  json.RawMessage `json:"data"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("upload: non-JSON %d: %s", resp.StatusCode, raw)
	}
	out.Data = env.Data
	_ = json.Unmarshal(env.Error, &out.Error)
	if bytes.Contains(raw, []byte("passwd")) || bytes.Contains(raw, []byte(".exe")) {
		t.Fatalf("the client filename leaked into the response: %s", raw)
	}
	return out
}

func cmFields(kind, alt string) [][2]string {
	return [][2]string{{"kind", kind}, {"alt_text", alt}, {"depicts_minor", "false"}}
}

const cmEvilName = "../../etc/passwd.jpg.exe"

type cmMedia struct {
	ID             string  `json:"id"`
	Kind           string  `json:"kind"`
	Status         string  `json:"status"`
	RejectedReason *string `json:"rejected_reason"`
	Width          *int    `json:"width"`
	Height         *int    `json:"height"`
	Version        int     `json:"version"`
	ContentURL     string  `json:"content_url"`
	AltText        string  `json:"alt_text"`
	Position       int     `json:"position"`
}

func cmUploadOK(t *testing.T, u cmUser, campaignID, kind, ctype string, content []byte) cmMedia {
	t.Helper()
	r := cmUpload(t, u.browser, campaignID, cmFields(kind, "A school building in Harare"), cmEvilName, ctype, content)
	u.expect(r, 201, "")
	var m cmMedia
	if err := json.Unmarshal(r.Data, &m); err != nil {
		t.Fatal(err)
	}
	if m.Status != media.StatusQuarantined {
		t.Fatalf("new media status %s", m.Status)
	}
	return m
}

func cmList(t *testing.T, b *browser, path string) []cmMedia {
	t.Helper()
	r := b.do("GET", path, nil)
	b.expect(r, 200, "")
	var out struct {
		Media []cmMedia `json:"media"`
	}
	if err := json.Unmarshal(r.Data, &out); err != nil {
		t.Fatal(err)
	}
	return out.Media
}

func cmFind(list []cmMedia, id string) (cmMedia, bool) {
	for _, m := range list {
		if m.ID == id {
			return m, true
		}
	}
	return cmMedia{}, false
}

// cmWait polls the owner list until the media reaches a final status.
func cmWait(t *testing.T, u cmUser, campaignID, mediaID string, timeout time.Duration) cmMedia {
	t.Helper()
	var last cmMedia
	waitFor(t, timeout, "media "+mediaID+" final", func() bool {
		m, ok := cmFind(cmList(t, u.browser, "/campaigns/"+campaignID+"/media"), mediaID)
		last = m
		if !ok {
			t.Fatalf("media %s not listed", mediaID)
		}
		time.Sleep(300 * time.Millisecond)
		return m.Status == media.StatusApproved || m.Status == media.StatusRejected || m.Status == media.StatusRemoved
	})
	return last
}

type cmRaw struct {
	status int
	header http.Header
	body   []byte
}

func cmGet(t *testing.T, b *browser, url string, hdr ...string) cmRaw {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	c := http.DefaultClient
	if b != nil {
		req.Header.Set("X-Forwarded-For", b.ip)
		c = b.c
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return cmRaw{status: resp.StatusCode, header: resp.Header, body: body}
}

func cmOwnerContent(t *testing.T, u cmUser, c cmCampaign, mediaID string) cmRaw {
	return cmGet(t, u.browser, u.s.srv.URL+"/api/v1/campaigns/"+c.id+"/media/"+mediaID+"/content")
}

func cmPublicContent(t *testing.T, s *itServer, slug, mediaID string, hdr ...string) cmRaw {
	return cmGet(t, nil, s.srv.URL+"/api/v1/public/campaigns/"+slug+"/media/"+mediaID, hdr...)
}

func cmCount(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool(t, workerURL(t)).QueryRow(ctx(t), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// ---- in-process scan claim (scanner outage / malware verdict injection) --------------------------------

type cmScanner struct {
	called  atomic.Bool
	verdict storage.Verdict
	err     error
}

func (s *cmScanner) Scan(_ context.Context, r io.Reader) (storage.Verdict, error) {
	s.called.Store(true)
	_, _ = io.Copy(io.Discard, r)
	return s.verdict, s.err
}

func cmAllBlobs(t *testing.T) *pstorage.Client {
	t.Helper()
	all := map[pstorage.Class]pstorage.Credential{}
	for class, c := range creds() {
		all[class] = pstorage.Credential{Bucket: c.bucket, AccessKeyID: c.id, SecretAccessKey: c.secret}
	}
	blobs, err := pstorage.New(pstorage.Options{Endpoint: os.Getenv("STORAGE_ENDPOINT"), Region: os.Getenv("STORAGE_REGION"),
		ForcePathStyle: true, Credentials: all})
	if err != nil {
		t.Fatal(err)
	}
	return blobs
}

// cmWorkerStorage is a storage service on the worker pool with scanner sc (as the worker process has).
func cmWorkerStorage(t *testing.T, sc storage.Scanner) *storage.Service {
	t.Helper()
	need(t, "POSTGRES_WORKER_PASSWORD", "STORAGE_ENDPOINT", "STORAGE_PUBLIC_BUCKET")
	st, err := storage.New(storage.Config{AppEnv: "test", UploadMaxBytes: 10 << 20, ScanTimeout: 20 * time.Second, TicketKey: randKey(t)},
		storage.Deps{Pool: pool(t, workerURL(t)), Blobs: cmAllBlobs(t), Scanner: sc, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func cmObjectOf(t *testing.T, mediaID string) string {
	t.Helper()
	var id string
	if err := pool(t, workerURL(t)).QueryRow(ctx(t), `SELECT stored_object_id::text FROM app.campaign_media WHERE id = $1`, mediaID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// cmUploadClaimed uploads content and claims its scan in-process with sc before the worker container does
// (retrying with a fresh upload when the container wins the race). It returns the media id.
func cmUploadClaimed(t *testing.T, u cmUser, c cmCampaign, kind string, content func() []byte, sc *cmScanner) string {
	t.Helper()
	st := cmWorkerStorage(t, sc)
	for attempt := 0; attempt < 6; attempt++ {
		sc.called.Store(false)
		m := cmUploadOK(t, u, c.id, kind, "image/png", content())
		if err := st.ScanObject(ctx(t), cmObjectOf(t, m.ID)); err != nil {
			t.Fatal(err)
		}
		if sc.called.Load() {
			return m.ID
		}
		t.Logf("worker container claimed the scan first (attempt %d); retrying", attempt+1)
		// the container's media pipeline finishes it; remove it so the cover slot is free again
		cmWait(t, u, c.id, m.ID, 120*time.Second)
		u.expect(u.do("DELETE", "/campaigns/"+c.id+"/media/"+m.ID, nil), 204, "")
	}
	t.Fatal("could not claim a scan in-process")
	return ""
}

func cmStatus(t *testing.T, mediaID string) (string, string) {
	t.Helper()
	var st, reason string
	if err := pool(t, workerURL(t)).QueryRow(ctx(t), `SELECT status, coalesce(rejected_reason, '') FROM app.campaign_media WHERE id = $1`,
		mediaID).Scan(&st, &reason); err != nil {
		t.Fatal(err)
	}
	return st, reason
}

// ===== tests ==============================================================================================

// Upload → real ClamAV scan → decode/re-encode → derivative scan → APPROVED; the served bytes carry no metadata;
// public serving only for a live campaign and only for media in its approved version.
func TestCampaignMediaApprovedStrippedAndServed(t *testing.T) {
	s := newITServer(t, nil)
	u := cmOwner(t, s, "media-owner")
	c := cmMakeDraft(t, u.id)

	src := cmJPEGWithGPS(t)
	if !bytes.Contains(src, []byte("GPS-MARKER-HARARE")) {
		t.Fatal("fixture has no GPS marker")
	}
	cover := cmUploadOK(t, u, c.id, media.KindCover, "image/jpeg", src)
	got := cmWait(t, u, c.id, cover.ID, 120*time.Second)
	if got.Status != media.StatusApproved {
		t.Fatalf("cover status %s (%v)", got.Status, got.RejectedReason)
	}
	// orientation 6 applied: 48x24 → 24x48
	if got.Width == nil || *got.Width != 24 || *got.Height != 48 || got.ContentURL == "" {
		t.Fatalf("approved media view: %+v", got)
	}
	// owner preview: the derivative only, without EXIF/GPS, with strict headers
	r := cmOwnerContent(t, u, c, cover.ID)
	if r.status != 200 || r.header.Get("Content-Type") != "image/jpeg" || r.header.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(r.header.Get("Content-Security-Policy"), "default-src 'none'") || r.header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("owner content: %d %v", r.status, r.header)
	}
	for _, needle := range []string{"Exif", "GPS-MARKER", "IT-CAMERA-MAKE", "passwd"} {
		if bytes.Contains(r.body, []byte(needle)) {
			t.Fatalf("served bytes contain %q", needle)
		}
	}
	if bytes.Equal(r.body, src) {
		t.Fatal("the original was served instead of the derivative")
	}
	img, err := jpeg.Decode(bytes.NewReader(r.body))
	if err != nil || img.Bounds().Dx() != 24 || img.Bounds().Dy() != 48 {
		t.Fatalf("served image: %v %v", err, img.Bounds())
	}
	// timeline, audit and outbox
	if n := cmCount(t, `SELECT count(*) FROM app.campaign_media_events WHERE media_id = $1`, cover.ID); n < 4 {
		t.Fatalf("timeline rows: %d", n)
	}
	for _, ev := range []string{"UPLOADED", "QUARANTINED", "PROCESSED", "APPROVED"} {
		if cmCount(t, `SELECT count(*) FROM app.campaign_media_events WHERE media_id = $1 AND event_type = $2`, cover.ID, ev) != 1 {
			t.Errorf("timeline event %s missing", ev)
		}
	}
	if cmCount(t, `SELECT count(*) FROM audit.audit_events WHERE action = 'campaign.media_added' AND target_id = $1`, cover.ID) != 1 {
		t.Error("audit campaign.media_added missing")
	}
	if cmCount(t, `SELECT count(*) FROM app.outbox_events WHERE event_type = 'campaigns.media_added' AND payload->>'media_id' = $1`, cover.ID) != 1 {
		t.Error("outbox campaigns.media_added missing")
	}
	// the client filename is stored nowhere
	if cmCount(t, `SELECT count(*) FROM app.stored_objects o JOIN app.campaign_media m ON o.id IN (m.stored_object_id, m.processed_object_id)
		 WHERE m.id = $1 AND (o::text LIKE '%passwd%' OR o::text LIKE '%.exe%')`, cover.ID) != 0 ||
		cmCount(t, `SELECT count(*) FROM app.campaign_media WHERE id = $1 AND campaign_media::text LIKE '%passwd%'`, cover.ID) != 0 {
		t.Fatal("client filename stored")
	}
	// MediaInfo for the core
	mi := s.d.Verification.CampaignMedia
	rd, err := mi.Readiness(ctx(t), c.id)
	if err != nil || !rd.CoverApproved || rd.Pending != 0 || rd.Rejected != 0 {
		t.Fatalf("readiness %+v %v", rd, err)
	}

	// not public while DRAFT
	if p := cmPublicContent(t, s, c.slug, cover.ID); p.status != 404 {
		t.Fatalf("draft campaign media served publicly: %d", p.status)
	}
	approved, err := mi.ApprovedIDs(ctx(t), c.id)
	if err != nil || len(approved) != 1 || approved[0] != cover.ID {
		t.Fatalf("approved ids %v %v", approved, err)
	}
	cmPublish(t, c, approved...)
	p := cmPublicContent(t, s, c.slug, cover.ID)
	if p.status != 200 || !bytes.Equal(p.body, r.body) || p.header.Get("Content-Type") != "image/jpeg" ||
		p.header.Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(p.header.Get("Cache-Control"), "public, max-age=") ||
		!strings.Contains(p.header.Get("Content-Security-Policy"), "default-src 'none'") || p.header.Get("ETag") == "" ||
		p.header.Get("Set-Cookie") != "" {
		t.Fatalf("public content: %d %v", p.status, p.header)
	}
	if nm := cmPublicContent(t, s, c.slug, cover.ID, "If-None-Match", p.header.Get("ETag")); nm.status != 304 || len(nm.body) != 0 {
		t.Fatalf("conditional request: %d", nm.status)
	}
	// wrong slug / unknown id / malformed id → 404
	for _, path := range [][2]string{{"no-such-campaign-zz", cover.ID}, {c.slug, ids.New()}, {c.slug, "not-a-uuid"}} {
		if q := cmPublicContent(t, s, path[0], path[1]); q.status != 404 {
			t.Fatalf("public %v: %d", path, q.status)
		}
	}

	// a gallery image added to the LIVE campaign is a material change; once approved it is still not public,
	// because it is not part of the approved version
	gal := cmUploadOK(t, u, c.id, media.KindGallery, "image/png", cmPNGWithText(t, []byte("gallery")))
	if cmCount(t, `SELECT count(*) FROM app.campaigns WHERE id = $1 AND re_review_required`, c.id) != 1 {
		t.Fatal("media on a live campaign did not request a re-review")
	}
	if g := cmWait(t, u, c.id, gal.ID, 120*time.Second); g.Status != media.StatusApproved {
		t.Fatalf("gallery status %s", g.Status)
	}
	if q := cmPublicContent(t, s, c.slug, gal.ID); q.status != 404 {
		t.Fatalf("media outside the approved version served publicly: %d", q.status)
	}
	if q := cmOwnerContent(t, u, c, gal.ID); q.status != 200 {
		t.Fatalf("owner preview of new gallery: %d", q.status)
	}
	// suspension hides everything
	cmWalk(t, c, nil, "SUSPENDED")
	if q := cmPublicContent(t, s, c.slug, cover.ID); q.status != 404 {
		t.Fatalf("suspended campaign media served publicly: %d", q.status)
	}
	cmWalk(t, c, nil, "ACTIVE")
	if q := cmPublicContent(t, s, c.slug, cover.ID); q.status != 200 {
		t.Fatalf("reactivated: %d", q.status)
	}
	// owner removal: soft, never served again, objects soft-deleted, audit + outbox
	u.expect(u.do("DELETE", "/campaigns/"+c.id+"/media/"+cover.ID, nil), 204, "")
	if q := cmPublicContent(t, s, c.slug, cover.ID); q.status != 404 {
		t.Fatalf("removed media served publicly: %d", q.status)
	}
	if q := cmOwnerContent(t, u, c, cover.ID); q.status != 404 {
		t.Fatalf("removed media served to owner: %d", q.status)
	}
	if st, _ := cmStatus(t, cover.ID); st != media.StatusRemoved {
		t.Fatalf("status after delete %s", st)
	}
	if cmCount(t, `SELECT count(*) FROM app.stored_objects o JOIN app.campaign_media m ON o.id IN (m.stored_object_id, m.processed_object_id)
		 WHERE m.id = $1 AND o.scan_status = 'DELETED'`, cover.ID) != 2 {
		t.Error("objects of removed media not soft-deleted")
	}
	if cmCount(t, `SELECT count(*) FROM audit.audit_events WHERE action = 'campaign.media_removed' AND target_id = $1`, cover.ID) != 1 ||
		cmCount(t, `SELECT count(*) FROM app.outbox_events WHERE event_type = 'campaigns.media_removed' AND payload->>'media_id' = $1`, cover.ID) != 1 {
		t.Error("removal audit/outbox missing")
	}
}

// EICAR: a raw EICAR file is refused at upload (it is not an image); a malware verdict on an image rejects the
// media; and an EICAR string hidden in PNG metadata (which ClamAV 1.5 does not flag inside a valid image) never
// reaches the served bytes because the derivative is re-encoded from pixels only.
func TestCampaignMediaEICARRejected(t *testing.T) {
	s := newITServer(t, nil)
	u := cmOwner(t, s, "media-eicar")
	c := cmMakeDraft(t, u.id)

	r := cmUpload(t, u.browser, c.id, cmFields(media.KindGallery, "Not an image"), "eicar.png", "image/png", storage.EICAR())
	u.expect(r, 422, "UNSUPPORTED_FILE_TYPE")
	if n := cmCount(t, `SELECT count(*) FROM app.campaign_media WHERE campaign_id = $1`, c.id); n != 0 {
		t.Fatalf("media rows after a refused upload: %d", n)
	}

	// malware verdict from the scanner → media REJECTED (MALWARE_DETECTED), never served
	sc := &cmScanner{verdict: storage.Verdict{Clean: false, Signature: "Eicar-Test-Signature", Engine: "it-injected-verdict"}}
	id := cmUploadClaimed(t, u, c, media.KindCover, func() []byte { return cmPNGWithText(t, storage.EICAR()) }, sc)
	got := cmWait(t, u, c.id, id, 60*time.Second)
	if got.Status != media.StatusRejected || got.RejectedReason == nil || *got.RejectedReason != media.ReasonMalware {
		t.Fatalf("media after malware verdict: %+v", got)
	}
	if q := cmOwnerContent(t, u, c, id); q.status != 404 {
		t.Fatalf("rejected media served: %d", q.status)
	}
	rd, _ := s.d.Verification.CampaignMedia.Readiness(ctx(t), c.id)
	if rd.CoverApproved || rd.Rejected != 1 {
		t.Fatalf("readiness after rejection %+v", rd)
	}
	// a rejected cover still holds the cover slot until the owner removes it
	u.expect(cmUpload(t, u.browser, c.id, cmFields(media.KindCover, "Another cover"), "c.png", "image/png", cmPNGWithText(t, nil)),
		409, "COVER_ALREADY_EXISTS")
	u.expect(u.do("DELETE", "/campaigns/"+c.id+"/media/"+id, nil), 204, "")

	// EICAR inside a tEXt chunk, real ClamAV: the original scans CLEAN, the served derivative has no trace of it
	hidden := cmUploadOK(t, u, c.id, media.KindCover, "image/png", cmPNGWithText(t, storage.EICAR()))
	got = cmWait(t, u, c.id, hidden.ID, 120*time.Second)
	if got.Status == media.StatusApproved {
		body := cmOwnerContent(t, u, c, hidden.ID).body
		if len(body) == 0 || bytes.Contains(body, storage.EICAR()) || bytes.Contains(body, []byte("tEXt")) {
			t.Fatal("EICAR/tEXt survived into the served derivative")
		}
		t.Logf("real ClamAV passed a PNG with EICAR in a tEXt chunk; the re-encode removed it from the served bytes")
	} else if got.RejectedReason == nil || *got.RejectedReason != media.ReasonMalware {
		t.Fatalf("hidden EICAR media: %+v", got)
	}
}

// A scanner outage leaves the media SCANNING (never APPROVED) while the storage rescan job retries; once the
// scanner is back the worker container's rescan picks the object up and the media is approved.
func TestCampaignMediaScannerOutageNeverApproves(t *testing.T) {
	s := newITServer(t, nil)
	u := cmOwner(t, s, "media-outage")
	c := cmMakeDraft(t, u.id)
	sc := &cmScanner{err: &storage.ScanError{Code: storage.ScanErrUnavailable, Err: errors.New("injected: clamd unreachable")}}
	id := cmUploadClaimed(t, u, c, media.KindCover, func() []byte { return cmPNGWithText(t, []byte("outage")) }, sc)
	obj := cmObjectOf(t, id)
	var scan string
	if err := pool(t, workerURL(t)).QueryRow(ctx(t), `SELECT scan_status FROM app.stored_objects WHERE id = $1`, obj).Scan(&scan); err != nil {
		t.Fatal(err)
	}
	if scan != storage.StatusFailedScan {
		t.Fatalf("object after outage: %s", scan)
	}
	// the media pipeline sees the failed scan: SCANNING, no derivative, not served, never approved
	ms := media.New(media.Deps{Pool: pool(t, workerURL(t)), Storage: cmWorkerStorage(t, sc), MaxUploadBytes: 10 << 20})
	for i := 0; i < 3; i++ {
		if err := ms.Advance(ctx(t), id); err != nil {
			t.Fatal(err)
		}
	}
	if st, _ := cmStatus(t, id); st != media.StatusScanning {
		t.Fatalf("media during outage: %s", st)
	}
	if cmCount(t, `SELECT count(*) FROM app.campaign_media WHERE id = $1 AND processed_object_id IS NOT NULL`, id) != 0 {
		t.Fatal("a derivative was produced from an unscanned object")
	}
	if q := cmOwnerContent(t, u, c, id); q.status != 404 {
		t.Fatalf("unscanned media served: %d", q.status)
	}
	rd, _ := s.d.Verification.CampaignMedia.Readiness(ctx(t), c.id)
	if rd.CoverApproved || rd.Pending != 1 {
		t.Fatalf("readiness during outage %+v", rd)
	}
	// recovery: the worker container's rescan job (every 30 s, after the backoff) scans it with ClamAV
	got := cmWait(t, u, c.id, id, 180*time.Second)
	if got.Status != media.StatusApproved {
		t.Fatalf("after recovery: %+v", got)
	}
	if n := cmCount(t, `SELECT scan_attempts FROM app.stored_objects WHERE id = $1`, obj); n < 2 {
		t.Fatalf("approved after %d scan attempts", n)
	}
}

// Another user (and anonymous callers) get 404 for everything; staff need the right permission; validation codes.
func TestCampaignMediaIDORAndValidation(t *testing.T) {
	s := newITServer(t, nil)
	f := staffFixture(t, s)
	owner := cmOwner(t, s, "media-idor-owner")
	other := cmOwner(t, s, "media-idor-other")
	c := cmMakeDraft(t, owner.id)
	m := cmUploadOK(t, owner, c.id, media.KindCover, "image/png", cmPNGWithText(t, []byte("idor")))
	cmWait(t, owner, c.id, m.ID, 120*time.Second)

	// stranger: 404 everywhere; the upload is refused before the file is read (no stored object)
	before := cmCount(t, `SELECT count(*) FROM app.stored_objects WHERE uploaded_by_user_id = $1`, other.id)
	other.expect(cmUpload(t, other.browser, c.id, cmFields(media.KindGallery, "Sneaky upload"), "x.png", "image/png", cmPNGWithText(t, nil)),
		404, "CAMPAIGN_NOT_FOUND")
	if cmCount(t, `SELECT count(*) FROM app.stored_objects WHERE uploaded_by_user_id = $1`, other.id) != before {
		t.Fatal("a stranger's upload reached storage")
	}
	other.expect(other.do("GET", "/campaigns/"+c.id+"/media", nil), 404, "CAMPAIGN_NOT_FOUND")
	other.expect(other.do("PATCH", "/campaigns/"+c.id+"/media/"+m.ID, map[string]any{"alt_text": "Changed by a stranger"}, "If-Match", `"3"`), 404, "")
	other.expect(other.do("DELETE", "/campaigns/"+c.id+"/media/"+m.ID, nil), 404, "CAMPAIGN_NOT_FOUND")
	if q := cmGet(t, other.browser, s.srv.URL+"/api/v1/campaigns/"+c.id+"/media/"+m.ID+"/content"); q.status != 404 {
		t.Fatalf("stranger content: %d", q.status)
	}
	// the stranger's own campaign cannot address the owner's media
	oc := cmMakeDraft(t, other.id)
	other.expect(other.do("DELETE", "/campaigns/"+oc.id+"/media/"+m.ID, nil), 404, "MEDIA_NOT_FOUND")
	if q := cmGet(t, other.browser, s.srv.URL+"/api/v1/campaigns/"+oc.id+"/media/"+m.ID+"/content"); q.status != 404 {
		t.Fatalf("cross-campaign content: %d", q.status)
	}
	// anonymous
	if q := cmGet(t, nil, s.srv.URL+"/api/v1/campaigns/"+c.id+"/media"); q.status != 401 {
		t.Fatalf("anonymous list: %d", q.status)
	}

	// validation (all before the file is stored)
	cases := []struct {
		fields [][2]string
		status int
		code   string
	}{
		{[][2]string{{"kind", "GALLERY"}, {"alt_text", "A child at school"}, {"depicts_minor", "true"}}, 422, "MINOR_MEDIA_NOT_SUPPORTED"},
		{[][2]string{{"kind", "GALLERY"}, {"alt_text", "<b>bold</b> alt"}, {"depicts_minor", "false"}}, 422, "VALIDATION_FAILED"},
		{[][2]string{{"kind", "BANNER"}, {"alt_text", "A school"}, {"depicts_minor", "false"}}, 422, "VALIDATION_FAILED"},
		{[][2]string{{"kind", "GALLERY"}, {"alt_text", "A school"}}, 400, "MALFORMED_UPLOAD"},
		{[][2]string{{"kind", "COVER"}, {"alt_text", "Second cover"}, {"depicts_minor", "false"}}, 409, "COVER_ALREADY_EXISTS"},
	}
	for _, tc := range cases {
		owner.expect(cmUpload(t, owner.browser, c.id, tc.fields, "x.png", "image/png", cmPNGWithText(t, nil)), tc.status, tc.code)
	}
	// declared type must match the content
	owner.expect(cmUpload(t, owner.browser, c.id, cmFields(media.KindGallery, "Mismatched type"), "x.png", "image/png", cmJPEGWithGPS(t)),
		422, "FILE_TYPE_MISMATCH")

	// PATCH: If-Match required and checked; HTML refused
	owner.expect(owner.do("PATCH", "/campaigns/"+c.id+"/media/"+m.ID, map[string]any{"alt_text": "Students outside"}), 400, "PRECONDITION_REQUIRED")
	cur, _ := cmFind(cmList(t, owner.browser, "/campaigns/"+c.id+"/media"), m.ID)
	owner.expect(owner.do("PATCH", "/campaigns/"+c.id+"/media/"+m.ID, map[string]any{"alt_text": "Students outside"}, "If-Match",
		fmt.Sprintf(`"%d"`, cur.Version-1)), 409, "MEDIA_STATE_CHANGED")
	owner.expect(owner.do("PATCH", "/campaigns/"+c.id+"/media/"+m.ID, map[string]any{"alt_text": "<script>x</script>"}, "If-Match",
		fmt.Sprintf(`"%d"`, cur.Version)), 422, "VALIDATION_FAILED")
	r := owner.do("PATCH", "/campaigns/"+c.id+"/media/"+m.ID, map[string]any{"alt_text": "Students outside the school"}, "If-Match",
		fmt.Sprintf(`"%d"`, cur.Version))
	owner.expect(r, 200, "")
	if r.field(t, "alt_text") != "Students outside the school" || r.Header.Get("ETag") != fmt.Sprintf(`"%d"`, cur.Version+1) {
		t.Fatalf("patch result %s %s", r.Data, r.Header.Get("ETag"))
	}

	// campaign under review: no media changes
	cmWalk(t, c, nil, "SUBMITTED")
	owner.expect(cmUpload(t, owner.browser, c.id, cmFields(media.KindGallery, "During review"), "x.png", "image/png", cmPNGWithText(t, nil)),
		409, "INVALID_STATUS")
	owner.expect(owner.do("DELETE", "/campaigns/"+c.id+"/media/"+m.ID, nil), 409, "INVALID_STATUS")

	// staff: campaign.view lists and previews; content.moderate removes (support has view only)
	sup := s.staff(t, f.support)
	if l := cmList(t, sup, "/admin/campaigns/"+c.id+"/media/all"); len(l) == 0 {
		t.Fatal("staff list empty")
	}
	if q := cmGet(t, sup, s.srv.URL+"/api/v1/campaigns/"+c.id+"/media/"+m.ID+"/content"); q.status != 200 {
		t.Fatalf("staff preview: %d", q.status)
	}
	rm := map[string]string{"reason_code": "POLICY_VIOLATION", "note": "image breaches the content policy"}
	sup.expect(sup.do("POST", "/admin/campaigns/"+c.id+"/media/"+m.ID+"/remove", rm), 403, "PERMISSION_DENIED")
	owner.expect(owner.do("POST", "/admin/campaigns/"+c.id+"/media/"+m.ID+"/remove", rm), 404, "")
	mods := cuModerators(t, s)
	mod := s.staff(t, mods.mod1)
	mod.expect(mod.do("POST", "/admin/campaigns/"+c.id+"/media/"+m.ID+"/remove", map[string]string{"reason_code": "bad", "note": "x"}), 422, "VALIDATION_FAILED")
	mod.expect(mod.do("POST", "/admin/campaigns/"+c.id+"/media/"+m.ID+"/remove", rm), 200, "")
	mod.expect(mod.do("POST", "/admin/campaigns/"+c.id+"/media/"+m.ID+"/remove", rm), 409, "INVALID_STATUS")
	if q := cmOwnerContent(t, owner, c, m.ID); q.status != 404 {
		t.Fatalf("moderated media served to owner: %d", q.status)
	}
	if cmCount(t, `SELECT count(*) FROM app.campaign_media_events WHERE media_id = $1 AND event_type = 'REMOVED' AND actor_type = 'STAFF'
		 AND reason_code = 'POLICY_VIOLATION'`, m.ID) != 1 {
		t.Fatal("moderation not on the timeline")
	}
	if cmCount(t, `SELECT count(*) FROM audit.audit_events WHERE action = 'campaign.media_removed' AND target_id = $1 AND actor_type = 'staff'`, m.ID) != 1 {
		t.Fatal("moderation audit missing")
	}
}

func cmSQLState(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

// The database refuses to make a private (KYC/evidence) object, or any non-campaign-media object, into campaign
// media, whatever the application does.
func TestCampaignMediaPrivateObjectNeverLinked(t *testing.T) {
	s := newITServer(t, nil)
	u := cmOwner(t, s, "media-private")
	c := cmMakeDraft(t, u.id)
	st := s.d.Verification.Storage
	kyc, err := st.Upload(ctx(t), storage.UploadInput{BucketClass: storage.BucketPrivateKYC, Purpose: "KYC_DOCUMENT", OwnerModule: "kyc",
		UploaderUserID: u.id, DeclaredContentType: "image/png", Body: bytes.NewReader(cmPNGWithText(t, []byte("kyc")))})
	if err != nil {
		t.Fatal(err)
	}
	avatar, err := st.Upload(ctx(t), storage.UploadInput{BucketClass: storage.BucketPublicMedia, Purpose: "PROFILE_AVATAR", OwnerModule: "users",
		UploaderUserID: u.id, DeclaredContentType: "image/png", Body: bytes.NewReader(cmPNGWithText(t, []byte("avatar")))})
	if err != nil {
		t.Fatal(err)
	}
	app := s.d.DB // fundzim_app, as the API
	insert := func(objectID, bucket string) error {
		return pgx.BeginFunc(ctx(t), app, func(tx pgx.Tx) error {
			id := ids.New()
			if _, err := tx.Exec(ctx(t), `INSERT INTO app.campaign_media (id, campaign_id, stored_object_id, stored_object_bucket, kind, alt_text,
				status, created_by) VALUES ($1, $2, $3, $4, 'GALLERY', 'A private document', 'UPLOADED', $5)`, id, c.id, objectID, bucket, u.id); err != nil {
				return err
			}
			_, err := tx.Exec(ctx(t), `INSERT INTO app.campaign_media_events (id, media_id, media_version, event_type, to_status, actor_type, actor_id)
				VALUES ($1, $2, 1, 'UPLOADED', 'UPLOADED', 'USER', $3)`, ids.New(), id, u.id)
			return err
		})
	}
	// the purpose/owner trigger refuses it first; the bucket-pinned composite foreign key is the structural backstop
	if err := insert(kyc.ID, "PUBLIC_MEDIA"); cmSQLState(err) != "23514" {
		t.Fatalf("KYC object as PUBLIC_MEDIA: %v", err)
	}
	for _, fk := range []string{"fk_campaign_media_stored_object", "fk_campaign_media_processed_object"} {
		if cmCount(t, `SELECT count(*) FROM pg_constraint WHERE conname = $1 AND contype = 'f'
			 AND confrelid = 'app.stored_objects'::regclass AND pg_get_constraintdef(oid) LIKE '%(id, bucket_class)%'`, fk) != 1 {
			t.Fatalf("%s is not the bucket-pinned composite foreign key", fk)
		}
	}
	if err := insert(kyc.ID, "PRIVATE_KYC"); cmSQLState(err) != "23514" {
		t.Fatalf("KYC object with its own bucket: %v", err)
	}
	if err := insert(avatar.ID, "PUBLIC_MEDIA"); cmSQLState(err) != "23514" {
		t.Fatalf("avatar object as campaign media: %v", err)
	}
	// a legitimate media row cannot be re-pointed at a private derivative either
	m := cmUploadOK(t, u, c.id, media.KindCover, "image/png", cmPNGWithText(t, []byte("legit")))
	err = pgx.BeginFunc(ctx(t), app, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx(t), `UPDATE app.campaign_media SET processed_object_id = $2 WHERE id = $1`, m.ID, kyc.ID)
		return err
	})
	if cmSQLState(err) != "23514" { // the purpose/owner trigger fires before the bucket-pinned foreign key
		t.Fatalf("private derivative: %v", err)
	}
	// rows are never deleted; the stored object of a media row cannot change
	if _, err := app.Exec(ctx(t), `DELETE FROM app.campaign_media WHERE id = $1`, m.ID); err == nil {
		t.Fatal("campaign media deleted")
	}
	if _, err := app.Exec(ctx(t), `UPDATE app.campaign_media SET stored_object_id = $2 WHERE id = $1`, m.ID, avatar.ID); err == nil {
		t.Fatal("stored object re-pointed")
	}
	// the state machine is enforced in the database (no edge leads back to UPLOADED)
	if _, err := app.Exec(ctx(t), `UPDATE app.campaign_media SET status = 'UPLOADED' WHERE id = $1`, m.ID); cmSQLState(err) != "23514" {
		t.Fatalf("illegal transition back to UPLOADED: %v", err)
	}
	cmWait(t, u, c.id, m.ID, 120*time.Second)
}
