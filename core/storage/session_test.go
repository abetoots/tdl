package storage

import (
	"context"
	"reflect"
	"testing"

	"github.com/gotd/td/session"
)

type sessionTestStorage struct {
	Storage
	data []byte
}

func (s sessionTestStorage) Get(context.Context, string) ([]byte, error) {
	return s.data, nil
}

func TestLoadSession(t *testing.T) {
	const fields = `"DC":2,"AuthKey":"AQID","ServerTimeOffset":123,"UserID":9007199254740993`
	const dcKeys = `{"4":{"AuthKey":"BAUG","Authorized":true}}`
	const envelope = `{"Version":1,"Data":{`
	const prefix = envelope + fields
	const withDCs = fields + `,"DCs":` + dcKeys + `}}`
	want := session.Data{
		DC:               2,
		AuthKey:          []byte{1, 2, 3},
		ServerTimeOffset: 123,
		UserID:           9007199254740993,
	}

	for _, tc := range []struct {
		name string
		data string
	}{
		{"with DCs", envelope + withDCs},
		{"with Dcs", prefix + `,"Dcs":` + dcKeys + `}}`},
		{"with dcs", prefix + `,"dcs":` + dcKeys + `}}`},
		{"with DCs and DCS", prefix + `,"DCs":` + dcKeys + `,"DCS":` + dcKeys + `}}`},
		{"without DCs", "\n" + prefix + `}}`},
		{"lowercase envelope", `{"version":1,"data":{` + withDCs},
		{"duplicate Data and data", `{"Version":1,"Data":{},"data":{` + withDCs},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			s := NewSession(sessionTestStorage{data: []byte(tc.data)}, false)
			got, err := s.LoadSession(ctx)
			if err != nil {
				t.Fatal(err)
			}

			mem := &session.StorageMemory{}
			if err := mem.StoreSession(ctx, got); err != nil {
				t.Fatal(err)
			}
			decoded, err := (&session.Loader{Storage: mem}).Load(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(decoded.DCs) != 0 {
				t.Fatalf("gotd still sees %d stored non-home dc keys", len(decoded.DCs))
			}
			if !reflect.DeepEqual(*decoded, want) {
				t.Errorf("loaded session = %+v, want %+v", *decoded, want)
			}
		})
	}
}

func TestLoadSessionUnrecognizedEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
	}{
		{"empty", ""},
		{"invalid JSON", `{"Version":1,"Data":{"DCs":{}}`},
		{"other JSON", ` {"Data":{"DCs":{}}} `},
		{"unsupported version", ` {"Version":2,"Data":{"DCs":{}}} `},
		{"non-object Data", ` {"Version":1,"Data":[{"DCs":{}}]} `},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSession(sessionTestStorage{data: []byte(tc.data)}, false)
			got, err := s.LoadSession(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.data {
				t.Error("unrecognized session bytes changed")
			}
		})
	}
}

func TestLoadSessionLogin(t *testing.T) {
	s := NewSession(nil, true)
	got, err := s.LoadSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("login loaded a session")
	}
}
