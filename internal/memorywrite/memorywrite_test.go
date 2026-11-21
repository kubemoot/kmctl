package memorywrite

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kubemoot/kubemoot/operator/pkg/crewmemory"
	"github.com/kubemoot/kubemoot/operator/pkg/crewscope"
)

// fakeStore is an in-memory Store that records what was written.
type fakeStore struct {
	data    map[string]Entry
	rev     uint64
	writes  int
	purged  []string
	updateE error
}

func newFake() *fakeStore { return &fakeStore{data: map[string]Entry{}} }

func (s *fakeStore) Get(_ context.Context, key string) (Entry, error) {
	e, ok := s.data[key]
	if !ok {
		return Entry{}, ErrNotFound
	}
	return e, nil
}

func (s *fakeStore) put(key string, v []byte) {
	s.rev++
	s.writes++
	s.data[key] = Entry{Value: v, Revision: s.rev}
}

func (s *fakeStore) Create(_ context.Context, key string, v []byte) error {
	if _, ok := s.data[key]; ok {
		return ErrExists
	}
	s.put(key, v)
	return nil
}

func (s *fakeStore) Update(_ context.Context, key string, v []byte, revision uint64) error {
	if s.updateE != nil {
		return s.updateE
	}
	if s.data[key].Revision != revision {
		return ErrChanged
	}
	s.put(key, v)
	return nil
}

func (s *fakeStore) Delete(_ context.Context, key string) error {
	s.writes++
	delete(s.data, key)
	return nil
}

func (s *fakeStore) PurgePrefix(_ context.Context, prefix string) error {
	s.writes++
	s.purged = append(s.purged, prefix)
	for k := range s.data {
		if strings.HasPrefix(k, prefix) {
			delete(s.data, k)
		}
	}
	return nil
}

var (
	t0    = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	t1    = t0.Add(time.Hour)
	scope = mustScope("crew-demo", "demo")
)

func mustScope(ns, crew string) crewscope.Scope {
	s, err := crewscope.New(ns, crew)
	if err != nil {
		panic(err)
	}
	return s
}

func gpuFact(value string) Fact {
	return Fact{Scope: scope, Topic: "hardware", Key: "gpu0", Value: value}
}

