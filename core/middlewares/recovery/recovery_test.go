package recovery

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
)

func TestRecoveryRequestDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mw := New(t.Context(), func() backoff.BackOff {
			return backoff.NewConstantBackOff(time.Second)
		})
		attempts := 0
		next := telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
			attempts++
			return errors.New("connection lost")
		})
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
		defer cancel()

		start := time.Now()
		if err := mw.Handle(next)(ctx, nil, nil); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Invoke error = %v, want %v", err, context.DeadlineExceeded)
		}
		if elapsed := time.Since(start); elapsed != 10*time.Millisecond {
			t.Errorf("Invoke took %v, want 10ms", elapsed)
		}
		if attempts != 1 {
			t.Errorf("Invoke made %d attempts, want 1", attempts)
		}
	})
}

func TestRecoveryConcurrentRetryBudgets(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mw := New(t.Context(), func() backoff.BackOff {
			return backoff.WithMaxRetries(backoff.NewConstantBackOff(time.Second), 2)
		})
		connectionErr := errors.New("connection lost")
		run := func() int {
			attempts := 0
			next := telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
				attempts++
				return connectionErr
			})
			if err := mw.Handle(next)(t.Context(), nil, nil); !errors.Is(err, connectionErr) {
				t.Errorf("Invoke error = %v, want %v", err, connectionErr)
			}
			return attempts
		}

		first := make(chan int, 1)
		go func() { first <- run() }()
		synctest.Wait()
		time.Sleep(time.Millisecond)
		second := run()
		if n := <-first; n != 3 || second != 3 {
			t.Errorf("Invoke attempts = %d and %d, want 3 each", n, second)
		}
	})
}
