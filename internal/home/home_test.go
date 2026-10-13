package home

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func userHome(dir string) func() (string, error) {
	return func() (string, error) { return dir, nil }
}

func TestResolveDefaultsToDotLokalInTheUserHome(t *testing.T) {
	base := t.TempDir()

	got, err := Resolve(env(nil), userHome(base))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	root := filepath.Join(base, ".lokal")
	want := Layout{
		Root:       root,
		Config:     filepath.Join(root, "config.yaml"),
		DB:         filepath.Join(root, "lokal.db"),
		Token:      filepath.Join(root, "serve.token"),
		Skills:     filepath.Join(root, "skills"),
		Candidates: filepath.Join(root, "candidates"),
		Traces:     filepath.Join(root, "traces"),
	}
	if got != want {
		t.Fatalf("Resolve() =\n%+v\nwant\n%+v", got, want)
	}
}

func TestResolveHonoursLokalHome(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "data", "lokal")

	got, err := Resolve(env(map[string]string{EnvVar: custom + string(filepath.Separator)}), userHome("unused"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Root != custom {
		t.Fatalf("Root = %q, want %q (cleaned)", got.Root, custom)
	}
	if got.DB != filepath.Join(custom, "lokal.db") {
		t.Fatalf("DB = %q, want it under LOKAL_HOME", got.DB)
	}
}

func TestResolveIgnoresBlankLokalHome(t *testing.T) {
	base := t.TempDir()

	got, err := Resolve(env(map[string]string{EnvVar: "   "}), userHome(base))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Root != filepath.Join(base, ".lokal") {
		t.Fatalf("Root = %q, want the default under the user home", got.Root)
	}
}

func TestResolveRejectsRelativeLokalHome(t *testing.T) {
	// A relative path would put state wherever the command happens to run,
	// and an unexpanded "~/x" (quoted in a unit file) is relative too.
	for _, v := range []string{"lokal-data", "./lokal", "~/lokal"} {
		_, err := Resolve(env(map[string]string{EnvVar: v}), userHome(t.TempDir()))
		if err == nil || !strings.Contains(err.Error(), EnvVar) {
			t.Errorf("Resolve(%s=%q) error = %v, want one that names %s", EnvVar, v, err, EnvVar)
		}
	}
}

func TestResolveFailsWithoutAnyHome(t *testing.T) {
	noHome := func() (string, error) { return "", errors.New("$HOME is not defined") }

	_, err := Resolve(env(nil), noHome)
	if err == nil || !strings.Contains(err.Error(), EnvVar) {
		t.Fatalf("Resolve() error = %v, want a hint to set %s", err, EnvVar)
	}

	_, err = Resolve(env(nil), userHome(""))
	if err == nil {
		t.Fatal("Resolve() with an empty user home must fail")
	}
}

func TestEnsureCreatesPrivateDirectories(t *testing.T) {
	l, err := Resolve(env(map[string]string{EnvVar: filepath.Join(t.TempDir(), "home")}), userHome("unused"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if err := l.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if err := l.Ensure(); err != nil {
		t.Fatalf("Ensure must be idempotent: %v", err)
	}

	for _, dir := range []string{l.Root, l.Skills, l.Candidates, l.Traces} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat %s: %v", dir, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", dir)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
			t.Errorf("%s mode = %v, want 0700", dir, info.Mode().Perm())
		}
	}
	for _, file := range []string{l.Config, l.DB, l.Token} {
		if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("Ensure must not create file %s (stat err = %v)", file, err)
		}
	}
}

func TestEnsureFailsWhenRootIsAFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "home")
	if err := os.WriteFile(root, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := Resolve(env(map[string]string{EnvVar: root}), userHome("unused"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if err := l.Ensure(); err == nil {
		t.Fatal("Ensure must fail when the lokal home is a regular file")
	}
}
