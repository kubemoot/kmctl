package cmd

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/kubemoot/kmctl/internal/scaffold"
	"github.com/spf13/cobra"
)

// invocation is one `kmctl ...` command found in text. runnable marks a command
// written to be run as is (a line of a fenced code block, or a help Example), so
// its argument count is checked too; a mention in prose or a code span is
// checked for its command path and flags.
type invocation struct {
	where    string
	args     []string
	runnable bool
}

func (i invocation) String() string {
	return fmt.Sprintf("%s: kmctl %s", i.where, strings.Join(i.args, " "))
}

// kmctlWord finds "kmctl " as a word: not inside a path, a flag, or another word.
var kmctlWord = regexp.MustCompile(`(^|[^A-Za-z0-9_./-])kmctl\s`)

// commandEnd holds the characters that end a shell command outside quotes: a
// closing backtick or parenthesis, a separator, a pipe, or a redirection.
const commandEnd = "`);|&>"

// continuedLine finds a kmctl command continued onto the next line.
var continuedLine = regexp.MustCompile(`(?m)kmctl .*\\$`)

// kmctlInvocations finds every `kmctl ...` in a file's text. allRunnable treats
// every line as runnable (help Examples); otherwise only fenced code lines are.
func kmctlInvocations(file, text string, allRunnable bool) []invocation {
	var out []invocation
	fenced := false
	for n, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimPrefix(strings.TrimSpace(line), "$ ")
		if strings.HasPrefix(trimmed, "```") {
			fenced = !fenced
			continue
		}
		runnable := allRunnable || (fenced && strings.HasPrefix(trimmed, "kmctl "))
		for _, seg := range kmctlSegments(line) {
			out = append(out, invocation{where: fmt.Sprintf("%s:%d", file, n+1), args: tokenize(seg), runnable: runnable})
		}
	}
	return out
}

// kmctlSegments returns the text after each "kmctl " on a line; tokenize finds
// where each command ends.
func kmctlSegments(line string) []string {
	var segs []string
	for _, m := range kmctlWord.FindAllStringIndex(line, -1) {
		segs = append(segs, line[m[1]:])
	}
	return segs
}

// tokenize splits a command into shell words: a quoted part, glued or not, joins
// the word it touches. The command ends, outside quotes, at a commandEnd
// character or a # comment. Prose ends it too: an unquoted word with trailing
// punctuation (kept without it), or a quote that never closes (a YAML string
// around the mention). A mention in prose of a command without subcommands
// ("kmctl apply takes only ...") is checked for its path and flags, not its
// arguments.
func tokenize(seg string) []string {
	var toks []string
	for i := skipSpaces(seg, 0); i < len(seg) && seg[i] != '#'; i = skipSpaces(seg, i) {
		word, next, last := shellWord(seg, i)
		if word != "" {
			toks = append(toks, word)
		}
		if last {
			return toks
		}
		i = next
	}
	return toks
}

func skipSpaces(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return i
}

// shellWord reads the word at s[i:]. last reports that the command ends with it.
func shellWord(s string, i int) (word string, next int, last bool) {
	var b strings.Builder
	quoted := false
	for ; i < len(s) && s[i] != ' ' && s[i] != '\t'; i++ {
		switch {
		case strings.IndexByte(commandEnd, s[i]) >= 0:
			return redirectFree(b.String(), s[i]), i, true
		case s[i] == '"' || s[i] == '\'':
			end := strings.IndexByte(s[i+1:], s[i])
			if end < 0 {
				return endOfProse(b.String(), quoted), len(s), true
			}
			b.WriteString(s[i+1 : i+1+end])
			quoted = true
			i += end + 1
		default:
			b.WriteByte(s[i])
		}
	}
	word = b.String()
	if trimmed := endOfProse(word, quoted); trimmed != word {
		return trimmed, i, true
	}
	return word, i, false
}

// redirectFree drops a file-descriptor number glued to a redirection ("2>").
func redirectFree(word string, end byte) string {
	if end == '>' && strings.Trim(word, "0123456789") == "" {
		return ""
	}
	return word
}

// endOfProse drops trailing sentence punctuation from an unquoted word. A bare
// "." is a path and stays.
func endOfProse(word string, quoted bool) string {
	trimmed := strings.TrimRight(word, ".,:;]")
	if quoted || trimmed == "" {
		return word
	}
	return trimmed
}

