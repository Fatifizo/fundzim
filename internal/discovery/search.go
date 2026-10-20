package discovery

import (
	"context"
	"encoding/base64"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/text/unicode/norm"

	"github.com/Fatifizo/fundzim/internal/campaigns"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
)

// Search limits (docs/stage-16/search.md). Engineering values, not commitments.
const (
	MaxQueryRunes   = 100
	MaxTerms        = 8
	MaxTermRunes    = 40
	DefaultPageSize = 12
	MaxPageSize     = 48
	CountCap        = 1000
	searchTimeout   = "2s"
	// RelevanceWindow bounds relevance ranking to the newest matching campaigns: ts_rank_cd over every match of a common
	// word cost 660 ms p50 at 54 000 discoverable campaigns (docs/stage-16/mobile-performance.md §5); ranking within the
	// newest 2 000 matches keeps the cost flat. Older matches stay reachable with sort=newest/oldest and narrower words.
	RelevanceWindow = 2000
	fuzzyThreshold  = "0.4" // pg_trgm word similarity for the typo-tolerant fallback
)

// Query is a normalised search query: lower-case terms of letters and digits only (no operators survive).
type Query struct {
	Terms []string
	Text  string // the terms joined by single spaces (shown back to the user)
}

// Empty reports whether the query has no searchable terms.
func (q Query) Empty() bool { return len(q.Terms) == 0 }

// TSQuery builds a to_tsquery('simple', …) expression from the terms: every term must match, the last one as a prefix
// (search-as-you-type). Terms contain only letters and digits, so no tsquery syntax can be injected.
func (q Query) TSQuery() string {
	if q.Empty() {
		return ""
	}
	parts := make([]string, len(q.Terms))
	for i, t := range q.Terms {
		parts[i] = t
		if i == len(q.Terms)-1 && utf8.RuneCountInString(t) >= 3 {
			parts[i] += ":*"
		}
	}
	return strings.Join(parts, " & ")
}

// NormalizeQuery normalises user input: valid UTF-8, NFC, lower case, split on anything that is not a letter or a
// digit (punctuation, symbols, operators, control characters and markup become separators), single-letter words
// dropped (digits kept), at most MaxTerms terms of at most MaxTermRunes runes, duplicates removed. The returned code is a
// validation code ("" when valid); an input with no usable terms is valid and empty.
func NormalizeQuery(in string) (Query, string) {
	if !utf8.ValidString(in) {
		return Query{}, "INVALID_ENCODING"
	}
	in = strings.TrimSpace(in)
	if utf8.RuneCountInString(in) > MaxQueryRunes {
		return Query{}, "INVALID_LENGTH"
	}
	in = strings.ToLower(norm.NFC.String(in))
	var terms []string
	seen := map[string]bool{}
	for _, f := range strings.FieldsFunc(in, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) && !unicode.Is(unicode.Mn, r) }) {
		if utf8.RuneCountInString(f) < 2 && !unicode.IsNumber([]rune(f)[0]) {
			continue
		}
		if r := []rune(f); len(r) > MaxTermRunes {
			f = string(r[:MaxTermRunes])
		}
		if seen[f] {
			continue
		}
		seen[f] = true
		terms = append(terms, f)
		if len(terms) == MaxTerms {
			break
		}
	}
	return Query{Terms: terms, Text: strings.Join(terms, " ")}, ""
}

// Segment is a piece of text, marked when it matches a search term (rendered as <mark> by the web app; plain text).
type Segment struct {
	Text  string `json:"text"`
	Match bool   `json:"match"`
}

// Highlight splits text into segments, marking every word that starts with a query term (case-insensitive, the same
// prefix rule as the search). Text is returned verbatim (concatenating the segments gives the input back).
func Highlight(text string, terms []string) []Segment {
	if len(terms) == 0 || text == "" {
		return []Segment{{Text: text}}
	}
	var out []Segment
	add := func(s string, m bool) {
		if s == "" {
			return
		}
		if n := len(out); n > 0 && out[n-1].Match == m {
			out[n-1].Text += s
			return
		}
		out = append(out, Segment{Text: s, Match: m})
	}
	isWord := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Mn, r) }
	i := 0
	for i < len(text) {
		r, w := utf8.DecodeRuneInString(text[i:])
		if !isWord(r) {
			j := i
			for j < len(text) {
				r2, w2 := utf8.DecodeRuneInString(text[j:])
				if isWord(r2) {
					break
				}
				j += w2
			}
			add(text[i:j], false)
			i = j
			continue
		}
		j := i + w
		for j < len(text) {
			r2, w2 := utf8.DecodeRuneInString(text[j:])
			if !isWord(r2) {
				break
			}
			j += w2
		}
		word := text[i:j]
		lw := strings.ToLower(norm.NFC.String(word))
		m := false
		for _, t := range terms {
			if strings.HasPrefix(lw, t) {
				m = true
				break
			}
		}
		add(word, m)
		i = j
	}
	return out
}

