// Package archtest enforces the module rules of docs/stage-2/design-baseline.md mechanically
// (docs/architecture/dependency-rules.md):
//   - §4 dependency graph: a domain module imports only the modules it is allowed to;
//   - §5 table ownership: a module's SQL references only tables it owns.
//
// It parses the source (no build tags, test files excluded), so it runs as an ordinary unit test.
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/Fatifizo/fundzim/"

// allowedImports is design-baseline §4 for the modules that exist so far. "platform" means any
// internal/platform/* package. internal/app (composition root) and cmd/ may import everything.
var allowedImports = map[string][]string{
	"platform":      {"platform"},
	"audit":         {"platform"},
	"users":         {"platform", "audit"},
	"organisations": {"platform", "audit", "users"},
	"notifications": {"platform", "audit", "users"},
	"auth":          {"platform", "audit", "users", "notifications"},
}

// ownedTables is design-baseline §5 (plus the Stage 4 additions recorded in docs/stage-4/implementation.md:
// auth_tokens, mfa_login_challenges, session_events belong to auth; user_terms_acceptances to users).
var ownedTables = map[string][]string{
	"users": {"app.users", "app.user_profiles", "app.user_emails", "app.user_phone_numbers", "app.user_terms_acceptances"},
	"auth": {"app.authentication_identities", "app.password_credentials", "app.otp_challenges", "app.sessions", "app.mfa_methods",
		"app.recovery_codes", "app.roles", "app.permissions", "app.role_permissions", "app.role_assignments",
		"app.role_assignment_requests", "app.break_glass_grants", "app.staff_conflict_declarations", "app.security_events",
		"app.auth_tokens", "app.mfa_login_challenges", "app.session_events"},
	"organisations": {"app.organisations", "app.organisation_roles", "app.organisation_members", "app.organisation_invitations",
		"app.organisation_verifications"},
	"audit":         {"audit.audit_events", "audit.security_audit_events", "audit.evidence_records", "audit.evidence_holds"},
	"notifications": {},
}

// moduleOf maps a repository-relative directory to its module ("" = not a constrained module).
func moduleOf(dir string) string {
	parts := strings.Split(filepath.ToSlash(dir), "/")
	if len(parts) < 2 || parts[0] != "internal" {
		return ""
	}
	switch parts[1] {
	case "platform":
		return "platform"
	case "app", "archtest":
		return ""
	}
	return parts[1]
}

var tableRE = regexp.MustCompile(`\b(app|audit|kyc|ledger|risk|compliance|recon)\.([a-z_][a-z0-9_]*)\b`)

// functions in the app/audit schemas that any module may call (not tables)
var sharedRoutines = map[string]bool{"audit.verify_chain": true}

type pkgFile struct {
	module string
	path   string
	file   *ast.File
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for i := 0; i < 5; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("go.mod not found")
	return ""
}

func sources(t *testing.T) []pkgFile {
	t.Helper()
	root := repoRoot(t)
	var out []pkgFile
	fset := token.NewFileSet()
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		mod := moduleOf(filepath.Dir(rel))
		if mod == "" {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		out = append(out, pkgFile{module: mod, path: rel, file: f})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 20 {
		t.Fatalf("suspiciously few source files: %d", len(out))
	}
	return out
}

func TestModuleImportGraph(t *testing.T) {
	for _, f := range sources(t) {
		allowed, known := allowedImports[f.module]
		if !known {
			t.Errorf("%s: module %q has no entry in allowedImports (add it from design-baseline §4)", f.path, f.module)
			continue
		}
		for _, imp := range f.file.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if !strings.HasPrefix(p, modulePath) {
				continue
			}
			target := moduleOf(strings.TrimPrefix(p, modulePath))
			if target == "" {
				if strings.HasPrefix(strings.TrimPrefix(p, modulePath), "internal/app") {
					t.Errorf("%s imports the composition root %s", f.path, p)
				}
				continue
			}
			if target == f.module {
				continue
			}
			ok := false
			for _, a := range allowed {
				ok = ok || a == target
			}
			if !ok {
				t.Errorf("%s (module %s) imports %s (module %s): not allowed by design-baseline §4", f.path, f.module, p, target)
			}
		}
	}
}

func TestModuleSQLOnlyTouchesOwnedTables(t *testing.T) {
	owned := map[string]map[string]bool{}
	for m, ts := range ownedTables {
		owned[m] = map[string]bool{}
		for _, tbl := range ts {
			owned[m][tbl] = true
		}
	}
	seen := map[string]map[string]bool{}
	for _, f := range sources(t) {
		own, constrained := owned[f.module]
		if !constrained {
			continue // platform owns shared infrastructure tables; other modules are not built yet
		}
		ast.Inspect(f.file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			upper := strings.ToUpper(s)
			if !strings.Contains(upper, "SELECT") && !strings.Contains(upper, "INSERT") && !strings.Contains(upper, "UPDATE") &&
				!strings.Contains(upper, "DELETE") && !strings.Contains(upper, " FROM ") {
				return true
			}
			for _, m := range tableRE.FindAllString(s, -1) {
				if sharedRoutines[m] || own[m] {
					if seen[f.module] == nil {
						seen[f.module] = map[string]bool{}
					}
					seen[f.module][m] = true
					continue
				}
				t.Errorf("%s (module %s) references %s, which it does not own (design-baseline §5): use the owner's Go interface",
					f.path, f.module, m)
			}
			return true
		})
	}
	// sanity: the scanner actually found SQL in each module that has tables
	for _, m := range []string{"users", "auth", "organisations"} { // audit builds its table name from a constant
		if len(seen[m]) == 0 {
			t.Errorf("no SQL found for module %s: the scanner is broken", m)
		}
	}
	var mods []string
	for m := range seen {
		mods = append(mods, m)
	}
	sort.Strings(mods)
	for _, m := range mods {
		t.Logf("%s uses %d owned tables", m, len(seen[m]))
	}
}
