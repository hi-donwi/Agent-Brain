// Package agent holds the policy model an agent host needs to run a coding agent
// safely: project instruction discovery, tool permissions, and sub-agent definitions.
//
// The package decides and describes; it never reads a repository's contents beyond
// instruction files, never writes, and never runs a command. Executing a tool call
// stays with the agent host, under its approvals and isolation (ADR-0013).
package agent

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Decision is what the policy says about one tool request.
type Decision string

const (
	Allow Decision = "allow"
	Ask   Decision = "ask"
	Deny  Decision = "deny"
)

func (d Decision) valid() bool { return d == Allow || d == Ask || d == Deny }

func (d Decision) rank() int {
	switch d {
	case Allow:
		return 0
	case Deny:
		return 2
	default:
		return 1
	}
}

// stricter returns the more restrictive of two decisions. An unknown value counts as ask.
func stricter(a, b Decision) Decision {
	if b.rank() > a.rank() {
		return b
	}
	return a
}

// Tool names a policy can govern.
const (
	ToolRead     = "read"
	ToolSearch   = "search"
	ToolEdit     = "edit"
	ToolWrite    = "write"
	ToolShell    = "shell"
	ToolNetwork  = "network"
	ToolSubagent = "subagent"
)

var knownTools = []string{ToolRead, ToolSearch, ToolEdit, ToolWrite, ToolShell, ToolNetwork, ToolSubagent}

func isKnownTool(name string) bool {
	for _, t := range knownTools {
		if t == name {
			return true
		}
	}
	return false
}

func isPathTool(name string) bool {
	return name == ToolRead || name == ToolSearch || name == ToolEdit || name == ToolWrite
}

// Rule matches requests for one tool, optionally narrowed by a path glob (path tools)
// or a command prefix (shell), and says what to do with them.
type Rule struct {
	Tool     string   `yaml:"tool"`
	Path     string   `yaml:"path,omitempty"`
	Command  string   `yaml:"command,omitempty"`
	Decision Decision `yaml:"decision"`
}

// Policy is an ordered-insensitive rule set: among the rules that match a request the
// most restrictive decision wins, so adding a rule can never loosen what another rule
// already restricts. A request no rule matches gets Default (ask when empty).
type Policy struct {
	// Root bounds every path the policy will consider. A path outside it is denied.
	Root    string   `yaml:"-"`
	Default Decision `yaml:"default,omitempty"`
	Rules   []Rule   `yaml:"rules,omitempty"`
}

// Request is one tool call to be judged.
type Request struct {
	Tool    string
	Path    string
	Command string
}

// ParsePolicy reads a policy document and rejects anything it cannot interpret
// exactly: an unknown tool, decision, or field is an error, never a silent default.
func ParsePolicy(data []byte) (Policy, error) {
	var p Policy
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil && !errors.Is(err, io.EOF) {
		return Policy{}, fmt.Errorf("parse policy: %w", err)
	}
	if err := p.Validate(); err != nil {
		return Policy{}, err
	}
	return p, nil
}

// Validate checks the structure of the policy.
func (p Policy) Validate() error {
	if p.Default != "" && !p.Default.valid() {
		return fmt.Errorf("policy default %q must be allow, ask, or deny", p.Default)
	}
	for i, r := range p.Rules {
		if !isKnownTool(r.Tool) {
			return fmt.Errorf("rule %d: unknown tool %q", i+1, r.Tool)
		}
		if !r.Decision.valid() {
			return fmt.Errorf("rule %d: decision %q must be allow, ask, or deny", i+1, r.Decision)
		}
		if r.Path != "" {
			if !isPathTool(r.Tool) {
				return fmt.Errorf("rule %d: path applies only to read, search, edit, and write", i+1)
			}
			if filepath.IsAbs(r.Path) || strings.Contains(r.Path, "..") {
				return fmt.Errorf("rule %d: path %q must be relative and may not contain ..", i+1, r.Path)
			}
		}
		if r.Command != "" && r.Tool != ToolShell {
			return fmt.Errorf("rule %d: command applies only to shell", i+1)
		}
	}
	return nil
}

func (p Policy) defaultDecision() Decision {
	if p.Default.valid() {
		return p.Default
	}
	return Ask
}

// Decide judges one request. It is pure apart from resolving symbolic links on disk,
// and it fails closed: an unknown tool, a path it cannot place under Root, or a shell
// command it cannot fully analyze never comes back as allow.
func (p Policy) Decide(r Request) Decision {
	if !isKnownTool(r.Tool) {
		return Deny
	}
	switch {
	case isPathTool(r.Tool):
		return p.decidePath(r)
	case r.Tool == ToolShell:
		return p.decideShell(r.Command)
	default:
		return p.strictestOf(r.Tool, func(Rule) bool { return true })
	}
}

// strictestOf returns the most restrictive decision among the tool's rules accepted by
// match, or the default when none matches.
func (p Policy) strictestOf(tool string, match func(Rule) bool) Decision {
	var out Decision
	found := false
	for _, rule := range p.Rules {
		if rule.Tool != tool || !match(rule) {
			continue
		}
		if !found {
			out, found = rule.Decision, true
			continue
		}
		out = stricter(out, rule.Decision)
	}
	if !found {
		return p.defaultDecision()
	}
	return out
}

