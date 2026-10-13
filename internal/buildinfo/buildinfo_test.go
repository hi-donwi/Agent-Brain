package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestResolvePrefersLinkerStampedValues(t *testing.T) {
	bi := &debug.BuildInfo{
		GoVersion: "go1.26.5",
		Main:      debug.Module{Version: "v9.9.9"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "ffffffffffffffffffffffffffffffffffffffff"},
			{Key: "vcs.time", Value: "2000-01-01T00:00:00Z"},
		},
	}

	got := resolve("0.1.0", "abc1234", "2026-10-13T00:00:00Z", bi, true)

	want := Info{Version: "0.1.0", Commit: "abc1234", Date: "2026-10-13T00:00:00Z", GoVersion: "go1.26.5"}
	if got != want {
		t.Fatalf("resolve() = %+v, want %+v", got, want)
	}
}

func TestResolveFallsBackToModuleAndVCSMetadata(t *testing.T) {
	// A `go install github.com/hi-donwi/Agent-Local/cmd/lokal@v0.2.0` build has no
	// ldflags, but the toolchain records the module version and VCS state.
	bi := &debug.BuildInfo{
		GoVersion: "go1.27.1",
		Main:      debug.Module{Version: "v0.2.0"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "0123456789abcdef0123456789abcdef01234567"},
			{Key: "vcs.time", Value: "2026-11-01T10:00:00Z"},
			{Key: "vcs.modified", Value: "true"},
		},
	}

	got := resolve("", "", "", bi, true)

	want := Info{Version: "0.2.0", Commit: "0123456789ab-dirty", Date: "2026-11-01T10:00:00Z", GoVersion: "go1.27.1"}
	if got != want {
		t.Fatalf("resolve() = %+v, want %+v", got, want)
	}
}

func TestResolveIgnoresDevelModuleVersion(t *testing.T) {
	bi := &debug.BuildInfo{GoVersion: "go1.26.5", Main: debug.Module{Version: "(devel)"}}

	got := resolve("", "", "", bi, true)

	want := Info{Version: "dev", Commit: "unknown", Date: "unknown", GoVersion: "go1.26.5"}
	if got != want {
		t.Fatalf("resolve() = %+v, want %+v", got, want)
	}
}

func TestResolveWithoutBuildInfo(t *testing.T) {
	got := resolve("", "", "", nil, false)

	if got.Version != "dev" || got.Commit != "unknown" || got.Date != "unknown" {
		t.Fatalf("resolve() = %+v, want dev/unknown/unknown", got)
	}
	if got.GoVersion == "" {
		t.Fatal("GoVersion must fall back to the running toolchain version")
	}
}

func TestInfoString(t *testing.T) {
	i := Info{Version: "0.1.0", Commit: "abc1234", Date: "2026-10-13T00:00:00Z", GoVersion: "go1.26.5"}

	want := "lokal 0.1.0 (commit abc1234, built 2026-10-13T00:00:00Z, go1.26.5)"
	if got := i.String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}
