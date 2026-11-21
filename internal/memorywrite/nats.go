package memorywrite

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kubemoot/kubemoot/operator/pkg/crewmemory"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Timeout bounds each call to the server.
const Timeout = 30 * time.Second

// OpenNATS binds the crew-memory bucket on an existing connection for writing. It
// never creates the bucket: the Kubemoot operator does.
func OpenNATS(ctx context.Context, nc *nats.Conn) (Store, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	kv, err := js.KeyValue(ctx, crewmemory.Bucket)
	if err != nil {
		return nil, fmt.Errorf("open bucket %s: %w", crewmemory.Bucket, err)
	}
	return &natsStore{kv: kv, js: js}, nil
}

type natsStore struct {
	kv jetstream.KeyValue
	js jetstream.JetStream
}

func (s *natsStore) Get(ctx context.Context, key string) (Entry, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	e, err := s.kv.Get(ctx, key)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return Entry{}, ErrNotFound
	}
	if err != nil {
		return Entry{}, err
	}
	return Entry{Value: e.Value(), Revision: e.Revision()}, nil
}

func (s *natsStore) Create(ctx context.Context, key string, value []byte) error {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	_, err := s.kv.Create(ctx, key, value)
	if errors.Is(err, jetstream.ErrKeyExists) {
		return ErrExists
	}
	return err
}

func (s *natsStore) Update(ctx context.Context, key string, value []byte, revision uint64) error {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	_, err := s.kv.Update(ctx, key, value, revision)
	if err != nil && errors.Is(err, jetstream.ErrKeyExists) {
		return ErrChanged
	}
	return err
}

func (s *natsStore) Delete(ctx context.Context, key string) error {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	return s.kv.Delete(ctx, key)
}

// PurgePrefix purges the key-value stream by subject filter. Listing the keys and
// deleting them one by one can miss keys, because a key listing is not a complete
// read; the stream purge is atomic and complete.
func (s *natsStore) PurgePrefix(ctx context.Context, prefix string) error {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	stream, err := s.js.Stream(ctx, "KV_"+crewmemory.Bucket)
	if err != nil {
		return err
	}
	return stream.Purge(ctx, jetstream.WithPurgeSubject(subject(prefix)+">"))
}

// subject is the subject a key prefix is stored under in the key-value stream.
func subject(prefix string) string {
	return "$KV." + crewmemory.Bucket + "." + prefix
}
