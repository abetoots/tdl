package retry

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
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

		err := New(5).Handle(next)(t.Context(), &tg.UploadGetFileRequest{}, &tg.UploadFileBox{})
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

		if err := <-result; !errors.Is(err, ctx.Err()) || !strings.Contains(err.Error(), rpcCallFail) {
			t.Errorf("Invoke error = %v, want %v with %s", err, ctx.Err(), rpcCallFail)
		}
		if calls != 1 {
			t.Errorf("Invoke made %d attempts, want 1", calls)
		}
		if elapsed := time.Since(start); elapsed != time.Second {
			t.Errorf("Invoke took %v, want 1s", elapsed)
		}
	})
}

func TestRetryPreservesLastFailureAtDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		calls := 0
		next := telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
			calls++
			if calls == 1 {
				return tgerr.New(500, rpcCallFail)
			}
			return tgerr.New(500, tg.ErrTimeout)
		})
		start := time.Now()
		err := New(5).Handle(next)(ctx, &tg.UploadGetFileRequest{}, &tg.UploadFileBox{})
		if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), tg.ErrTimeout) ||
			strings.Contains(err.Error(), rpcCallFail) {
			t.Errorf("Invoke error = %v, want deadline with the last Telegram error %s", err, tg.ErrTimeout)
		}
		if _, ok := tgerr.As(err); ok {
			t.Errorf("Invoke error = %v, still a Telegram error", err)
		}
		if _, ok := tgerr.AsFloodWait(err); ok {
			t.Errorf("Invoke error = %v, still a flood wait", err)
		}
		var netErr net.Error
		if errors.As(err, &netErr) {
			t.Errorf("Invoke error = %v, still a network error", err)
		}
		if elapsed := time.Since(start); calls != 2 || elapsed != 5*time.Second {
			t.Errorf("Invoke made %d calls in %v, want 2 in 5s", calls, elapsed)
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

func TestRetryWithZeroAttemptsStillInvokesNext(t *testing.T) {
	calls := 0
	next := telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
		calls++
		return nil
	})
	if err := New(0).Handle(next)(t.Context(), &tg.UploadGetFileRequest{}, &tg.UploadFileBox{}); err != nil {
		t.Errorf("Invoke error = %v, want nil", err)
	}
	if calls != 1 {
		t.Errorf("Invoke made %d attempts, want 1", calls)
	}
}
