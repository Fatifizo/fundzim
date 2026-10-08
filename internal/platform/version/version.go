// Package version exposes build metadata injected at link time:
//
//	go build -ldflags "-X github.com/Fatifizo/fundzim/internal/platform/version.Version=0.1.0 \
//	  -X github.com/Fatifizo/fundzim/internal/platform/version.Commit=$(git rev-parse --short HEAD) \
//	  -X github.com/Fatifizo/fundzim/internal/platform/version.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
package version

import "runtime/debug"

// Set via -ldflags. Defaults describe an unversioned development build.
var (
	Version   = "0.0.0-dev"
	Commit    = ""
	BuildTime = ""
)

// Info is the public build description. It contains no host, path or dependency details.
type Info struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Build   string `json:"build"`
	Commit  string `json:"commit"`
}

// Get returns build information. The commit falls back to the VCS stamp Go embeds in builds made from a
// git checkout, so `go run` and plain `go build` still report a commit.
func Get(appName string) Info {
	commit := Commit
	build := BuildTime
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if commit == "" && len(s.Value) >= 12 {
					commit = s.Value[:12]
				}
			case "vcs.time":
				if build == "" {
					build = s.Value
				}
			}
		}
	}
	if commit == "" {
		commit = "unknown"
	}
	if build == "" {
		build = "unknown"
	}
	return Info{Name: appName, Version: Version, Build: build, Commit: commit}
}