// ---- filters and sorts ------------------------------------------------------------------------------------------

// Sorts.
const (
	SortRelevance = "relevance"
	SortNewest    = "newest"
	SortOldest    = "oldest"
	SortTrending  = "trending"
)

// SearchInput is GET /discovery/campaigns (and /discovery/search).
type SearchInput struct {
	Q, Category, Province, Town, Status, Published, Organiser, Sort, Cursor string
	Limit                                                                   int
	Surface                                                                 string // SEARCH | CATEGORY | LOCATION | DIRECTORY (impression channel)
}

// Card is a discovery result: the campaigns card plus highlights and, where relevant, the reason it is shown.
type Card struct {
	campaigns.DiscoveryCard
	Highlights *Highlights `json:"highlights,omitempty"`
	Reason     *Reason     `json:"reason,omitempty"`
}

// Highlights are the marked title and summary of a search result.
type Highlights struct {
	Title   []Segment `json:"title"`
	Summary []Segment `json:"summary"`
}

// SearchResult is one page.
type SearchResult struct {
	Items      []Card          `json:"items"`
	NextCursor *string         `json:"next_cursor"`
	Total      campaigns.Count `json:"total"`
	Match      string          `json:"match"` // EXACT | FUZZY | NONE | ALL (no query)
	Query      string          `json:"query"`
	Sort       string          `json:"sort"`
}

var (
	slugRe   = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	catRe    = regexp.MustCompile(`^([a-z0-9]+(-[a-z0-9]+)*|[A-Z][A-Z_]{1,39})$`)
	statuses = map[string][]string{"": {"ACTIVE", "PAUSED", "COMPLETED"}, "active": {"ACTIVE"}, "paused": {"PAUSED"}, "ended": {"COMPLETED"}}
	windows  = map[string]int{"": 0, "7d": 7, "30d": 30, "90d": 90, "365d": 365}
	orgs     = map[string]string{"": "", "individual": "INDIVIDUAL", "organisation": "ORGANISATION"}
)

// plan is a validated search: SQL predicate parts and parameters.
type plan struct {
	q           Query
	sort        string
	limit       int
	where       []string
	args        []any
	cursor      []string // decoded cursor fields (after the sort tag)
	hasFilters  bool
	statusCodes []string
}

