package campaigns

import (
	"regexp"
	"strconv"

	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

var slugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func validID(id string) bool     { return ids.Valid(id) }
func validSlug(slug string) bool { return len(slug) <= 96 && slugRe.MatchString(slug) }

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
