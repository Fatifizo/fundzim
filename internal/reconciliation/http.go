package reconciliation

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/storage"
)

// Route is one reconciliation route. Idempotent creations get the REQUIRED idempotency middleware (composition root);
// the statement upload is idempotent by content hash instead (a duplicate file is refused with DUPLICATE_IMPORT).
type Route struct {
	Pattern    string
	Policy     httpx.Policy
	Handler    http.HandlerFunc
	Idempotent bool
	Scope      string
}

// HTTP serves the reconciliation API (staff only; api/openapi/fundzim-v1.yaml, tag reconciliation).
type HTTP struct{ S *Service }

// Routes lists the API.
func (h *HTTP) Routes() []Route {
	perm := httpx.Permission
	const a = "/api/v1/admin/reconciliation"
	exc := func(action string) Route {
		return Route{Pattern: "POST " + a + "/exceptions/{exception_id}/" + strings.ReplaceAll(action, "_", "-"), Policy: perm(PermInvestigate),
			Handler: h.act(action)}
	}
	return []Route{
		{Pattern: "GET " + a + "/overview", Policy: perm(PermView), Handler: h.overview},
		{Pattern: "GET " + a + "/sources", Policy: perm(PermView), Handler: h.sources},
		{Pattern: "GET " + a + "/imports", Policy: perm(PermView), Handler: h.imports},
		{Pattern: "POST " + a + "/imports", Policy: perm(PermImport), Handler: h.upload},
		{Pattern: "POST " + a + "/imports/sandbox", Policy: perm(PermImport), Handler: h.sandbox, Idempotent: true, Scope: "reconciliation.import.sandbox"},
		{Pattern: "GET " + a + "/imports/{import_id}", Policy: perm(PermView), Handler: h.importDetail},
		{Pattern: "GET " + a + "/runs", Policy: perm(PermView), Handler: h.runs},
		{Pattern: "POST " + a + "/runs", Policy: perm(PermRun), Handler: h.requestRun, Idempotent: true, Scope: "reconciliation.run.create"},
		{Pattern: "GET " + a + "/runs/{run_id}", Policy: perm(PermView), Handler: h.runDetail},
		{Pattern: "GET " + a + "/exceptions", Policy: perm(PermView), Handler: h.exceptions},
		{Pattern: "GET " + a + "/exceptions/{exception_id}", Policy: perm(PermView), Handler: h.exceptionDetail},
		exc("assign"), exc("start"), exc("await_evidence"), exc("resume"), exc("comment"), exc("close"), exc("reopen"),
		{Pattern: "POST " + a + "/exceptions/{exception_id}/resolutions", Policy: perm(PermInvestigate), Handler: h.propose, Idempotent: true,
			Scope: "reconciliation.resolution.create"},
		{Pattern: "GET " + a + "/resolutions", Policy: perm(PermView), Handler: h.pending},
		{Pattern: "POST " + a + "/resolutions/{resolution_id}/approve", Policy: perm(PermResolve), Handler: h.decide("approve")},
		{Pattern: "POST " + a + "/resolutions/{resolution_id}/reject", Policy: perm(PermResolve), Handler: h.decide("reject")},
		{Pattern: "POST " + a + "/resolutions/{resolution_id}/cancel", Policy: perm(PermInvestigate), Handler: h.decide("cancel")},
		{Pattern: "GET " + a + "/settlements", Policy: perm(PermView), Handler: h.settlements},
		{Pattern: "GET " + a + "/settlements/{batch_id}", Policy: perm(PermView), Handler: h.settlement},
		{Pattern: "GET " + a + "/payouts", Policy: perm(PermView), Handler: h.payouts},
		{Pattern: "GET " + a + "/ledger", Policy: perm(PermView), Handler: h.ledger},
		{Pattern: "GET " + a + "/fees", Policy: perm(PermView), Handler: h.fees},
		{Pattern: "GET " + a + "/reports", Policy: perm(PermView), Handler: h.report},
		{Pattern: "GET " + a + "/reports/export", Policy: perm(PermExport), Handler: h.export},
		{Pattern: "GET " + a + "/rule-sets", Policy: perm(PermView), Handler: h.ruleSets},
		{Pattern: "POST " + a + "/rule-sets", Policy: perm(PermRulesRequest), Handler: h.proposeRules, Idempotent: true, Scope: "reconciliation.rules.create"},
		{Pattern: "POST " + a + "/rule-sets/{rule_set_id}/approve", Policy: perm(PermRulesApprove), Handler: h.decideRules("approve")},
		{Pattern: "POST " + a + "/rule-sets/{rule_set_id}/reject", Policy: perm(PermRulesApprove), Handler: h.decideRules("reject")},
		{Pattern: "POST " + a + "/rule-sets/{rule_set_id}/retire", Policy: perm(PermRulesApprove), Handler: h.decideRules("retire")},
		{Pattern: "POST " + a + "/rule-sets/{rule_set_id}/withdraw", Policy: perm(PermRulesRequest), Handler: h.decideRules("withdraw")},
	}
}

