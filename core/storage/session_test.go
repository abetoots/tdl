package storage

import (
	"bytes"
	"context"
	"encoding/json"
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
	const fields = `"DC":2,"AuthKey":"AQID","ServerTimeOffset":123,"UserID":9007199254740993,"FutureField":{"Enabled":true}`
	const want = `{"Version":1,"Data":{` + fields + `},"FutureEnvelopeField":[1]}`
	decode := func(t *testing.T, data []byte) any {
		t.Helper()
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	wantValue := decode(t, []byte(want))

	for _, tc := range []struct {
		name string
		data string
	}{
		{"with DCs", `{"Version":1,"Data":{` + fields + `,"DCs":{"4":{"AuthKey":"BAUG","Authorized":true}}},"FutureEnvelopeField":[1]}`},
		{"without DCs", "\n" + want},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSession(sessionTestStorage{data: []byte(tc.data)}, false)
			got, err := s.LoadSession(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			var decoded struct{ Data session.Data }
			if err := json.Unmarshal(got, &decoded); err != nil {
				t.Fatal(err)
			}
			if len(decoded.Data.DCs) != 0 {
				t.Error("gotd still sees stored non-home DC keys")
			}
			if !reflect.DeepEqual(decode(t, got), wantValue) {
				t.Error("loaded session differs from the expected JSON without DCs")
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
			data := []byte(tc.data)
			s := NewSession(sessionTestStorage{data: data}, false)
			got, err := s.LoadSession(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, data) {
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
