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
	segments := splitShell(spliceContinuations(command))
	if len(segments) == 0 {
		return stricter(p.defaultDecision(), Ask)
	}
	out := Allow
	for _, seg := range segments {
		out = stricter(out, p.decideShellSegment(seg))
	}
	if hasUnanalyzableSyntax(command) {
		out = stricter(out, Ask)
	}
	return out
}

// decideShellSegment judges one command segment. The command it holds can be written in
// a way that hides the real command from a prefix match, so the quoting is resolved
// first, the segment is judged at every position a command can start from, and the
// head of the segment is judged with the assignments and the control words in front
// of it skipped.
func (p Policy) decideShellSegment(seg string) Decision {
	words, unterminated := scanShell(seg)
	if len(words) == 0 {
		return p.defaultDecision()
	}
	rules := p.shellRuleList()
	head := headPosition(words)
	headWords := words[head:]
	headSequence := commandSequence(words, head)
	if len(headSequence) == 0 {
		headSequence = headWords
	}
	headName := ""
	if len(headSequence) > 0 {
		headName = commandWord(headSequence[0].text)
	}

	prefixDecision, prefixMatched := matchShellRules(rules, headSequence)
	allDecision, allMatched := p.shellAllDecision()

	var base Decision
	switch {
	case allMatched && prefixMatched:
		base = stricter(allDecision, prefixDecision)
	case allMatched:
		base = allDecision
	case prefixMatched:
		base = prefixDecision
	default:
		base = p.defaultDecision()
		// A program that runs another program cannot be judged from its name alone:
		// what runs inside it is up to its arguments, not to the policy.
		if p.hasShellPrefixRules() && isShellExecutor(headName) {
			base = stricter(base, Ask)
		}
	}
	out := base

	// A command can also start behind a launcher, behind an inline command flag, or
	// behind an exec flag: "xargs rm" runs rm even though rm is not the first word.
	for _, i := range commandPositions(words, head, headName) {
		if d, ok := matchShellRules(rules, commandSequence(words, i)); ok {
			out = stricter(out, d)
		}
	}
	if unterminated || hasMixedWord(words) {
		out = stricter(out, Ask)
	}
	return out
}

// shellRule is a shell rule with its command prefix split into words.
type shellRule struct {
	command  []string
	decision Decision
}

// shellRuleList returns the shell rules that carry a command prefix.
func (p Policy) shellRuleList() []shellRule {
	var out []shellRule
	for _, rule := range p.Rules {
		if rule.Tool != ToolShell || rule.Command == "" {
			continue
		}
		words := strings.Fields(rule.Command)
		if len(words) == 0 {
			continue
		}
		out = append(out, shellRule{command: words, decision: rule.Decision})
	}
	return out
}

// shellAllDecision returns the decision of the rules that apply to every shell
// request, and whether such a rule exists.
func (p Policy) shellAllDecision() (Decision, bool) {
	var out Decision
	found := false
	for _, rule := range p.Rules {
		if rule.Tool != ToolShell || rule.Command != "" {
			continue
		}
		if !found || rule.Decision.rank() > out.rank() {
			out, found = rule.Decision, true
		}
	}
	return out, found
}

// matchShellRules returns the strictest decision among the rules whose command prefix
// matches the words of a command, and whether any of them matched.
func matchShellRules(rules []shellRule, words []shellWord) (Decision, bool) {
	if len(words) == 0 {
		return "", false
	}
	var out Decision
	found := false
	for _, rule := range rules {
		if !matchesShellRule(rule.command, words) {
			continue
		}
		if !found || rule.decision.rank() > out.rank() {
			out, found = rule.decision, true
		}
	}
	return out, found
}

// matchesShellRule reports whether the command prefix of a rule matches the words of a
// command. The first word of both sides is compared by its base name, so a rule on
// "rm" also covers "/bin/rm".
func matchesShellRule(want []string, words []shellWord) bool {
	if len(want) == 0 || len(words) < len(want) {
		return false
	}
	for i := range want {
		got := words[i].text
		if i == 0 {
			if commandWord(want[0]) != commandWord(got) {
				return false
			}
			continue
		}
		if want[i] != got {
			return false
		}
	}
	return true
}

