package discussion

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	idA = "6733c361-aa0c-4bde-85c6-e143af23b251"
	idB = "6733f000-1111-4bde-85c6-e143af23b251"
	idC = "9f00aa00-2222-4bde-85c6-e143af23b251"
)

var recent = []ConversationInfo{
	{ConversationID: idA, Question: "Describe the proxmox nodes."},
	{ConversationID: idB, Question: "What is failing?"},
	{ConversationID: idC},
	{ConversationID: "custom-id", Question: "A client chose this ID."},
}

func listOf(convs []ConversationInfo, err error) func() ([]ConversationInfo, error) {
	return func() ([]ConversationInfo, error) { return convs, err }
}

func TestResolveConversationID_FullIDNeedsNoList(t *testing.T) {
	called := false
	got, err := ResolveConversationID(idA, func() ([]ConversationInfo, error) { called = true; return nil, nil })
	if err != nil || got != idA || called {
		t.Fatalf("got %q, %v; listed %v", got, err, called)
	}
	if got, err := ResolveConversationID("", listOf(nil, errors.New("unused"))); err != nil || got != "" {
		t.Fatalf("no ID: got %q, %v", got, err)
	}
}

func TestResolveConversationID_UniquePrefix(t *testing.T) {
	for given, want := range map[string]string{"9f": idC, "6733c": idA, "6733f0": idB, "custom-id": "custom-id", "cust": "custom-id"} {
		if got, err := ResolveConversationID(given, listOf(recent, nil)); err != nil || got != want {
			t.Errorf("%s: got %q, %v; want %q", given, got, err, want)
		}
	}
}

func TestResolveConversationID_AmbiguousListsTheMatches(t *testing.T) {
	_, err := ResolveConversationID("6733", listOf(recent, nil))
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{`prefix "6733" is ambiguous`, idA + "  Describe the proxmox nodes.", idB + "  What is failing?"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}

func TestResolveConversationID_NoMatch(t *testing.T) {
	if _, err := ResolveConversationID("abc", listOf(recent, nil)); err == nil || !strings.Contains(err.Error(), `no recent conversation ID starts with "abc"`) {
		t.Fatalf("got %v", err)
	}
}

// An older gateway cannot list: a prefix fails with a clear message, a full ID works.
func TestResolveConversationID_OlderGateway(t *testing.T) {
	_, err := ResolveConversationID("6733c", listOf(nil, ErrListUnsupported))
	if err == nil || !strings.Contains(err.Error(), "pass the full ID") || !errors.Is(err, ErrListUnsupported) {
		t.Fatalf("got %v", err)
	}
	if got, err := ResolveConversationID(idA, listOf(nil, ErrListUnsupported)); err != nil || got != idA {
		t.Fatalf("full ID: got %q, %v", got, err)
	}
	boom := errors.New("gateway down")
	if _, err := ResolveConversationID("6733c", listOf(nil, boom)); !errors.Is(err, boom) {
		t.Fatalf("other list errors pass through: %v", err)
	}
}

func TestCompletions(t *testing.T) {
	got := Completions(recent, "6733")
	want := []string{idA + "\tDescribe the proxmox nodes.", idB + "\tWhat is failing?"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := Completions(recent, "9f"); len(got) != 1 || got[0] != idC {
		t.Errorf("an ID without a question has no description: %q", got)
	}
	if got := Completions(recent, "zz"); len(got) != 0 {
		t.Errorf("no match: %q", got)
	}
}

func TestShorten(t *testing.T) {
	if got := Shorten("  a\n b  ", 10); got != "a b" {
		t.Errorf("got %q", got)
	}
	if got := Shorten(strings.Repeat("x", 70), 60); len([]rune(got)) != 60 || !strings.HasSuffix(got, "...") {
		t.Errorf("got %q", got)
	}
}

func TestParseList(t *testing.T) {
	got, err := parseList([]byte(`{"conversations":[{"conversationId":"` + idA + `","question":"q","turns":2}]}`))
	if err != nil || len(got) != 1 || got[0].Turns != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if got, err := parseList([]byte(`{"conversations":[]}`)); err != nil || len(got) != 0 {
		t.Fatalf("empty: %+v, %v", got, err)
	}
	if _, err := parseList([]byte(`{"conversationId":"x"}`)); !errors.Is(err, ErrListUnsupported) {
		t.Fatalf("a reply without conversations: %v", err)
	}
	if _, err := parseList([]byte(`<html>not json</html>`)); !errors.Is(err, ErrListUnsupported) {
		t.Fatalf("a reply that is not JSON: %v", err)
	}
}

func TestMissingEndpoint(t *testing.T) {
	gr := schema.GroupResource{Resource: "services"}
	if !missingEndpoint(apierrors.NewNotFound(gr, "x")) || !missingEndpoint(apierrors.NewMethodNotSupported(gr, "get")) {
		t.Error("not found and method not allowed are a missing endpoint")
	}
	if missingEndpoint(nil) || missingEndpoint(apierrors.NewServiceUnavailable("down")) {
		t.Error("no error or another error is not")
	}
}

func TestParseSSE_DecodesTheGatewayStreamFields(t *testing.T) {
	stream := `data: {"type":"finding","agent":"k8s","signal":"failure","timestamp":"2026-10-15T09:00:16.4Z","modelName":"qwen3:14b","inputTokens":5210,"outputTokens":640,"failedTool":"pods_log","artifact":{"key":"ns/c/t/k8s/agree-1","bytes":12}}` + "\n\n" +
		`data: {"type":"subcommittee","source":"advisory","members":[{"agent":"k8s","reason":"pods"}],"brief":"Check the pods."}` + "\n\n"
	var got []Event
	if err := ParseSSE(strings.NewReader(stream), func(e Event) bool { got = append(got, e); return false }); err != nil {
		t.Fatal(err)
	}
	f, s := got[0], got[1]
	want := SignalMeta{ModelName: "qwen3:14b", InputTokens: 5210, OutputTokens: 640, FailedTool: "pods_log", Artifact: &ArtifactRef{Key: "ns/c/t/k8s/agree-1", Bytes: 12}}
	if f.Timestamp != "2026-10-15T09:00:16.4Z" || f.SignalMeta == nil || !reflect.DeepEqual(*f.SignalMeta, want) {
		t.Errorf("finding: %+v %+v", f, f.SignalMeta)
	}
	if s.Subcommittee == nil || s.Members[0].Reason != "pods" || s.Brief != "Check the pods." || s.SignalMeta != nil {
		t.Errorf("subcommittee: %+v", s)
	}
}