func (p Policy) decidePath(r Request) Decision {
	rel, ok := p.relativeToRoot(r.Path)
	if !ok {
		return Deny
	}
	return p.strictestOf(r.Tool, func(rule Rule) bool {
		return rule.Path == "" || globMatch(rule.Path, rel)
	})
}

// relativeToRoot places path under Root and returns it slash-separated and relative.
// Symbolic links in the part of the path that exists are resolved, so a link inside the
// project that points outside it does not escape. An empty path means the root itself.
func (p Policy) relativeToRoot(path string) (string, bool) {
	if p.Root == "" {
		return "", false
	}
	root, err := filepath.EvalSymlinks(p.Root)
	if err != nil {
		return "", false
	}
	if path == "" {
		path = "."
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(p.Root, abs)
	}
	abs = filepath.Clean(abs)
	resolved, ok := resolveExisting(abs)
	if !ok {
		return "", false
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		// The path as written may sit under an unresolved root alias (for example
		// /var versus /private/var on macOS); compare against the unresolved root too.
		raw, rerr := filepath.Rel(filepath.Clean(p.Root), abs)
		if rerr != nil || raw == ".." || strings.HasPrefix(raw, ".."+string(filepath.Separator)) || filepath.IsAbs(raw) {
			return "", false
		}
		// The unresolved form is inside the root lexically; the resolved form is not,
		// which means a link escaped. Refuse.
		if resolved != abs {
			return "", false
		}
		rel = raw
	}
	return filepath.ToSlash(rel), true
}

// resolveExisting resolves symbolic links in the longest existing ancestor of abs and
// re-appends the part that does not exist yet.
func resolveExisting(abs string) (string, bool) {
	rest := ""
	cur := abs
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			if rest == "" {
				return resolved, true
			}
			return filepath.Join(resolved, rest), true
		}
		if !os.IsNotExist(err) {
			return "", false
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", false
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

func (p Policy) decideShell(command string) Decision {
	segments := splitShell(command)
	if len(segments) == 0 {
		return stricter(p.defaultDecision(), Ask)
	}
	out := Allow
	for _, seg := range segments {
		d := p.strictestOf(ToolShell, func(rule Rule) bool {
			return rule.Command == "" || tokenPrefix(rule.Command, seg)
		})
		out = stricter(out, d)
	}
	if hasUnanalyzableSyntax(command) {
		out = stricter(out, Ask)
	}
	return out
}

// splitShell breaks a command line at the separators that start another command. It
// ignores quoting on purpose: splitting inside quotes only makes a command look like more
// commands, which can restrict it further but never loosen it.
func splitShell(command string) []string {
	f := func(r rune) bool { return r == ';' || r == '&' || r == '|' || r == '\n' || r == '\r' }
	var out []string
	for _, seg := range strings.FieldsFunc(command, f) {
		if s := strings.TrimSpace(seg); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// hasUnanalyzableSyntax reports syntax whose effect a prefix match cannot see:
// command substitution and redirection.
func hasUnanalyzableSyntax(command string) bool {
	return strings.Contains(command, "$(") || strings.Contains(command, "`") ||
		strings.ContainsAny(command, "<>")
}

// tokenPrefix reports whether the whitespace-separated tokens of prefix are the first
// tokens of command. "git status" matches "git status -s" but not "git statusx".
func tokenPrefix(prefix, command string) bool {
	want := strings.Fields(prefix)
	have := strings.Fields(command)
	if len(want) == 0 || len(have) < len(want) {
		return false
	}
	for i, w := range want {
		if have[i] != w {
			return false
		}
	}
	return true
}

// globMatch matches a slash-separated relative path against a glob where * matches
// within one path segment, ** matches across segments, and ? matches one character.
func globMatch(pattern, rel string) bool {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return false
	}
	return re.MatchString(rel)
}

// Layer combines a trusted policy (the operator's own) with an untrusted one (a file the
// repository ships). The untrusted layer can only tighten: its allow rules are dropped,
// and its default applies only when stricter. A repository can ask for more caution but
// can never grant itself permission.
func Layer(trusted, untrusted Policy) Policy {
	out := Policy{
		Root:    trusted.Root,
		Default: trusted.defaultDecision(),
		Rules:   append([]Rule(nil), trusted.Rules...),
	}
	if untrusted.Default.valid() {
		out.Default = stricter(out.Default, untrusted.Default)
	}
	for _, r := range untrusted.Rules {
		if r.Decision == Allow {
			continue
		}
		out.Rules = append(out.Rules, r)
	}
	return out
}

// Restrict derives a policy that permits only the listed tools, for a sub-agent. Every
// other tool is denied. The result is never more permissive than the receiver, and the
// receiver is not modified.
func (p Policy) Restrict(tools []string) Policy {
	keep := make(map[string]bool, len(tools))
	for _, t := range tools {
		keep[t] = true
	}
	out := Policy{
		Root:    p.Root,
		Default: p.Default,
		Rules:   append([]Rule(nil), p.Rules...),
	}
	for _, t := range knownTools {
		if !keep[t] {
			out.Rules = append(out.Rules, Rule{Tool: t, Decision: Deny})
		}
	}
	return out
}