func (h *HTTP) fail(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteError(w, r, h.S.Logger, err)
}

func write(w http.ResponseWriter, r *http.Request, status int, data any) {
	w.Header().Set("Cache-Control", "private, no-store")
	httpx.WriteData(w, r, status, data)
}

func staffID(r *http.Request) string { return authz.PrincipalFrom(r.Context()).UserID }

func intParam(r *http.Request, name string) int {
	n, _ := strconv.Atoi(r.URL.Query().Get(name))
	return n
}

func (h *HTTP) reply(w http.ResponseWriter, r *http.Request, status int, data any, err error) {
	if err != nil {
		h.fail(w, r, err)
		return
	}
	write(w, r, status, data)
}

func (h *HTTP) overview(w http.ResponseWriter, r *http.Request) {
	o, err := h.S.Overview(r.Context())
	h.reply(w, r, http.StatusOK, o, err)
}

func (h *HTTP) sources(w http.ResponseWriter, r *http.Request) {
	l, err := h.S.Sources(r.Context())
	h.reply(w, r, http.StatusOK, map[string]any{"items": l}, err)
}

func (h *HTTP) imports(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	l, err := h.S.Imports(r.Context(), ImportFilter{Source: q.Get("source"), Status: q.Get("status"), Currency: q.Get("currency"), Limit: intParam(r, "limit")})
	h.reply(w, r, http.StatusOK, map[string]any{"items": l}, err)
}

// upload reads a multipart form: source, optional supersedes_import_id, and one file part "file" (text/csv or
// application/json). The body is capped before anything is read.
func (h *HTTP) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, h.S.maxBytes()+64<<10)
	mr, err := r.MultipartReader()
	if err != nil {
		h.fail(w, r, httpx.Validation(errs.Detail{Field: "file", Code: "MULTIPART_REQUIRED"}))
		return
	}
	var in UploadInput
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			h.fail(w, r, httpx.Validation(errs.Detail{Field: "file", Code: "MALFORMED_UPLOAD"}))
			return
		}
		switch part.FormName() {
		case "source", "supersedes_import_id":
			b, _ := io.ReadAll(io.LimitReader(part, 200))
			if part.FormName() == "source" {
				in.SourceCode = strings.TrimSpace(string(b))
			} else {
				in.SupersedesImportID = strings.TrimSpace(string(b))
			}
		case "file":
			if in.Content != nil {
				h.fail(w, r, httpx.Validation(errs.Detail{Field: "file", Code: "ONE_FILE_ONLY"}))
				return
			}
			mt, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
			in.MediaType, in.Filename = mt, part.FileName()
			b, err := io.ReadAll(io.LimitReader(part, h.S.maxBytes()+1))
			if err != nil {
				h.fail(w, r, httpx.Validation(errs.Detail{Field: "file", Code: "FILE_TOO_LARGE"}))
				return
			}
			if b == nil {
				b = []byte{}
			}
			in.Content = b
		}
		_ = part.Close()
	}
	if in.Content == nil {
		h.fail(w, r, httpx.Validation(errs.Detail{Field: "file", Code: "REQUIRED"}))
		return
	}
	if in.MediaType != storage.TypeCSV && in.MediaType != storage.TypeJSON {
		h.fail(w, r, httpx.Validation(errs.Detail{Field: "file", Code: "UNSUPPORTED_MEDIA_TYPE"}))
		return
	}
	imp, err := h.S.Upload(r.Context(), staffID(r), in)
	h.reply(w, r, http.StatusAccepted, imp, err)
}