func (s *Service) planSearch(ctx context.Context, in SearchInput) (*plan, error) {
	p := &plan{limit: in.Limit}
	if p.limit <= 0 {
		p.limit = DefaultPageSize
	}
	if p.limit > MaxPageSize {
		p.limit = MaxPageSize
	}
	var det []errs.Detail
	q, code := NormalizeQuery(in.Q)
	if code != "" {
		det = append(det, errs.Detail{Field: "q", Code: code})
	}
	p.q = q
	p.sort = in.Sort
	switch p.sort {
	case "":
		p.sort = SortNewest
		if !q.Empty() {
			p.sort = SortRelevance
		}
	case SortRelevance:
		if q.Empty() {
			p.sort = SortNewest // relevance without terms is the newest order (documented)
		}
	case SortNewest, SortOldest, SortTrending:
	default:
		det = append(det, errs.Detail{Field: "sort", Code: "INVALID_VALUE"})
	}
	st, ok := statuses[in.Status]
	if !ok {
		det = append(det, errs.Detail{Field: "status", Code: "INVALID_VALUE"})
	}
	p.statusCodes = st
	days, ok := windows[in.Published]
	if !ok {
		det = append(det, errs.Detail{Field: "published", Code: "INVALID_VALUE"})
	}
	org, ok := orgs[in.Organiser]
	if !ok {
		det = append(det, errs.Detail{Field: "organiser", Code: "INVALID_VALUE"})
	}
	if in.Category != "" && (len(in.Category) > 40 || !catRe.MatchString(in.Category)) {
		det = append(det, errs.Detail{Field: "category", Code: "INVALID_VALUE"})
	}
	for f, v := range map[string]string{"province": in.Province, "town": in.Town} {
		if v != "" && (len(v) > 40 || !slugRe.MatchString(v)) {
			det = append(det, errs.Detail{Field: f, Code: "INVALID_VALUE"})
		}
	}
	if len(det) > 0 {
		return nil, httpx.Validation(det...)
	}
	arg := func(v any) string {
		p.args = append(p.args, v)
		return "$" + strconv.Itoa(len(p.args))
	}
	p.where = append(p.where, "i.discoverable", "i.status = ANY("+arg(st)+"::text[])")
	ref, err := s.reference(ctx)
	if err != nil {
		return nil, err
	}
	// Unknown category or location values match nothing (a valid but unknown slug is an empty result, not an error).
	if in.Category != "" {
		code := ref.categoryCode(in.Category)
		p.where = append(p.where, "i.category_code = "+arg(code))
		p.hasFilters = true
	}
	if in.Province != "" {
		p.where = append(p.where, "i.province_code = "+arg(ref.locationCode("PROVINCE", in.Province)))
		p.hasFilters = true
	}
	if in.Town != "" {
		p.where = append(p.where, "i.location_code = "+arg(ref.locationCode("TOWN", in.Town)))
		p.hasFilters = true
	}
	if days > 0 {
		p.where = append(p.where, "i.published_at >= "+arg(s.now().AddDate(0, 0, -days)))
		p.hasFilters = true
	}
	if org != "" {
		p.where = append(p.where, "i.organiser_type = "+arg(org))
		p.hasFilters = true
	}
	if in.Status != "" {
		p.hasFilters = true
	}
	if !q.Empty() {
		p.where = append(p.where, "i.search_doc @@ to_tsquery('simple', "+arg(q.TSQuery())+")")
	}
	if in.Cursor != "" {
		f, err := decodeCursor(in.Cursor, p.sort)
		if err != nil {
			return nil, httpx.Validation(errs.Detail{Field: "cursor", Code: "INVALID_FORMAT"})
		}
		p.cursor = f
	}
	return p, nil
}

// cursor format: base64url("<sort>|<key...>|<campaign id>").
func encodeCursor(sort string, parts ...string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(sort + "|" + strings.Join(parts, "|")))
}

func decodeCursor(c, sort string) ([]string, error) {
	if len(c) > 200 {
		return nil, errors.New("cursor too long")
	}
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return nil, err
	}
	f := strings.Split(string(raw), "|")
	want := map[string]int{SortNewest: 3, SortOldest: 3, SortRelevance: 4, SortTrending: 4}[sort]
	if len(f) != want || f[0] != sort {
		return nil, errors.New("cursor does not match the sort")
	}
	f = f[1:]
	if _, err := time.Parse(time.RFC3339Nano, f[len(f)-2]); err != nil {
		return nil, err
	}
	if !uuidRe.MatchString(f[len(f)-1]) {
		return nil, errors.New("bad id")
	}
	if sort == SortRelevance {
		if _, err := strconv.ParseFloat(f[0], 32); err != nil {
			return nil, err
		}
	}
	if sort == SortTrending && !decimalRe.MatchString(f[0]) {
		return nil, errors.New("bad score")
	}
	return f, nil
}

var (
	tsParamRe = regexp.MustCompile(`^i\.search_doc @@ to_tsquery\('simple', (\$[0-9]+)\)$`)
	uuidRe    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	decimalRe = regexp.MustCompile(`^[0-9]{1,9}(\.[0-9]{1,6})?$`)
)

type candidate struct {
	id          string
	publishedAt time.Time
	key         string // rank or score as text (relevance, trending)
}

