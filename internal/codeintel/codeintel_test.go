package codeintel_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hi-donwi/Agent-Local/internal/codeintel"
)

func TestExtractSymbols_Go(t *testing.T) {
	goCode := []byte(`package sample

// Config holds service settings.
type Config struct {
	Port int
}

type Runner interface {
	Run() error
}

const DefaultPort = 8080
var GlobalStatus = "ready"

func NewConfig() *Config {
	return &Config{Port: DefaultPort}
}

func (c *Config) Validate() error {
	return nil
}
`)

	symbols, err := codeintel.ExtractSymbols("pkg/sample.go", goCode)
	if err != nil {
		t.Fatalf("unexpected extract error: %v", err)
	}

	found := make(map[string]codeintel.SymbolKind)
	for _, s := range symbols {
		found[s.Name] = s.Kind
	}

	if found["Config"] != codeintel.KindStruct {
		t.Errorf("expected Config to be struct, got %s", found["Config"])
	}
	if found["Runner"] != codeintel.KindInterface {
		t.Errorf("expected Runner to be interface, got %s", found["Runner"])
	}
	if found["DefaultPort"] != codeintel.KindConstant {
		t.Errorf("expected DefaultPort to be constant, got %s", found["DefaultPort"])
	}
	if found["GlobalStatus"] != codeintel.KindVariable {
		t.Errorf("expected GlobalStatus to be variable, got %s", found["GlobalStatus"])
	}
	if found["NewConfig"] != codeintel.KindFunction {
		t.Errorf("expected NewConfig to be function, got %s", found["NewConfig"])
	}
	if found["Validate"] != codeintel.KindMethod {
		t.Errorf("expected Validate to be method, got %s", found["Validate"])
	}
}

func TestExtractSymbols_OtherLanguages(t *testing.T) {
	// 1. Python
	pyCode := []byte(`
class Calculator:
    def add(self, a, b):
        return a + b

def standalone_helper():
    pass
`)
	pySyms, err := codeintel.ExtractSymbols("calc.py", pyCode)
	if err != nil {
		t.Fatalf("py error: %v", err)
	}
	if len(pySyms) != 3 {
		t.Errorf("expected 3 python symbols, got %d", len(pySyms))
	}

	// 2. TypeScript
	tsCode := []byte(`
export interface User {
    id: string;
}

export type UserID = string;

export class UserService {
    getUser() {}
}

export function fetchUser() {}
`)
	tsSyms, err := codeintel.ExtractSymbols("service.ts", tsCode)
	if err != nil {
		t.Fatalf("ts error: %v", err)
	}
	if len(tsSyms) != 4 {
		t.Errorf("expected 4 ts symbols, got %d", len(tsSyms))
	}

	// 3. Rust
	rsCode := []byte(`
pub struct Container {
    value: i32,
}

pub enum Color {
    Red,
    Green,
}

pub trait Drawable {
    fn draw(&self);
}

pub fn render() {}
`)
	rsSyms, err := codeintel.ExtractSymbols("main.rs", rsCode)
	if err != nil {
		t.Fatalf("rs error: %v", err)
	}
	if len(rsSyms) != 5 {
		t.Errorf("expected 5 rs symbols, got %d", len(rsSyms))
	}
}

func TestRepoMap(t *testing.T) {
	tmpDir := t.TempDir()

	mainGo := filepath.Join(tmpDir, "main.go")
	mustWrite(t, mainGo, `package main
func main() {}
`)

	libGo := filepath.Join(tmpDir, "lib", "util.go")
	mustWrite(t, libGo, `package lib
type Helper struct {}
func (h *Helper) Do() {}
`)

	repoMap, err := codeintel.BuildRepoMap(tmpDir, 1000)
	if err != nil {
		t.Fatalf("build repomap failed: %v", err)
	}

	if repoMap.FileCount != 2 {
		t.Errorf("expected 2 files, got %d", repoMap.FileCount)
	}
	if !strings.Contains(repoMap.Formatted, "main.go") || !strings.Contains(repoMap.Formatted, "Helper") {
		t.Errorf("unexpected repomap formatting: %s", repoMap.Formatted)
	}

	// Token budget limit test
	tinyMap, err := codeintel.BuildRepoMap(tmpDir, 15)
	if err != nil {
		t.Fatalf("tiny build repomap failed: %v", err)
	}
	if len(tinyMap.Files) > 1 {
		t.Errorf("expected at most 1 file under tiny token budget, got %d", len(tinyMap.Files))
	}
}

func TestSymbolAndCodeSearch(t *testing.T) {
	tmpDir := t.TempDir()

	file1 := filepath.Join(tmpDir, "server.go")
	mustWrite(t, file1, `package app
type Server struct {}
func NewServer() *Server {
    return &Server{}
}
`)

	file2 := filepath.Join(tmpDir, "main.go")
	mustWrite(t, file2, `package main
import "app"
func main() {
    srv := app.NewServer()
    _ = srv
}
`)

	// 1. FindSymbol
	res, err := codeintel.FindSymbol(tmpDir, "NewServer", 10)
	if err != nil {
		t.Fatalf("find symbol failed: %v", err)
	}

	if len(res.Definitions) != 1 {
		t.Fatalf("expected 1 definition, got %d", len(res.Definitions))
	}
	if res.Definitions[0].Name != "NewServer" {
		t.Errorf("expected NewServer definition, got %s", res.Definitions[0].Name)
	}
	if len(res.References) == 0 {
		t.Errorf("expected at least 1 reference in main.go")
	}

	// 2. SearchCode
	matches, err := codeintel.SearchCode(tmpDir, `app\.NewServer`, 10)
	if err != nil {
		t.Fatalf("search code failed: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if !strings.Contains(matches[0].Snippet, "app.NewServer()") {
		t.Errorf("unexpected snippet: %s", matches[0].Snippet)
	}
}

func TestProjectDetector(t *testing.T) {
	tmpDir := t.TempDir()

	agentsMD := filepath.Join(tmpDir, "AGENTS.md")
	mustWrite(t, agentsMD, "# Agents Contract\nRules here.")

	makefile := filepath.Join(tmpDir, "Makefile")
	mustWrite(t, makefile, `.PHONY: build test vet
build:
	go build
test:
	go test
vet:
	go vet
`)

	meta, err := codeintel.DetectProject(tmpDir)
	if err != nil {
		t.Fatalf("detect project failed: %v", err)
	}

	if len(meta.Instructions) != 1 || meta.Instructions[0].Name != "AGENTS.md" {
		t.Errorf("expected AGENTS.md instruction, got %+v", meta.Instructions)
	}

	if len(meta.Commands.Build) == 0 || meta.Commands.Build[0] != "make build" {
		t.Errorf("expected make build, got %+v", meta.Commands.Build)
	}
	if len(meta.Commands.Test) == 0 || meta.Commands.Test[0] != "make test" {
		t.Errorf("expected make test, got %+v", meta.Commands.Test)
	}
	if len(meta.Commands.Lint) == 0 || meta.Commands.Lint[0] != "make vet" {
		t.Errorf("expected make vet, got %+v", meta.Commands.Lint)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