// checkInvocation resolves an invocation against a fresh command tree (flag
// values must not leak between checks): the path must name a runnable command,
// its flags must exist, and a runnable invocation must pass its Args rule.
func checkInvocation(inv invocation) error {
	root := NewRootCommand()
	cmd, rest, err := root.Find(inv.args)
	if err != nil {
		return err
	}
	if cmd == root {
		return fmt.Errorf("no such command")
	}
	if !cmd.Runnable() {
		return fmt.Errorf("%q is a command group, not a command", cmd.CommandPath())
	}
	if err := cmd.ParseFlags(rest); err != nil {
		return fmt.Errorf("%s: %w", cmd.CommandPath(), err)
	}
	args := cmd.Flags().Args()
	if cmd.HasSubCommands() && len(args) > 0 {
		return fmt.Errorf("%s has no subcommand %q", cmd.CommandPath(), args[0])
	}
	if inv.runnable {
		if err := cmd.ValidateArgs(args); err != nil {
			return fmt.Errorf("%s: %w", cmd.CommandPath(), err)
		}
	}
	return nil
}

// scaffoldCase is a set of create options and the runnable commands its README
// must walk the reader through.
type scaffoldCase struct {
	opts scaffold.Options
	want []string
}

var (
	bundleSteps = []string{"apply", "conversation ask", "crew get", "fitness get", "fitness run"}
	chartSteps  = []string{"conversation ask", "crew get", "fitness get", "fitness run"}
)

// scaffoldCases cover every branch of the templates that can name a command:
// bundle and chart, one and five specialists, no Models, a custom namespace.
var scaffoldCases = map[string]scaffoldCase{
	"bundle-1":    {scaffold.Options{Name: "hello", Members: 1, ModelFamily: "qwen", Providers: []string{"ollama"}}, bundleSteps},
	"bundle-5-ns": {scaffold.Options{Name: "hello", Members: 5, ModelFamily: "qwen", Providers: []string{"ollama"}, Namespace: "team-a"}, bundleSteps},
	"bundle-bare": {scaffold.Options{Name: "hello", Members: 1}, bundleSteps},
	"chart-1":     {scaffold.Options{Name: "hello", Members: 1, ModelFamily: "qwen", Providers: []string{"ollama"}, Chart: true}, chartSteps},
	"chart-5":     {scaffold.Options{Name: "hello", Members: 5, Chart: true}, chartSteps},
}

