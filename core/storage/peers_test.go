package storage

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/gotd/td/telegram/peers"
)

type peerTestStorage struct {
	Storage
	values map[string][]byte
}

func (s peerTestStorage) Set(_ context.Context, key string, value []byte) error {
	s.values[key] = value
	return nil
}

func TestPeerOptionsDisablesCache(t *testing.T) {
	opts := PeerOptions(nil)
	if _, ok := opts.Cache.(peers.NoopCache); !ok {
		t.Errorf("cache = %T, want peers.NoopCache", opts.Cache)
	}
}

func TestPeerOptionsUsesKV(t *testing.T) {
	kv := peerTestStorage{values: make(map[string][]byte)}
	opts := PeerOptions(kv)
	key := peers.Key{Prefix: "channel_", ID: 4}
	want := peers.Value{AccessHash: 42}
	if err := opts.Storage.Save(t.Context(), key, want); err != nil {
		t.Fatal(err)
	}
	var got peers.Value
	if err := json.Unmarshal(kv.values["peers:key:channel_:4"], &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("stored peer = %+v, want %+v", got, want)
	}
}
