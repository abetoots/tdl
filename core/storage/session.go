package storage

import (
	"context"
	"errors"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"

	"github.com/iyear/tdl/core/storage/keygen"
)

type Session struct {
	kv    Storage
	login bool
}

func NewSession(kv Storage, login bool) telegram.SessionStorage {
	return &Session{kv: kv, login: login}
}

func (s *Session) LoadSession(ctx context.Context) ([]byte, error) {
	if s.login {
		return nil, nil
	}

	b, err := s.kv.Get(ctx, s.key())
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	mem := &session.StorageMemory{}
	if err := mem.StoreSession(ctx, b); err != nil {
		return b, nil
	}
	loader := session.Loader{Storage: mem}
	data, err := loader.Load(ctx)
	if err != nil {
		return b, nil
	}
	// gotd v0.166+ restores stored non-home dc keys as authorized; dcpool can't recover a revoked one, so drop them on load (v0.140 behaviour)
	data.DCs = nil
	if err := loader.Save(ctx, data); err != nil {
		return b, nil
	}
	return mem.LoadSession(ctx)
}

func (s *Session) StoreSession(ctx context.Context, data []byte) error {
	return s.kv.Set(ctx, s.key(), data)
}

func (s *Session) key() string {
	return keygen.New("session")
}