// renderedInvocations renders one scaffold and returns its kmctl invocations,
// failing on a kmctl line continued with a backslash (the extractor reads one line).
func renderedInvocations(t *testing.T, name string, o scaffold.Options) []invocation {
	t.Helper()
	files, err := scaffold.Generate(o)
	if err != nil {
		t.Fatalf("%s: Generate: %v", name, err)
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var invs []invocation
	for _, p := range paths {
		if continuedLine.MatchString(files[p]) {
			t.Errorf("%s/%s: a kmctl line continues with a backslash; keep each command on one line", name, p)
		}
		invs = append(invs, kmctlInvocations(name+"/"+p, files[p], false)...)
	}
	return invs
}

// commandPath is the resolved command path of a valid invocation, without "kmctl".
func commandPath(inv invocation) string {
	cmd, _, _ := NewRootCommand().Find(inv.args)
	return strings.TrimPrefix(cmd.CommandPath(), "kmctl ")
}

// Every kmctl command the starter crew's README, chart, and manifests tell a
// reader to run is a real command with real flags, and each README walks
// through its runnable steps.
func TestScaffoldNamesOnlyRealCommands(t *testing.T) {
	for name, c := range scaffoldCases {
		var steps []string
		for _, inv := range renderedInvocations(t, name, c.opts) {
			if err := checkInvocation(inv); err != nil {
				t.Errorf("%s: %v", inv, err)
				continue
			}
			if p := commandPath(inv); inv.runnable && !slices.Contains(steps, p) {
				steps = append(steps, p)
			}
		}
		sort.Strings(steps)
		if !slices.Equal(steps, c.want) {
			t.Errorf("%s: runnable kmctl steps = %v, want %v", name, steps, c.want)
		}
	}
}

// Every help Example in the command tree runs as written.
func TestExamplesNameOnlyRealCommands(t *testing.T) {
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, inv := range kmctlInvocations(c.CommandPath()+" example", c.Example, true) {
			if err := checkInvocation(inv); err != nil {
				t.Errorf("%s: %v", inv, err)
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(NewRootCommand())
}

// The checker rejects what it must: unknown commands and subcommands, unknown
// flags, command groups, and wrong argument counts on a runnable line.
func TestCheckInvocationRejects(t *testing.T) {
	bad := []struct {
		line     string
		runnable bool
	}{
		{"crew status hello", true},
		{"crewfitnesssuite get x", false},
		{"crew get hello --wait", false},
		{"crew", false},
		{"crew get", true},
		{"conversation ask x --message=hi --bogus", false},
		{`conversation ask x "pods (all); now" --bogus`, false},
		{"version extra", true},
	}
	for _, c := range bad {
		inv := invocation{args: tokenize(c.line), runnable: c.runnable}
		if checkInvocation(inv) == nil {
			t.Errorf("kmctl %s: want an error", c.line)
		}
	}
	good := invocation{args: tokenize("crew get hello -n crew-hello"), runnable: true}
	if err := checkInvocation(good); err != nil {
		t.Errorf("%s: %v", good, err)
	}
}

// The wordings the scaffold used to ship are caught end to end.
func TestOldScaffoldWordingIsRejected(t *testing.T) {
	text := "```bash\nkmctl crew status hello -n crew-hello   # wait for Ready\n```\n" +
		"# kmctl applies only kubemoot.ai kinds, so apply this file with kubectl:\n"
	invs := kmctlInvocations("old", text, false)
	if len(invs) != 2 {
		t.Fatalf("want 2 invocations, got %v", invs)
	}
	for _, inv := range invs {
		if checkInvocation(inv) == nil {
			t.Errorf("%s: want an error", inv)
		}
	}
}

func TestTokenize(t *testing.T) {
	cases := map[string][]string{
		"":                               nil,
		"create.":                        {"create"},
		"create: a guide":                {"create"},
		"model list, then":               {"model", "list"},
		"apply -n ns -f .":               {"apply", "-n", "ns", "-f", "."},
		`ask x "What is up? Yes." -n ns`: {"ask", "x", "What is up? Yes.", "-n", "ns"},
		`ask x --message="hi there" -q`:  {"ask", "x", "--message=hi there", "-q"},
		`ask x 'single quoted' -q`:       {"ask", "x", "single quoted", "-q"},
		`create."`:                       {"create"},
		`ask x "never closed`:            {"ask", "x"},
		"get  a\tb":                      {"get", "a", "b"},
		`ask x "a (b); c | d" -q`:        {"ask", "x", "a (b); c | d", "-q"},
		"get x) and more":                {"get", "x"},
		"get x; echo":                    {"get", "x"},
		"get x>out":                      {"get", "x"},
		"get x 2> err":                   {"get", "x"},
		"get x # note":                   {"get", "x"},
		"get x && kmctl y":               {"get", "x"},
	}
	for in, want := range cases {
		if got := tokenize(in); !slices.Equal(got, want) {
			t.Errorf("tokenize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKmctlInvocationsExtraction(t *testing.T) {
	text := strings.Join([]string{
		"Scaffolded by `kmctl create`: a guide.",
		"description: \"scaffolded by kmctl create.\"",
		"# pointing at a provider (kmctl model list):",
		"```bash",
		"kmctl conversation ask hello \"What is up? Tell me.\" -n ns   # comment",
		"$ kmctl apply -n ns -f . && kmctl crew get hello; echo done",
		"kmctl fitness run -f f.yaml > out.txt 2> err.txt",
		"```",
		"Apply it again (`kmctl apply -n ns -f .`); then",
		"Quoted 'kmctl crew list' and [kmctl agent list].",
		"notkmctl x, /usr/bin/kmctl y, --kmctl z",
	}, "\n")
	got := kmctlInvocations("f", text, false)
	want := []struct {
		line     string
		runnable bool
	}{
		{"f:1: kmctl create", false},
		{"f:2: kmctl create", false},
		{"f:3: kmctl model list", false},
		{"f:5: kmctl conversation ask hello What is up? Tell me. -n ns", true},
		{"f:6: kmctl apply -n ns -f .", true},
		{"f:6: kmctl crew get hello", true},
		{"f:7: kmctl fitness run -f f.yaml", true},
		{"f:9: kmctl apply -n ns -f .", false},
		{"f:10: kmctl crew list", false},
		{"f:10: kmctl agent list", false},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d invocations %v, want %d", len(got), got, len(want))
	}
	for i, w := range want {
		if got[i].String() != w.line || got[i].runnable != w.runnable {
			t.Errorf("invocation %d = %q (runnable %v), want %q (runnable %v)", i, got[i], got[i].runnable, w.line, w.runnable)
		}
	}
}