func TestSanitize(t *testing.T) {
	for in, want := range map[string]string{
		"hardware":   "hardware",
		"my topic":   "my_topic",
		"a.b/c":      "a_b_c",
		"  padded  ": "padded",
		"":           "_",
		"   ":        "_",
		"keep_=-ok":  "keep_=-ok",
		"emojié":     "emoji_",
	} {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStoreKeySanitizesTopicAndKey(t *testing.T) {
	f := Fact{Scope: scope, Topic: "my topic", Key: "a.b"}
	if got := f.StoreKey(); got != "crew-demo.demo.my_topic.a_b" {
		t.Errorf("StoreKey = %q", got)
	}
}

// parsed reads a stored value back with the operator's own parser.
func parsed(t *testing.T, st *fakeStore, key string) crewmemory.Fact {
	t.Helper()
	e := st.data[key]
	f, err := crewmemory.ParseFact(scope, key, crewmemory.Entry{Value: e.Value, Revision: e.Revision})
	if err != nil {
		t.Fatalf("operator parser rejects the stored value of %s: %v", key, err)
	}
	return f
}

func TestAddStoresAFactTheOperatorParserReads(t *testing.T) {
	st := newFake()
	res, err := Add(context.Background(), st, gpuFact("RTX 5090"), t0, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Key != "crew-demo.demo.hardware.gpu0" || res.DryRun {
		t.Errorf("result = %+v", res)
	}
	f := parsed(t, st, res.Key)
	if f.Value != "RTX 5090" || f.LearnedBy != LearnedBy || f.Topic != "hardware" || f.Key != "gpu0" {
		t.Errorf("fact = %+v", f)
	}
	if !f.LearnedAt.Equal(t0) || !f.UsedAt.Equal(t0) {
		t.Errorf("times = %v %v, want %v", f.LearnedAt, f.UsedAt, t0)
	}
}

func TestAddRefusesAnExistingFact(t *testing.T) {
	st := newFake()
	if _, err := Add(context.Background(), st, gpuFact("one"), t0, false); err != nil {
		t.Fatal(err)
	}
	_, err := Add(context.Background(), st, gpuFact("two"), t1, false)
	if !errors.Is(err, ErrExists) {
		t.Fatalf("err = %v, want ErrExists", err)
	}
	if parsed(t, st, "crew-demo.demo.hardware.gpu0").Value != "one" {
		t.Error("the existing fact was changed")
	}
}

func TestEditKeepsTheFirstLearnedTime(t *testing.T) {
	st := newFake()
	ctx := context.Background()
	if _, err := Add(ctx, st, gpuFact("one"), t0, false); err != nil {
		t.Fatal(err)
	}
	if _, err := Edit(ctx, st, gpuFact("two"), t1, false); err != nil {
		t.Fatal(err)
	}
	f := parsed(t, st, "crew-demo.demo.hardware.gpu0")
	if f.Value != "two" || !f.LearnedAt.Equal(t0) || !f.UsedAt.Equal(t1) {
		t.Errorf("fact = %+v", f)
	}
}

func TestEditOfAMissingFactFails(t *testing.T) {
	_, err := Edit(context.Background(), newFake(), gpuFact("x"), t0, false)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestEditReportsAChangedFact(t *testing.T) {
	st := newFake()
	ctx := context.Background()
	if _, err := Add(ctx, st, gpuFact("one"), t0, false); err != nil {
		t.Fatal(err)
	}
	st.updateE = ErrChanged
	if _, err := Edit(ctx, st, gpuFact("two"), t1, false); !errors.Is(err, ErrChanged) {
		t.Fatalf("err = %v, want ErrChanged", err)
	}
}

func TestEditOfAnUnreadableValueStampsNow(t *testing.T) {
	st := newFake()
	st.put("crew-demo.demo.hardware.gpu0", []byte("not json"))
	if _, err := Edit(context.Background(), st, gpuFact("two"), t1, false); err != nil {
		t.Fatal(err)
	}
	if f := parsed(t, st, "crew-demo.demo.hardware.gpu0"); !f.LearnedAt.Equal(t1) {
		t.Errorf("learnedAt = %v, want %v", f.LearnedAt, t1)
	}
}

func TestDeleteRemovesOneFactAndFailsOnAMissingOne(t *testing.T) {
	st := newFake()
	ctx := context.Background()
	if _, err := Add(ctx, st, gpuFact("one"), t0, false); err != nil {
		t.Fatal(err)
	}
	if _, err := Delete(ctx, st, gpuFact(""), false); err != nil {
		t.Fatal(err)
	}
	if len(st.data) != 0 {
		t.Errorf("data = %v", st.data)
	}
	if _, err := Delete(ctx, st, gpuFact(""), false); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	st := newFake()
	ctx := context.Background()
	if res, err := Add(ctx, st, gpuFact("one"), t0, true); err != nil || !res.DryRun {
		t.Fatalf("add dry run = %+v, %v", res, err)
	}
	if _, err := Add(ctx, st, gpuFact("one"), t0, false); err != nil {
		t.Fatal(err)
	}
	writes := st.writes
	dryRunOthers(t, st)
	if st.writes != writes || len(st.purged) != 0 {
		t.Errorf("a dry run wrote: writes %d -> %d, purged %v", writes, st.writes, st.purged)
	}
	if parsed(t, st, "crew-demo.demo.hardware.gpu0").Value != "one" {
		t.Error("a dry run changed the fact")
	}
}

// dryRunOthers runs edit, delete and clear as dry runs.
func dryRunOthers(t *testing.T, st *fakeStore) {
	t.Helper()
	ctx := context.Background()
	if _, err := Edit(ctx, st, gpuFact("two"), t1, true); err != nil {
		t.Fatal(err)
	}
	if _, err := Delete(ctx, st, gpuFact(""), true); err != nil {
		t.Fatal(err)
	}
	res, err := Clear(ctx, st, scope, 3, true)
	if err != nil || res.Count != 3 || !res.DryRun {
		t.Fatalf("clear dry run = %+v, %v", res, err)
	}
}

func TestClearPurgesTheCrewPrefixOnly(t *testing.T) {
	st := newFake()
	ctx := context.Background()
	for _, k := range []string{"crew-demo.demo.a.b", "crew-demo.demo-prose.a.b", "other-ns.demo.a.b"} {
		st.put(k, []byte("{}"))
	}
	res, err := Clear(ctx, st, scope, 1, false)
	if err != nil || res.Count != 1 {
		t.Fatalf("clear = %+v, %v", res, err)
	}
	if len(st.purged) != 1 || st.purged[0] != "crew-demo.demo." {
		t.Errorf("purged = %v", st.purged)
	}
	if len(st.data) != 2 {
		t.Errorf("another crew lost facts: %v", st.data)
	}
}

type failingStore struct{ *fakeStore }

func (failingStore) Get(context.Context, string) (Entry, error) {
	return Entry{}, errors.New("server went away")
}
func (failingStore) PurgePrefix(context.Context, string) error { return errors.New("purge refused") }

func TestStoreErrorsArePassedOn(t *testing.T) {
	ctx := context.Background()
	st := failingStore{newFake()}
	if _, err := Add(ctx, st, gpuFact("x"), t0, false); err == nil || errors.Is(err, ErrExists) {
		t.Errorf("add err = %v", err)
	}
	if _, err := Edit(ctx, st, gpuFact("x"), t0, false); err == nil {
		t.Error("edit: want error")
	}
	if _, err := Delete(ctx, st, gpuFact("x"), false); err == nil {
		t.Error("delete: want error")
	}
	if _, err := Clear(ctx, st, scope, 1, false); err == nil {
		t.Error("clear: want error")
	}
}
