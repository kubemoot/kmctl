package cmd

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kubemoot/kmctl/internal/clierr"
	"github.com/kubemoot/kmctl/internal/conversation"
	"github.com/kubemoot/kmctl/internal/discussion"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
)

// dropThenResume is a gateway whose first stream is cut mid-answer, before any synthesis
// or done. A second open must carry the Last-Event-ID of the last whole event.
func dropThenResume(resumes bool) (*httptest.Server, *[]string) {
	var mu sync.Mutex
	var ids []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/state") { // a gateway older than the state read
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		n := len(ids)
		ids = append(ids, r.Header.Get("Last-Event-ID"))
		mu.Unlock()
		switch {
		case n == 0:
			_, _ = w.Write([]byte("id: t1:5\ndata: {\"type\":\"thread_found\",\"threadId\":\"t1\"}\n\n"))
		case resumes:
			_, _ = w.Write([]byte("id: t1:9\ndata: {\"type\":\"synthesis\",\"content\":\"the answer\"}\n\n" +
				"id: t1:10\ndata: {\"type\":\"done\"}\n\n"))
		default:
			http.Error(w, "gateway down", http.StatusServiceUnavailable)
		}
	}))
	return srv, &ids
}

func follow(t *testing.T, srv *httptest.Server, mode conversation.Mode) (string, error) {
	t.Helper()
	defer discussion.SetResumeBounds(2, time.Millisecond)()
	rc, err := rest.UnversionedRESTClientFor(&rest.Config{
		Host:          srv.URL,
		ContentConfig: rest.ContentConfig{NegotiatedSerializer: scheme.Codecs.WithoutConversion()},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	opts := conversation.Options{Mode: mode, Crew: "pilot", Namespace: "crew-pilot", ConversationID: "conv-1", Start: time.Now()}
	err = followStream(context.Background(), cmd, rc, opts, streamFlags{idleTimeout: time.Minute}, nil)
	return out.String(), err
}

func TestFollowStreamResumesADroppedStreamAndPrintsTheAnswer(t *testing.T) {
	srv, ids := dropThenResume(true)
	defer srv.Close()

	out, err := follow(t, srv, conversation.ModeJSON)

	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"answer":"the answer"`) {
		t.Fatalf("no result with the answer:\n%s", out)
	}
	if len(*ids) != 2 || (*ids)[1] != "t1:5" {
		t.Fatalf("Last-Event-ID per open = %q, want the second to carry t1:5", *ids)
	}
}

func TestFollowStreamThatCannotResumeIsAnErrorNotAnEmptyResult(t *testing.T) {
	srv, _ := dropThenResume(false)
	defer srv.Close()

	out, err := follow(t, srv, conversation.ModeJSON)

	if err == nil {
		t.Fatalf("a stream that ended without done must fail; output:\n%s", out)
	}
	if strings.Contains(out, `"type":"result"`) {
		t.Fatalf("an empty result was written:\n%s", out)
	}
	explained, ok := clierr.As(err)
	if !errors.Is(err, discussion.ErrStreamLost) || !ok || !strings.Contains(explained.Hint, "kmctl conversation watch") {
		t.Fatalf("error does not say how to reattach: %#v", err)
	}
}
