// Package memorywrite changes a crew's long-term memory in the kubemoot_crew_memory
// NATS key-value bucket: add, edit and delete one fact, or clear a crew's facts.
//
// The stored form is the one the agent runtime writes: the key
// <namespace>.<crew>.<topic>.<key> and the JSON value {value, learnedBy, learnedAt,
// usedAt}. The operator's crewmemory package only reads, so this package owns the
// write side; its tests parse every value it writes with crewmemory.ParseFact, so
// the two cannot drift apart unnoticed.
package memorywrite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/kubemoot/kubemoot/operator/pkg/crewmemory"
	"github.com/kubemoot/kubemoot/operator/pkg/crewscope"
)

// LearnedBy is the origin kmctl stamps on a fact it writes.
const LearnedBy = "kmctl"

// ErrNotFound means the key holds no fact.
var ErrNotFound = errors.New("no such fact")

// ErrExists means the key already holds a fact.
var ErrExists = errors.New("the fact already exists")

// ErrChanged means the fact changed between reading it and writing it.
var ErrChanged = errors.New("the fact changed while it was being edited")

// Entry is one stored value and its revision in the store.
type Entry struct {
	Value    []byte
	Revision uint64
}

// Store is the write side of the crew-memory bucket.
type Store interface {
	// Get returns the entry, or ErrNotFound.
	Get(ctx context.Context, key string) (Entry, error)
	// Create stores a new key, or returns ErrExists.
	Create(ctx context.Context, key string, value []byte) error
	// Update stores a new value only if the key is still at revision, or returns ErrChanged.
	Update(ctx context.Context, key string, value []byte, revision uint64) error
	// Delete removes one key.
	Delete(ctx context.Context, key string) error
	// PurgePrefix removes every key that starts with prefix.
	PurgePrefix(ctx context.Context, prefix string) error
}

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9_=-]`)

// Sanitize makes a topic or key name safe for a key: the agent runtime keeps
// [A-Za-z0-9_=-] and replaces every other character with an underscore.
func Sanitize(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "_"
	}
	return unsafeChars.ReplaceAllString(s, "_")
}

// Fact names one fact of a crew and holds the text to store.
type Fact struct {
	Scope crewscope.Scope
	Topic string
	Key   string
	Value string
}

// StoreKey is the fact's key in the bucket, with topic and key sanitized.
func (f Fact) StoreKey() string {
	return f.Scope.MemoryPrefix() + Sanitize(f.Topic) + "." + Sanitize(f.Key)
}

// Result is what an operation did, or with DryRun would do.
type Result struct {
	Key    string
	DryRun bool
	// Count is how many facts a Clear removed or would remove.
	Count uint64
}

type stored struct {
	Value     string `json:"value"`
	LearnedBy string `json:"learnedBy"`
	LearnedAt string `json:"learnedAt"`
	UsedAt    string `json:"usedAt"`
}

func encode(value, learnedAt string, now time.Time) ([]byte, error) {
	return json.Marshal(stored{Value: value, LearnedBy: LearnedBy, LearnedAt: learnedAt, UsedAt: now.UTC().Format(time.RFC3339Nano)})
}

// firstLearned is when the stored fact was first learned, or now when its text
// carries no usable time.
func firstLearned(raw []byte, now time.Time) string {
	var s stored
	if err := json.Unmarshal(raw, &s); err != nil {
		return now.UTC().Format(time.RFC3339Nano)
	}
	if _, err := time.Parse(time.RFC3339Nano, s.LearnedAt); err != nil {
		return now.UTC().Format(time.RFC3339Nano)
	}
	return s.LearnedAt
}

// keyed names the fact a store error is about.
func keyed(key string, err error) error {
	return fmt.Errorf("%s: %w", key, err)
}

// Add stores a new fact. It fails with ErrExists when the key holds one.
func Add(ctx context.Context, st Store, f Fact, now time.Time, dryRun bool) (Result, error) {
	key := f.StoreKey()
	if _, err := st.Get(ctx, key); err == nil {
		return Result{}, keyed(key, ErrExists)
	} else if !errors.Is(err, ErrNotFound) {
		return Result{}, err
	}
	body, err := encode(f.Value, now.UTC().Format(time.RFC3339Nano), now)
	if err != nil {
		return Result{}, err
	}
	if !dryRun {
		if err := st.Create(ctx, key, body); err != nil {
			return Result{}, err
		}
	}
	return Result{Key: key, DryRun: dryRun}, nil
}

// Edit replaces the value of an existing fact and keeps the time it was first
// learned. It fails with ErrNotFound when the key holds none.
func Edit(ctx context.Context, st Store, f Fact, now time.Time, dryRun bool) (Result, error) {
	key := f.StoreKey()
	cur, err := st.Get(ctx, key)
	if err != nil {
		return Result{}, keyed(key, err)
	}
	body, err := encode(f.Value, firstLearned(cur.Value, now), now)
	if err != nil {
		return Result{}, err
	}
	if !dryRun {
		if err := st.Update(ctx, key, body, cur.Revision); err != nil {
			return Result{}, err
		}
	}
	return Result{Key: key, DryRun: dryRun}, nil
}

// Delete removes one fact. It fails with ErrNotFound when the key holds none.
func Delete(ctx context.Context, st Store, f Fact, dryRun bool) (Result, error) {
	key := f.StoreKey()
	if _, err := st.Get(ctx, key); err != nil {
		return Result{}, keyed(key, err)
	}
	if !dryRun {
		if err := st.Delete(ctx, key); err != nil {
			return Result{}, err
		}
	}
	return Result{Key: key, DryRun: dryRun}, nil
}

// Clear removes every fact of one crew. The prefix carries the namespace and the
// crew as whole name parts, so neither a crew of the same name in another namespace
// nor a crew whose name extends this one loses a fact. Count is existing, the number of
// facts the caller counted before the call; with dryRun nothing is removed.
func Clear(ctx context.Context, st Store, scope crewscope.Scope, existing int, dryRun bool) (Result, error) {
	prefix := scope.MemoryPrefix()
	res := Result{Key: prefix + "*", DryRun: dryRun, Count: uint64(max(existing, 0))}
	if dryRun {
		return res, nil
	}
	if err := st.PurgePrefix(ctx, prefix); err != nil {
		return Result{}, err
	}
	return res, nil
}

// Facts reads the crew's facts through the read-only reader, for a dry run and for
// the count a confirmation shows.
func Facts(r crewmemory.EntryReader, scope crewscope.Scope) ([]crewmemory.Fact, error) {
	res, err := crewmemory.ReadFacts(r, scope)
	if err != nil {
		return nil, err
	}
	return res.Facts, nil
}