func (h *HTTP) sandbox(w http.ResponseWriter, r *http.Request) {
	var in SandboxInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	imp, err := h.S.GenerateSandboxStatement(r.Context(), staffID(r), in)
	h.reply(w, r, http.StatusAccepted, imp, err)
}

func (h *HTTP) importDetail(w http.ResponseWriter, r *http.Request) {
	imp, err := h.S.ImportByID(r.Context(), r.PathValue("import_id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	lines, err := h.S.Items(r.Context(), ItemFilter{ImportID: imp.ID, MatchStatus: r.URL.Query().Get("match_status"), Limit: intParam(r, "limit")})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	exc, err := h.S.Exceptions(r.Context(), ExceptionFilter{Limit: 200})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	lineSet := map[string]bool{}
	for _, l := range lines {
		lineSet[l.ID] = true
	}
	related := []Exception{}
	for _, e := range exc {
		if e.ItemID != nil && lineSet[*e.ItemID] {
			related = append(related, e)
		}
	}
	write(w, r, http.StatusOK, map[string]any{"import": imp, "lines": lines, "exceptions": related})
}

func (h *HTTP) runs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	l, err := h.S.Runs(r.Context(), q.Get("source"), q.Get("status"), intParam(r, "limit"))
	h.reply(w, r, http.StatusOK, map[string]any{"items": l}, err)
}

func (h *HTTP) requestRun(w http.ResponseWriter, r *http.Request) {
	var in RunInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	run, err := h.S.RequestRun(r.Context(), staffID(r), in)
	h.reply(w, r, http.StatusAccepted, run, err)
}

func (h *HTTP) runDetail(w http.ResponseWriter, r *http.Request) {
	run, err := h.S.RunByID(r.Context(), r.PathValue("run_id"))
	h.reply(w, r, http.StatusOK, run, err)
}

func (h *HTTP) exceptions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	l, err := h.S.Exceptions(r.Context(), ExceptionFilter{Status: q.Get("status"), Type: q.Get("type"), Severity: q.Get("severity"),
		Source: q.Get("source"), Currency: q.Get("currency"), AssignedTo: q.Get("assigned_to"), OpenOnly: q.Get("open") == "true",
		Limit: intParam(r, "limit")})
	h.reply(w, r, http.StatusOK, map[string]any{"items": l}, err)
}

func (h *HTTP) exceptionDetail(w http.ResponseWriter, r *http.Request) {
	x, err := h.S.ExceptionByID(r.Context(), r.PathValue("exception_id"))
	h.reply(w, r, http.StatusOK, x, err)
}

func (h *HTTP) act(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in ActionInput
		if err := httpx.DecodeJSON(r, &in); err != nil {
			h.fail(w, r, err)
			return
		}
		x, err := h.S.Act(r.Context(), staffID(r), r.PathValue("exception_id"), action, in)
		h.reply(w, r, http.StatusOK, x, err)
	}
}

func (h *HTTP) propose(w http.ResponseWriter, r *http.Request) {
	var in ProposalInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	res, err := h.S.Propose(r.Context(), staffID(r), r.PathValue("exception_id"), in)
	h.reply(w, r, http.StatusCreated, res, err)
}

