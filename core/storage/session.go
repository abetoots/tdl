package storage

import (
	"context"
	"encoding/json"
	"errors"

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
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(b, &envelope); err != nil {
		return b, nil
	}
	var version int
	if err := json.Unmarshal(envelope["Version"], &version); err != nil || version != 1 {
		return b, nil
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(envelope["Data"], &data); err != nil {
		return b, nil
	}
	if _, ok := data["DCs"]; !ok {
		return b, nil
	}

	// gotd v0.166+ stores other DCs' keys as authorized; tdl's dcpool can't recover a revoked one, so drop them and transfer each run (as v0.140 did)
	delete(data, "DCs")
	envelope["Data"], err = json.Marshal(data)
	if err != nil {
		return b, nil
	}
	filtered, err := json.Marshal(envelope)
	if err != nil {
		return b, nil
	}
	return filtered, nil
}

func (s *Session) StoreSession(ctx context.Context, data []byte) error {
	return s.kv.Set(ctx, s.key(), data)
}

func (s *Session) key() string {
	return keygen.New("session")
}
