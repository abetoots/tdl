package floodlimit

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tgerr"
)

func TestFloodLimitExhaustion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()

		calls := 0
		next := telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
			calls++
			return tgerr.New(420, "FLOOD_WAIT_5")
		})
		start := time.Now()
		err := New(10).Handle(next)(ctx, nil, nil)

		if _, ok := tgerr.AsFloodWait(err); ok {
			t.Errorf("Invoke error = %v, still matches a flood wait", err)
		}
		if err == nil || !strings.Contains(err.Error(), "FLOOD_WAIT") {
			t.Errorf("Invoke error = %v, want flood wait text", err)
		}
		if calls != 11 {
			t.Errorf("Invoke made %d attempts, want 11", calls)
		}
		if elapsed := time.Since(start); elapsed != 50*time.Second {
			t.Errorf("Invoke took %v, want 50s", elapsed)
		}
	})
}

func TestFloodLimitAllowsLongWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		next := telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
			calls++
			if calls == 1 {
				return tgerr.New(420, "FLOOD_WAIT_120")
			}
			return nil
		})
		start := time.Now()

		if err := New(10).Handle(next)(t.Context(), nil, nil); err != nil {
			t.Errorf("Invoke error = %v, want nil", err)
		}
		if calls != 2 {
			t.Errorf("Invoke made %d attempts, want 2", calls)
		}
		if elapsed := time.Since(start); elapsed != 120*time.Second {
			t.Errorf("Invoke took %v, want 120s", elapsed)
		}
	})
}

func TestFloodLimitPassesThroughNonFloodError(t *testing.T) {
	want := errors.New("connection lost")
	next := telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error { return want })

	if err := New(10).Handle(next)(t.Context(), nil, nil); err != want {
		t.Errorf("Invoke error = %v, want original error %v", err, want)
	}
}
