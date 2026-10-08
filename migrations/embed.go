// Package migrations embeds the executable goose SQL migrations (ADR-028).
//
// Files are named YYYYMMDDHHMMSS_<description>.sql and applied in order by `fundzimctl migrate`.
// The design drafts in design/sql are NOT migrations.
package migrations

import (
	"embed"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

// FS holds every migration file.
//
//go:embed *.sql
var FS embed.FS

// ExpectedVersion returns the highest migration version embedded in this binary. /readyz reports
// not-ready while the database is behind it.
func ExpectedVersion() int64 {
	entries, err := fs.ReadDir(FS, ".")
	if err != nil {
		return 0
	}
	var versions []int64
	for _, e := range entries {
		name := e.Name()
		i := strings.IndexByte(name, '_')
		if i <= 0 || !strings.HasSuffix(name, ".sql") {
			continue
		}
		v, err := strconv.ParseInt(name[:i], 10, 64)
		if err == nil {
			versions = append(versions, v)
		}
	}
	if len(versions) == 0 {
		return 0
	}
	sort.Slice(versions, func(a, b int) bool { return versions[a] < versions[b] })
	return versions[len(versions)-1]
}