// Search runs a discovery query. Candidates come from the projection; cards come from campaigns.PublicCards (eligibility
// re-checked now). Bounded: query length and terms, page size, statement timeout, keyset pagination, capped count.
func (s *Service) Search(ctx context.Context, in SearchInput) (SearchResult, error) {
	res := SearchResult{Items: []Card{}}
	p, err := s.planSearch(ctx, in)
	if err != nil {
		return res, err
	}
	res.Sort, res.Query = p.sort, p.q.Text
	cands, total, err := s.candidates(ctx, p)
	outcome := "RESULTS"
	defer func() { s.recordSearch(p, outcome) }()
	if err != nil {
		outcome = "ERROR"
		if errors.Is(err, ErrSearchTimeout) {
			outcome = "TIMEOUT"
		}
		return res, err
	}
	res.Total = total
	res.Match = "ALL"
	if !p.q.Empty() {
		res.Match = "EXACT"
	}
	if len(cands) == 0 && !p.q.Empty() && in.Cursor == "" {
		// typo tolerance: trigram word similarity on titles, one page, no cursor
		if cands, err = s.fuzzy(ctx, p); err != nil {
			outcome = "ERROR"
			return res, err
		}
		res.Match, res.Total = "FUZZY", campaigns.Count{Value: len(cands)}
		outcome = "FUZZY_RESULTS"
	}
	if len(cands) == 0 {
		outcome = "NO_RESULTS"
		if !p.q.Empty() {
			res.Match = "NONE"
		}
		return res, nil
	}
	if len(cands) == p.limit && res.Match != "FUZZY" {
		last := cands[len(cands)-1]
		ts := last.publishedAt.UTC().Format(time.RFC3339Nano)
		var c string
		switch p.sort {
		case SortNewest, SortOldest:
			c = encodeCursor(p.sort, ts, last.id)
		default:
			c = encodeCursor(p.sort, last.key, ts, last.id)
		}
		res.NextCursor = &c
	}
	ids := make([]string, len(cands))
	for i, c := range cands {
		ids[i] = c.id
	}
	cards, err := s.Campaigns.PublicCards(ctx, ids, false)
	if err != nil {
		outcome = "ERROR"
		return res, err
	}
	surface := in.Surface
	if surface == "" {
		surface = "SEARCH"
	}
	for _, c := range cards {
		card := Card{DiscoveryCard: c}
		if !p.q.Empty() {
			card.Highlights = &Highlights{Title: Highlight(c.Title, p.q.Terms), Summary: Highlight(c.Summary, p.q.Terms)}
		}
		res.Items = append(res.Items, card)
		s.recordImpression(c.ID, surface)
	}
	return res, nil
}

const candidateCols = `i.campaign_id::text, i.published_at`

func (s *Service) candidates(ctx context.Context, p *plan) ([]candidate, campaigns.Count, error) {
	var total campaigns.Count
	args := append([]any{}, p.args...)
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	where := strings.Join(p.where, " AND ")
	var sel, from, order, after string
	from = ` FROM app.discovery_campaign_index i`
	outer := where
	switch p.sort {
	case SortNewest:
		sel, order = candidateCols+`, ''`, ` ORDER BY i.published_at DESC, i.campaign_id DESC`
		if p.cursor != nil {
			after = ` AND (i.published_at, i.campaign_id) < (` + arg(p.cursor[0]) + `::timestamptz, ` + arg(p.cursor[1]) + `::uuid)`
		}
	case SortOldest:
		sel, order = candidateCols+`, ''`, ` ORDER BY i.published_at ASC, i.campaign_id ASC`
		if p.cursor != nil {
			after = ` AND (i.published_at, i.campaign_id) > (` + arg(p.cursor[0]) + `::timestamptz, ` + arg(p.cursor[1]) + `::uuid)`
		}
	case SortRelevance:
		// ts_rank_cd with A (title) > B (summary) > C (category, location) > D (organiser) weights; ties by newest.
		// Ranked within the RelevanceWindow newest matches (the GIN match plus a bounded sort; never every match).
		from = ` FROM (SELECT * FROM app.discovery_campaign_index i WHERE ` + where + ` ORDER BY i.published_at DESC, i.campaign_id DESC LIMIT ` +
			strconv.Itoa(RelevanceWindow) + `) i`
		outer = "true"
		rank := `ts_rank_cd('{0.1, 0.2, 0.4, 1.0}', i.search_doc, to_tsquery('simple', ` + arg(p.q.TSQuery()) + `), 32)::real`
		sel = candidateCols + `, (` + rank + `)::text`
		order = ` ORDER BY ` + rank + ` DESC, i.published_at DESC, i.campaign_id DESC`
		if p.cursor != nil {
			after = ` AND (` + rank + `, i.published_at, i.campaign_id) < (` + arg(p.cursor[0]) + `::real, ` + arg(p.cursor[1]) + `::timestamptz, ` +
				arg(p.cursor[2]) + `::uuid)`
		}
	case SortTrending:
		from += ` LEFT JOIN app.discovery_trending_scores t ON t.campaign_id = i.campaign_id AND t.rank_eligible`
		score := `coalesce(t.score, 0)`
		sel = candidateCols + `, ` + score + `::text`
		order = ` ORDER BY ` + score + ` DESC, i.published_at DESC, i.campaign_id DESC`
		if p.cursor != nil {
			after = ` AND (` + score + `, i.published_at, i.campaign_id) < (` + arg(p.cursor[0]) + `::numeric, ` + arg(p.cursor[1]) + `::timestamptz, ` +
				arg(p.cursor[2]) + `::uuid)`
		}
	}
	var out []candidate
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout = '`+searchTimeout+`'; SET LOCAL plan_cache_mode = force_custom_plan`); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+sel+from+` WHERE `+outer+after+order+` LIMIT `+strconv.Itoa(p.limit), args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var c candidate
			if err := rows.Scan(&c.id, &c.publishedAt, &c.key); err != nil {
				rows.Close()
				return err
			}
			out = append(out, c)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM (SELECT 1 FROM app.discovery_campaign_index i WHERE `+where+` LIMIT `+
			strconv.Itoa(CountCap+1)+`) x`, p.args...).Scan(&total.Value)
	})
	if isTimeout(err) {
		return nil, total, ErrSearchTimeout
	}
	if err != nil {
		return nil, total, err
	}
	if total.Value > CountCap {
		total = campaigns.Count{Value: CountCap, Capped: true}
	}
	return out, total, nil
}