// hasShellPrefixRules reports whether the policy carries a shell rule that a hidden
// command could slip past. Without one there is nothing for an unusual spelling to
// hide from.
func (p Policy) hasShellPrefixRules() bool {
	for _, rule := range p.Rules {
		if rule.Tool == ToolShell && rule.Command != "" {
			return true
		}
	}
	return false
}

// shellWord is one whitespace-separated word of a command segment with quoting
// resolved: quoted text is part of the word, and a word that mixes quoted and
// unquoted text is marked as mixed because its value is not literal. A word that is
// exactly one quoted region keeps its content, because that content can be a command
// of its own.
type shellWord struct {
	text  string
	mixed bool
	// payload holds the content of a word that is exactly one quoted region.
	payload string
	// isPayload reports whether the word is exactly one quoted region.
	isPayload bool
}

// commandSequence returns the words of the command that starts at a word: a quoted
// payload is a command of its own, everything else continues in the segment.
func commandSequence(words []shellWord, i int) []shellWord {
	if i < 0 || i >= len(words) {
		return nil
	}
	if words[i].isPayload {
		inner, _ := scanShell(words[i].payload)
		return inner
	}
	return words[i:]
}

// scanShell resolves quoting in one command segment. It returns the words and whether
// a quote was left open.
func scanShell(seg string) (words []shellWord, unterminated bool) {
	var cur, region strings.Builder
	var curMixed, sawQuote, otherText bool
	var regions int
	var inQuote byte

	flush := func() {
		if cur.Len() > 0 || sawQuote {
			w := shellWord{text: cur.String(), mixed: curMixed}
			if regions == 1 && !otherText {
				w.payload, w.isPayload = region.String(), true
			}
			words = append(words, w)
		}
		cur.Reset()
		region.Reset()
		curMixed, sawQuote, otherText = false, false, false
		regions = 0
	}

	for i := 0; i < len(seg); i++ {
		c := seg[i]
		switch {
		case inQuote != 0:
			if c == inQuote {
				inQuote = 0
				continue
			}
			if c == '\\' && inQuote == '"' && i+1 < len(seg) {
				i++
				cur.WriteByte(seg[i])
				region.WriteByte(seg[i])
				continue
			}
			cur.WriteByte(c)
			region.WriteByte(c)
		case c == '\'' || c == '"':
			if cur.Len() > 0 {
				curMixed = true
				otherText = true
			}
			inQuote = c
			sawQuote = true
			regions++
			region.Reset()
		case c == '\\' && i+1 < len(seg):
			i++
			cur.WriteByte(seg[i])
			otherText = true
		case c == ' ' || c == '\t':
			flush()
		default:
			cur.WriteByte(c)
			otherText = true
		}
	}
	unterminated = inQuote != 0
	flush()
	return words, unterminated
}

func hasMixedWord(words []shellWord) bool {
	for _, w := range words {
		if w.mixed {
			return true
		}
	}
	return false
}

// commandWord returns the name of the command a word names: grouping punctuation is
// removed and a path is reduced to its base name.
func commandWord(w string) string {
	w = strings.TrimLeft(w, "({!")
	w = strings.TrimRight(w, ")};")
	if w == "" {
		return ""
	}
	return filepath.Base(w)
}

// headPosition returns the index of the word that names the command of a segment,
// skipping the assignments and the control words that stand in front of a command.
func headPosition(words []shellWord) int {
	i := 0
	for i < len(words) && (isAssignment(words[i].text) || isShellPrefix(words[i].text)) {
		i++
	}
	return i
}

// commandPositions returns the indexes of the words that can start a command inside a
// segment, besides the head: behind a launcher, behind an inline command flag, or
// behind an exec flag.
func commandPositions(words []shellWord, head int, headWord string) []int {
	var out []int
	launcherAt := -1
	for i := head + 1; i < len(words); i++ {
		prev := words[i-1].text
		if isInlineCommandFlag(prev, headWord) || isExecFlag(prev) {
			out = append(out, i)
			continue
		}
		if isShellLauncher(prev) {
			launcherAt = i
		}
		if launcherAt >= 0 && i >= launcherAt {
			out = append(out, i)
		}
	}
	return out
}

