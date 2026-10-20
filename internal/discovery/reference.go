package discovery

import (
	"context"
	"sync"
	"time"

	"github.com/Fatifizo/fundzim/internal/campaigns"
)

// referenceTTL bounds how long category and location lists are cached in process. They are configuration (changed by
// staff or migrations), never campaign state, so a short cache cannot expose anything.
const referenceTTL = time.Minute

// noMatch is a code no row has: an unknown filter value matches nothing.
const noMatch = "__none__"

type refData struct {
	categories []campaigns.PublicCategory
	locations  []campaigns.Location
	catBySlug  map[string]string
	catCodes   map[string]bool
	locBySlug  map[string]string // kind|slug → code
	loaded     time.Time
}

type refCache struct {
	mu   sync.Mutex
	data *refData
}

func (r *refData) categoryCode(v string) string {
	if r.catCodes[v] {
		return v
	}
	if c, ok := r.catBySlug[v]; ok {
		return c
	}
	return noMatch
}

func (r *refData) locationCode(kind, slug string) string {
	if c, ok := r.locBySlug[kind+"|"+slug]; ok {
		return c
	}
	return noMatch
}

// reference returns the cached category and location lists (reloaded after referenceTTL).
func (s *Service) reference(ctx context.Context) (*refData, error) {
	s.ref.mu.Lock()
	defer s.ref.mu.Unlock()
	if d := s.ref.data; d != nil && s.now().Sub(d.loaded) < referenceTTL {
		return d, nil
	}
	cats, err := s.Campaigns.PublicCategories(ctx, "")
	if err != nil {
		return nil, err
	}
	locs, err := s.Campaigns.Locations(ctx)
	if err != nil {
		return nil, err
	}
	d := &refData{categories: cats, locations: locs, catBySlug: map[string]string{}, catCodes: map[string]bool{}, locBySlug: map[string]string{},
		loaded: s.now()}
	for _, c := range cats {
		d.catBySlug[c.Slug], d.catCodes[c.Code] = c.Code, true
	}
	for _, l := range locs {
		d.locBySlug[l.Kind+"|"+l.Slug] = l.Code
	}
	s.ref.data = d
	return d, nil
}
