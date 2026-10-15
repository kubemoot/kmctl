package discussion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/kubemoot/kmctl/internal/client"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/rest"
)

// ListLimit is how many recent conversations kmctl asks the gateway for, its maximum.
const ListLimit = 100

// ErrListUnsupported means the crew's gateway does not list conversations: it is
// older than this kmctl, or the crew has no gateway.
var ErrListUnsupported = errors.New("the crew's discussion gateway does not list conversations " +
	"(it is older than this kmctl, or the crew has no gateway)")

// ErrArtifactNotFound means the gateway has no artifact under the key.
var ErrArtifactNotFound = errors.New("artifact not found: a thread's artifacts are deleted after the thread " +
	"closes, and a gateway older than this kmctl cannot fetch artifacts")

// ConversationInfo is one recent conversation of a crew, as the gateway lists it.
type ConversationInfo struct {
	ConversationID string `json:"conversationId"`
	Question       string `json:"question,omitempty"`
	StartedAt      string `json:"startedAt,omitempty"`
	LastTurnAt     string `json:"lastTurnAt,omitempty"`
	Turns          int    `json:"turns,omitempty"`
}

// List returns the crew's recent conversations, newest first.
func List(ctx context.Context, rc rest.Interface, namespace, crew string, limit int) ([]ConversationInfo, error) {
	raw, err := client.ServiceProxy(rc.Get(), namespace, ServiceName(crew), gatewayPort).
		Suffix("api", "v1", "discussions", crew).
		Param("limit", strconv.Itoa(limit)).
		DoRaw(ctx)
	if missingEndpoint(err) {
		return nil, ErrListUnsupported
	}
	if err != nil {
		return nil, fmt.Errorf("list the conversations of crew %q: %w", crew, err)
	}
	return parseList(raw)
}

// parseList reads the list reply. A reply that is not JSON or has no conversations
// field comes from something other than a gateway that lists conversations.
func parseList(raw []byte) ([]ConversationInfo, error) {
	var resp struct {
		Conversations *[]ConversationInfo `json:"conversations"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil || resp.Conversations == nil {
		return nil, ErrListUnsupported
	}
	return *resp.Conversations, nil
}

// FetchArtifact opens the artifact under key in the crew's artifact store.
func FetchArtifact(ctx context.Context, rc rest.Interface, namespace, crew, key string) (io.ReadCloser, error) {
	body, err := client.ServiceProxy(rc.Get(), namespace, ServiceName(crew), gatewayPort).
		Suffix("api", "v1", "discussions", crew, "artifact").
		Param("key", key).
		Stream(ctx)
	if missingEndpoint(err) {
		return nil, ErrArtifactNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("fetch artifact %s: %w", key, err)
	}
	return body, nil
}

// missingEndpoint reports whether err is the gateway, or the API server in front of
// it, saying the path or method is not there.
func missingEndpoint(err error) bool {
	return err != nil && (apierrors.IsNotFound(err) || apierrors.IsMethodNotSupported(err))
}

// fullID matches a conversation ID the gateway generates (a UUID), which kmctl uses
// as given without asking the gateway.
var fullID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ResolveConversationID turns a conversation ID or a unique prefix of one into the
// full ID, the way git resolves a short hash. A full UUID is used as given; anything
// else is looked up in the crew's recent conversations, which list returns.
func ResolveConversationID(given string, list func() ([]ConversationInfo, error)) (string, error) {
	if given == "" || fullID.MatchString(given) {
		return given, nil
	}
	conversations, err := list()
	if errors.Is(err, ErrListUnsupported) {
		return "", fmt.Errorf("%q is not a full conversation ID, and %w; pass the full ID", given, err)
	}
	if err != nil {
		return "", err
	}
	return matchPrefix(given, conversations)
}

// matchPrefix finds the one conversation whose ID is given or starts with it.
func matchPrefix(given string, conversations []ConversationInfo) (string, error) {
	var matches []ConversationInfo
	for _, c := range conversations {
		if c.ConversationID == given {
			return given, nil
		}
		if strings.HasPrefix(c.ConversationID, given) {
			matches = append(matches, c)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no recent conversation ID starts with %q", given)
	case 1:
		return matches[0].ConversationID, nil
	}
	lines := make([]string, 0, len(matches))
	for _, m := range matches {
		lines = append(lines, "  "+describeConversation(m))
	}
	return "", fmt.Errorf("conversation ID prefix %q is ambiguous; it matches:\n%s", given, strings.Join(lines, "\n"))
}

// describeConversation is an ID with its first question.
func describeConversation(c ConversationInfo) string {
	if c.Question == "" {
		return c.ConversationID
	}
	return c.ConversationID + "  " + Shorten(c.Question, questionWidth)
}

// questionWidth is the most characters of a question a completion or match shows.
const questionWidth = 60

// Completions are the shell completions for a conversation ID: each listed ID that
// starts with toComplete, with its first question as the description.
func Completions(conversations []ConversationInfo, toComplete string) []string {
	var out []string
	for _, c := range conversations {
		if !strings.HasPrefix(c.ConversationID, toComplete) {
			continue
		}
		entry := c.ConversationID
		if c.Question != "" {
			entry += "\t" + Shorten(c.Question, questionWidth)
		}
		out = append(out, entry)
	}
	return out
}

// Shorten is text on one line, cut to at most width characters with "...".
func Shorten(text string, width int) string {
	line := strings.Join(strings.Fields(text), " ")
	runes := []rune(line)
	if len(runes) <= width {
		return line
	}
	return strings.TrimRight(string(runes[:width-3]), " ") + "..."
}
