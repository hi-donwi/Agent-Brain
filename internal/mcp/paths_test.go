package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Built at run time so that neither name appears in this file, which sits in the process
// directory a wrongly resolved relative path would search.
var (
	outsideMarker = "Outside" + "MarkerSecret"
	insideName    = "Inside" + "RootWorker"
)

// codeToolCalls are the tools that take an optional path, each with the arguments it
// needs besides that path.
var codeToolCalls = map[string]map[string]interface{}{
	"repo_map":         {"tokens": float64(1000)},
	"find_symbol":      {"name": outsideMarker},
	"search_code":      {"pattern": outsideMarker},
	"project_commands": {},
}

func pathTestRoots(t *testing.T) (root, outside string) {
	t.Helper()
	root, outside = t.TempDir(), t.TempDir()
	for dir, body := range map[string]string{
		filepath.Join(root, "sub"): "package sub\n\nfunc " + insideName + "() {}\n",
		outside:                    "package secret\n\nfunc " + outsideMarker + "() {}\n",
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "code.go"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n\ngo 1.26\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root, outside
}

func callWithPath(t *testing.T, s *Server, tool, path string) *protocol.CallToolResult {
	t.Helper()
	args := map[string]interface{}{}
	for k, v := range codeToolCalls[tool] {
		args[k] = v
	}
	if path != "" {
		args["path"] = path
	}
	result, err := s.CallTool(context.Background(), tool, args)
	if err != nil {
		t.Fatalf("%s(%q): %v", tool, path, err)
	}
	return result
}

func resultText(result *protocol.CallToolResult) string {
	var b strings.Builder
	for _, c := range result.Content {
		if text, ok := c.(*protocol.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String()
}

// The code tools read files, so a path argument must not reach past the project root:
// the agent policy contract denies every out-of-root path, and these tools are how a
// caller would otherwise read one without being judged.
func TestCodeToolsRejectPathsOutsideTheRoot(t *testing.T) {
	root, outside := pathTestRoots(t)
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	s := NewServer(Config{RootDir: root})
	for tool := range codeToolCalls {
		for _, path := range []string{outside, "..", "../" + filepath.Base(outside), "sub/../..", "escape"} {
			result := callWithPath(t, s, tool, path)
			text := resultText(result)
			if !result.IsError || !strings.Contains(text, "outside the project root") {
				t.Errorf("%s(%q) = %q, want an outside-the-root tool error", tool, path, text)
			}
			if strings.Contains(text, "func "+outsideMarker) || strings.Contains(text, "package secret") {
				t.Errorf("%s(%q) leaked content from outside the root: %q", tool, path, text)
			}
		}
	}
}

// A relative path is taken from the project root, not from the process directory, and
// no path means the root itself.
func TestCodeToolPathsAreRelativeToTheRoot(t *testing.T) {
	root, _ := pathTestRoots(t)
	s := NewServer(Config{RootDir: root})
	for _, path := range []string{"sub", "./sub", filepath.Join(root, "sub"), ""} {
		args := map[string]interface{}{"pattern": "func " + insideName}
		if path != "" {
			args["path"] = path
		}
		result, err := s.CallTool(context.Background(), "search_code", args)
		// The reply echoes the pattern, so only a match line proves the file was found.
		if err != nil || result.IsError || !strings.Contains(resultText(result), "code.go:3") {
			t.Errorf("search_code under %q = %v %q, want the root's %s", path, err, resultText(result), insideName)
		}
	}
	if result := callWithPath(t, s, "repo_map", "missing"); !result.IsError {
		t.Errorf("repo_map on a missing directory = %q, want a tool error", resultText(result))
	}
}
