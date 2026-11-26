package retry

import (
	"context"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tgerr"
)

const rpcCallFail = "RPC_CALL_FAIL"

func TestRetryExhaustion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		var calls []time.Duration
		next := telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
			calls = append(calls, time.Since(start))
			return tgerr.New(500, rpcCallFail)
		})

		err := New(5).Handle(next)(t.Context(), nil, nil)
		if !tgerr.Is(err, rpcCallFail) {
			t.Errorf("Invoke error = %v, want %s", err, rpcCallFail)
		}
		if err == nil || !strings.Contains(err.Error(), "retry limit reached after 5 attempts") {
			t.Errorf("Invoke error = %v, want retry limit message", err)
		}
		wantCalls := []time.Duration{0, 4 * time.Second, 12 * time.Second, 28 * time.Second, 60 * time.Second}
		if !slices.Equal(calls, wantCalls) {
			t.Errorf("Invoke attempts at %v, want %v", calls, wantCalls)
		}
		if elapsed := time.Since(start); elapsed != 60*time.Second {
			t.Errorf("Invoke took %v, want 60s", elapsed)
		}
	})
}

func TestRetryCancelsWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		calls := 0
		next := telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
			calls++
			return tgerr.New(500, rpcCallFail)
		})
		start := time.Now()
		result := make(chan error, 1)
		go func() { result <- New(5).Handle(next)(ctx, nil, nil) }()
		synctest.Wait()
		time.Sleep(time.Second)
		cancel()

		if err := <-result; err != ctx.Err() {
			t.Errorf("Invoke error = %v, want %v", err, ctx.Err())
		}
		if calls != 1 {
			t.Errorf("Invoke made %d attempts, want 1", calls)
		}
		if elapsed := time.Since(start); elapsed != time.Second {
			t.Errorf("Invoke took %v, want 1s", elapsed)
		}
	})
}

func TestRetrySucceedsOnThirdAttempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		next := telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
			calls++
			if calls < 3 {
				return tgerr.New(500, rpcCallFail)
			}
			return nil
		})

		if err := New(5).Handle(next)(t.Context(), nil, nil); err != nil {
			t.Errorf("Invoke error = %v, want nil", err)
		}
		if calls != 3 {
			t.Errorf("Invoke made %d attempts, want 3", calls)
		}
	})
}