// isAssignment reports whether a word sets a variable in front of a command.
func isAssignment(w string) bool {
	i := 0
	for i < len(w) && (w[i] == '_' || w[i] >= 'a' && w[i] <= 'z' || w[i] >= 'A' && w[i] <= 'Z' || w[i] >= '0' && w[i] <= '9') {
		i++
	}
	return i > 0 && i < len(w) && w[i] == '='
}

// shellPrefixes are the control words that stand in front of a command.
var shellPrefixes = map[string]bool{
	"(": true, "{": true, "!": true, "(((": true, "builtin": true,
	"command": true, "coproc": true, "do": true, "elif": true, "else": true,
	"eval": true, "exec": true, "then": true, "time": true,
}

// shellLaunchers are the words that run another command, so the command they run can
// sit in an argument position instead of the first one.
var shellLaunchers = map[string]bool{
	"doas": true, "env": true, "expect": true, "ionice": true, "nice": true,
	"nohup": true, "script": true, "setsid": true, "stdbuf": true, "su": true,
	"sudo": true, "timeout": true, "watch": true, "xargs": true,
}

// inlineCommandFlags are the flags that carry a command of their own. The short
// "-e" and "-E" only count behind an interpreter, where they carry code.
var inlineCommandFlags = map[string]bool{
	"-c": true, "/c": true, "-EncodedCommand": true,
}

// interpreterFlags are the flags that carry code behind an interpreter.
var interpreterFlags = map[string]bool{
	"-e": true, "-E": true,
}

// shellInterpreters are the programs whose short flags carry code.
var shellInterpreters = map[string]bool{
	"awk": true, "bash": true, "node": true, "perl": true, "php": true,
	"python": true, "python2": true, "python3": true, "ruby": true, "sh": true,
	"zsh": true,
}

// execFlags are the flags that run a command for every match, as find does.
var execFlags = map[string]bool{
	"-exec": true, "-execdir": true,
}

// shellExecutors are the programs whose arguments decide what runs. The engine cannot
// read them, so a request that reaches one is never better than ask.
var shellExecutors = map[string]bool{
	"adb": true, "ansible": true, "ansible-playbook": true, "ash": true, "awk": true,
	"bash": true, "bun": true, "bundle": true, "busybox": true, "cargo": true,
	"csh": true, "dash": true, "deno": true, "docker": true, "dotnet": true,
	"expect": true, "fish": true, "gawk": true, "gmake": true, "go": true,
	"gradle": true, "helm": true, "java": true, "js": true, "ksh": true,
	"kubectl": true, "make": true, "mawk": true, "mvn": true, "mysql": true,
	"nerdctl": true, "node": true, "npm": true, "npx": true, "perl": true,
	"php": true, "pip": true, "pip3": true, "podman": true, "poetry": true,
	"psql": true, "python": true, "python2": true, "python3": true, "rake": true,
	"redis-cli": true, "rsync": true, "ruby": true, "scp": true, "sed": true,
	"sftp": true, "sh": true, "ssh": true, "systemctl": true, "task": true,
	"tcsh": true, "terraform": true, "uv": true, "watch": true, "yarn": true,
	"zsh": true,
}

func isShellPrefix(word string) bool { return shellPrefixes[word] }

func isShellLauncher(word string) bool { return shellLaunchers[word] }

func isShellExecutor(word string) bool { return shellExecutors[word] }

func isExecFlag(word string) bool { return execFlags[word] }

// isInlineCommandFlag reports whether a flag carries a command of its own.
func isInlineCommandFlag(word string, headWord string) bool {
	if inlineCommandFlags[word] {
		return true
	}
	return interpreterFlags[word] && shellInterpreters[headWord]
}

// spliceContinuations removes the line continuations of a command, so a command
// written over two lines is judged as the one command it is.
func spliceContinuations(command string) string {
	if !strings.Contains(command, "\\\n") && !strings.Contains(command, "\\\r\n") {
		return command
	}
	return strings.NewReplacer("\\\r\n", "", "\\\n", "").Replace(command)
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
// expansion, command substitution, and redirection.
func hasUnanalyzableSyntax(command string) bool {
	return strings.ContainsAny(command, "$`<>")
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