func (h *HTTP) pending(w http.ResponseWriter, r *http.Request) {
	l, err := h.S.PendingResolutions(r.Context(), intParam(r, "limit"))
	h.reply(w, r, http.StatusOK, map[string]any{"items": l}, err)
}

func (h *HTTP) decide(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in DecideInput
		if err := httpx.DecodeJSON(r, &in); err != nil {
			h.fail(w, r, err)
			return
		}
		res, err := h.S.Decide(r.Context(), staffID(r), r.PathValue("resolution_id"), action, in)
		h.reply(w, r, http.StatusOK, res, err)
	}
}

func (h *HTTP) settlements(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	l, err := h.S.Settlements(r.Context(), q.Get("status"), q.Get("currency"), intParam(r, "limit"))
	h.reply(w, r, http.StatusOK, map[string]any{"items": l}, err)
}

func (h *HTTP) settlement(w http.ResponseWriter, r *http.Request) {
	b, err := h.S.Settlement(r.Context(), r.PathValue("batch_id"))
	h.reply(w, r, http.StatusOK, b, err)
}

func (h *HTTP) payouts(w http.ResponseWriter, r *http.Request) {
	v, err := h.S.PayoutsView(r.Context())
	h.reply(w, r, http.StatusOK, v, err)
}

func (h *HTTP) ledger(w http.ResponseWriter, r *http.Request) {
	v, err := h.S.LedgerViewModel(r.Context())
	h.reply(w, r, http.StatusOK, v, err)
}

func (h *HTTP) fees(w http.ResponseWriter, r *http.Request) {
	v, err := h.S.FeesView(r.Context())
	h.reply(w, r, http.StatusOK, v, err)
}

func reportRange(r *http.Request) (time.Time, time.Time, error) {
	var from, to time.Time
	var err error
	q := r.URL.Query()
	if v := q.Get("from"); v != "" {
		if from, err = time.Parse(time.RFC3339, v); err != nil {
			return from, to, httpx.Validation(errs.Detail{Field: "from", Code: "INVALID_TIMESTAMP"})
		}
	}
	if v := q.Get("to"); v != "" {
		if to, err = time.Parse(time.RFC3339, v); err != nil {
			return from, to, httpx.Validation(errs.Detail{Field: "to", Code: "INVALID_TIMESTAMP"})
		}
	}
	return from, to, nil
}

func (h *HTTP) report(w http.ResponseWriter, r *http.Request) {
	from, to, err := reportRange(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	rep, err := h.S.BuildReport(r.Context(), r.URL.Query().Get("kind"), from, to)
	h.reply(w, r, http.StatusOK, rep, err)
}

// export returns the report as a CSV attachment (formula-neutralised, audited; step-up by permission).
func (h *HTTP) export(w http.ResponseWriter, r *http.Request) {
	from, to, err := reportRange(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	rep, err := h.S.BuildReport(r.Context(), r.URL.Query().Get("kind"), from, to)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	body, err := h.S.ExportCSV(r.Context(), staffID(r), rep)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+reportFilename(rep.Kind, rep.GeneratedAt)+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (h *HTTP) ruleSets(w http.ResponseWriter, r *http.Request) {
	l, err := h.S.RuleSets(r.Context())
	h.reply(w, r, http.StatusOK, map[string]any{"items": l}, err)
}

func (h *HTTP) proposeRules(w http.ResponseWriter, r *http.Request) {
	var in RuleSetInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	x, err := h.S.ProposeRuleSet(r.Context(), staffID(r), in)
	h.reply(w, r, http.StatusCreated, x, err)
}

func (h *HTTP) decideRules(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Note         string `json:"note"`
			ConfigSHA256 string `json:"config_sha256"`
		}
		if err := httpx.DecodeJSON(r, &in); err != nil {
			h.fail(w, r, err)
			return
		}
		x, err := h.S.DecideRuleSet(r.Context(), staffID(r), r.PathValue("rule_set_id"), action, in.Note, in.ConfigSHA256)
		h.reply(w, r, http.StatusOK, x, err)
	}
}
