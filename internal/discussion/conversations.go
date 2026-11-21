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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/rest"
)

// ListLimit is how many recent conversations kmctl asks the gateway for, its maximum.
const ListLimit = 100

// ErrListUnsupported means the crew's gateway does not list conversations: it is
// older than this kmctl, or the crew has no gateway.
var ErrListUnsupported = errors.New("the crew's discussion gateway does not list conversations " +
	"(it is older than this kmctl, or the crew has no gateway)")

// ErrWorkspaceNotFound means the gateway has no workspace object under the key.
var ErrWorkspaceNotFound = errors.New("workspace object not found: a thread's workspace is deleted after the thread " +
	"closes, and a gateway older than this kmctl cannot fetch workspace objects")

// ConversationInfo is one recent conversation of a crew, as the gateway lists it.
type ConversationInfo struct {
	ConversationID string `json:"conversationId"`
	Question       string `json:"question,omitempty"`
	StartedAt      string `json:"startedAt,omitempty"`
	LastTurnAt     string `json:"lastTurnAt,omitempty"`
	Turns          int    `json:"turns,omitempty"`
	// Owner is the pseudonymous id of the caller that started the conversation; the
	// gateway names it only to an operator listing every caller's conversations.
	Owner string `json:"owner,omitempty"`
}

// List returns the crew's recent conversations, newest first: the caller's own on a
// crew that verifies callers.
func List(ctx context.Context, rc rest.Interface, crew string, limit int) ([]ConversationInfo, error) {
	return ListAs(ctx, rc, crew, limit, false)
}

// ListAs is List; with all it asks for every caller's conversations, which the gateway
// allows an operator with a reason. A 404 then means refused, not that the gateway
// cannot list, so it is returned as the gateway's status.
func ListAs(ctx context.Context, rc rest.Interface, crew string, limit int, all bool) ([]ConversationInfo, error) {
	req := rc.Get().
		Suffix("api", "v1", "discussions", crew).
		Param("limit", strconv.Itoa(limit))
	if all {
		req = req.Param("all", "true")
	}
	raw, err := req.DoRaw(ctx)
	if !all && missingEndpoint(err) {
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

// Gateway routes that serve a workspace object: the current one, then the name a gateway
// older than this kmctl answers to.
const (
	workspaceRoute       = "workspace"
	legacyWorkspaceRoute = "artifact"
)

// FetchWorkspace opens the workspace object under key in the discussion's workspace. It asks
// the workspace route and, when the gateway does not have it, the route's old name.
func FetchWorkspace(ctx context.Context, rc rest.Interface, crew, key string) (io.ReadCloser, error) {
	body, err := fetchRoute(ctx, rc, crew, key, workspaceRoute)
	if missingEndpoint(err) {
		body, err = fetchRoute(ctx, rc, crew, key, legacyWorkspaceRoute)
	}
	if missingEndpoint(err) {
		return nil, ErrWorkspaceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("fetch workspace object %s: %w", key, err)
	}
	return body, nil
}

func fetchRoute(ctx context.Context, rc rest.Interface, crew, key, route string) (io.ReadCloser, error) {
	return rc.Get().
		Suffix("api", "v1", "discussions", crew, route).
		Param("key", key).
		Stream(ctx)
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

// ErrUnknownConversation means the ID is in no recent conversation of the crew.
var ErrUnknownConversation = errors.New("no recent conversation or thread has that ID")

// CheckKnown fails at once when the crew's recent conversations do not include id, so a
// command that reads a finished turn never waits on a stream that will not produce one.
// A gateway that cannot list conversations cannot say, and the ID is taken as given.
func CheckKnown(id string, list func() ([]ConversationInfo, error)) error {
	conversations, err := list()
	if err != nil {
		return nil //nolint:nilerr // an unlisted crew is not a missing conversation
	}
	for _, c := range conversations {
		if c.ConversationID == id {
			return nil
		}
	}
	return fmt.Errorf("%w: %s (kmctl conversation list shows the recent ones; the gateway keeps about a day)",
		ErrUnknownConversation, id)
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