// fuzzy finds titles similar to the query (pg_trgm word similarity, GIN-indexed), with the same filters minus the text
// match. One page, ordered by similarity then newest.
func (s *Service) fuzzy(ctx context.Context, p *plan) ([]candidate, error) {
	var where []string
	args := []any{}
	// rebuild the predicate without the full-text condition (the last one added for a non-empty query)
	for _, w := range p.where {
		if m := tsParamRe.FindStringSubmatch(w); m != nil {
			// keep the parameter typed (an unused parameter cannot be planned), drop the full-text condition
			where = append(where, m[1]+"::text <> ''")
			continue
		}
		where = append(where, w)
	}
	args = append(args, p.args...)
	args = append(args, p.q.Text)
	qp := "$" + strconv.Itoa(len(args))
	var out []candidate
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout = '`+searchTimeout+`'; SET LOCAL plan_cache_mode = force_custom_plan;
			SET LOCAL pg_trgm.word_similarity_threshold = `+fuzzyThreshold); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+candidateCols+`, public.word_similarity(`+qp+`, i.title_norm)::real::text
			FROM app.discovery_campaign_index i WHERE `+strings.Join(where, " AND ")+` AND `+qp+` OPERATOR(public.<%) i.title_norm
			ORDER BY public.word_similarity(`+qp+`, i.title_norm) DESC, i.published_at DESC, i.campaign_id DESC LIMIT `+strconv.Itoa(p.limit), args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var c candidate
			if err := rows.Scan(&c.id, &c.publishedAt, &c.key); err != nil {
				rows.Close()
				return err
			}
			out = append(out, c)
		}
		rows.Close()
		return rows.Err()
	})
	if isTimeout(err) {
		return nil, ErrSearchTimeout
	}
	return out, err
}

func isTimeout(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "57014"
}

func (s *Service) recordSearch(p *plan, outcome string) {
	if s.Recorder != nil {
		s.Recorder.Search(s.now(), p.sort, !p.q.Empty(), p.hasFilters, outcome)
	}
}

func (s *Service) recordImpression(campaignID, surface string) {
	if s.Recorder == nil {
		return
	}
	metric := "LISTING_IMPRESSION"
	if surface == "SEARCH" {
		metric = "SEARCH_IMPRESSION"
	}
	channel := map[string]string{"SEARCH": "SEARCH", "CATEGORY": "CATEGORY", "LOCATION": "", "DIRECTORY": "", "TRENDING": "TRENDING",
		"FEATURED": "FEATURED", "RECOMMENDED": "RECOMMENDED"}[surface]
	s.Recorder.Count(s.now(), campaignID, metric, channel, 1)
}
