// Package buildinfo reports which lokal binary is running. Release builds stamp
// the values through -ldflags (see the Makefile); a plain `go install` build has
// no ldflags, so the module version and VCS metadata recorded by the toolchain
// fill the gaps instead of printing "dev" for a tagged install.
package buildinfo

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// Stamped by -ldflags "-X github.com/hi-donwi/Agent-Local/internal/buildinfo.Version=...".
var (
	Version = ""
	Commit  = ""
	Date    = ""
)

// Info identifies one build of lokal.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"go"`
}

// String is the one-line form printed by `lokal version`.
func (i Info) String() string {
	return fmt.Sprintf("lokal %s (commit %s, built %s, %s)", i.Version, i.Commit, i.Date, i.GoVersion)
}

// Get resolves the running binary's build information.
func Get() Info {
	bi, ok := debug.ReadBuildInfo()
	return resolve(Version, Commit, Date, bi, ok)
}

func resolve(version, commit, date string, bi *debug.BuildInfo, ok bool) Info {
	info := Info{Version: version, Commit: commit, Date: date, GoVersion: runtime.Version()}
	if ok && bi != nil {
		if bi.GoVersion != "" {
			info.GoVersion = bi.GoVersion
		}
		if info.Version == "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			info.Version = strings.TrimPrefix(bi.Main.Version, "v")
		}
		var revision, modified string
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				revision = s.Value
			case "vcs.time":
				if info.Date == "" {
					info.Date = s.Value
				}
			case "vcs.modified":
				modified = s.Value
			}
		}
		if info.Commit == "" && revision != "" {
			info.Commit = revision[:min(12, len(revision))]
			if modified == "true" {
				info.Commit += "-dirty"
			}
		}
	}
	if info.Version == "" {
		info.Version = "dev"
	}
	if info.Commit == "" {
		info.Commit = "unknown"
	}
	if info.Date == "" {
		info.Date = "unknown"
	}
	return info
}
