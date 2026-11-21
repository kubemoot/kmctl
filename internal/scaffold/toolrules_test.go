package scaffold

import (
	"regexp"
	"strings"
	"testing"
)

// Prompts hold behavior; how to call a tool belongs in the tool's own description
// (MCPServer toolOverrides) and what a model already knows belongs nowhere. These
// tests fail when a scaffolded PromptModule carries a tool-routing rule again.

// toolName matches the names of the tools the starter crew's servers register: the
// Kubernetes MCP server's families and the workspace server's.
var toolName = regexp.MustCompile(`\b(pods|resources|events|namespaces|nodes|helm|workspace|configuration)_[a-z_]+\b`)

// callATool matches a THEN clause that tells the agent to call, use, or invoke a
// specific tool or kind of tool. "call them first" is the behavior rule that stays.
var callATool = regexp.MustCompile(`(?i)\b(call|use|invoke|run|query)\s+(?:the\s+|a\s+|an\s+|this\s+)?(?:[\w-]+\s+)?tool\b`)

// toolRoutingRule reports whether a prompt line is a tool-routing rule: any rule line
// that names a tool, or a WHEN/THEN whose THEN says to call or use a tool.
func toolRoutingRule(line string, known []string) bool {
	line = strings.TrimSpace(line)
	if toolName.MatchString(line) {
		return true
	}
	for _, k := range known {
		if strings.Contains(line, k) {
			return true
		}
	}
	_, then, ok := strings.Cut(line, " THEN ")
	return ok && callATool.MatchString(then)
}

func knownTools() []string {
	var out []string
	for _, s := range specialists {
		out = append(out, s.Tools...)
	}
	return out
}

func TestToolRoutingRule_Detects(t *testing.T) {
	known := knownTools()
	for line, want := range map[string]bool{
		`WHEN asked about pods THEN call pods_list_in_namespace with the namespace`:          true,
		`WHEN asked about Jobs THEN call resources_list with apiVersion "batch/v1"`:          true,
		`WHEN a pod is not ready THEN read its log with pods_log`:                            true,
		`WHEN asked about a pod THEN use the pods tool`:                                      true,
		`WHEN asked about a pod THEN invoke a tool for it`:                                   true,
		`ALWAYS pass namespace "x" to events_list`:                                           true,
		`WHEN the question touches your domain AND you have tools THEN call them first`:      false,
		`WHEN a tool returns an error THEN report the error; NEVER present an error as data`: false,
		`NEVER tell a specialist not to use a tool`:                                          false,
		`WHEN a question names a namespace THEN read that namespace`:                         false,
		`internal signals (agree, concern, stand_aside, block)`:                              false,
		`NEVER respond NOTHING_TO_ADD only because the question needs live data`:             false,
	} {
		if got := toolRoutingRule(line, known); got != want {
			t.Errorf("toolRoutingRule(%q) = %v, want %v", line, got, want)
		}
	}
}

// ruleLines are the lines of a prompt that state a rule.
func ruleLines(prompt string) []string {
	var out []string
	for _, l := range strings.Split(prompt, "\n") {
		l = strings.TrimSpace(l)
		for _, kw := range []string{"WHEN ", "ALWAYS ", "NEVER ", "ASSERT "} {
			if strings.HasPrefix(l, kw) {
				out = append(out, l)
				break
			}
		}
	}
	return out
}

func TestGenerate_PromptsHoldNoToolRoutingRules(t *testing.T) {
	known := knownTools()
	forms := []Options{bundle(MaxMembers), {Name: "demo", Members: MaxMembers, Chart: true}}
	for _, o := range forms {
		files, err := Generate(o)
		if err != nil {
			t.Fatal(err)
		}
		checked := 0
		for name, content := range files {
			if !strings.HasSuffix(name, "promptmodules.yaml") {
				continue
			}
			for _, l := range ruleLines(content) {
				checked++
				if toolRoutingRule(l, known) {
					t.Errorf("%s (chart=%v) has a tool-routing rule; put how to call the tool in its description (toolOverrides) instead: %s", name, o.Chart, l)
				}
			}
		}
		if checked == 0 {
			t.Errorf("chart=%v: no prompt rules found to check", o.Chart)
		}
	}
}
