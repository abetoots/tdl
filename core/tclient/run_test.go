package tclient

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/gotd/td/telegram/auth"
)

type authStatusFunc func(context.Context) (*auth.Status, error)

func (f authStatusFunc) Status(ctx context.Context) (*auth.Status, error) {
	return f(ctx)
}

func authorizedStatus(context.Context) (*auth.Status, error) {
	return &auth.Status{Authorized: true}, nil
}

func runLikeGotd(caller context.Context, cb func(context.Context) error) error {
	child, cancel := context.WithCancel(caller)
	// simulate gotd's callback context ending independently of the caller.
	cancel()
	if err := cb(child); !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func TestRunWithAuthCommandResult(t *testing.T) {
	lost := fmt.Errorf("engine forcibly closed: %w", context.Canceled)
	ordinary := errors.New("command failed")
	cleanup := fmt.Errorf("cleanup: %w", context.Canceled)

	for _, tt := range []struct {
		name           string
		commandErr     error
		callerCanceled bool
		dropResult     bool
		cleanup        error
		want           error
		wantLostText   string
	}{
		{name: "live caller and canceled child", commandErr: lost, wantLostText: lost.Error()},
		{name: "canceled caller exits quietly", commandErr: lost, callerCanceled: true},
		{name: "runner drops a failed command", commandErr: ordinary, dropResult: true, want: ordinary},
		{name: "command completes successfully"},
		{name: "runner appends cleanup error", commandErr: ordinary, cleanup: cleanup, wantLostText: errors.Join(ordinary, cleanup).Error()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			run := func(ctx context.Context, cb func(context.Context) error) error {
				err := runLikeGotd(ctx, cb)
				if tt.dropResult {
					return nil
				}
				if tt.cleanup != nil {
					return errors.Join(err, tt.cleanup)
				}
				return err
			}
			calls := 0
			got := runWithAuth(ctx, run, authStatusFunc(authorizedStatus), func(context.Context) error {
				calls++
				if tt.callerCanceled {
					cancel()
				}
				return tt.commandErr
			})

			if calls != 1 {
				t.Errorf("command ran %d times, want 1", calls)
			}
			if tt.wantLostText != "" {
				if got == nil || errors.Is(got, context.Canceled) || got.Error() != "connection lost: "+tt.wantLostText {
					t.Errorf("run error = %v, want opaque connection lost with %v", got, tt.wantLostText)
				}
			} else if !errors.Is(got, tt.want) {
				t.Errorf("run error = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRunWithAuthRunnerStopsBeforeCommandFinishes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		finished := make(chan struct{})
		run := func(ctx context.Context, cb func(context.Context) error) error {
			go func() {
				defer close(finished)
				_ = cb(ctx)
			}()
			<-started
			return nil
		}
		got := runWithAuth(t.Context(), run, authStatusFunc(authorizedStatus), func(context.Context) error {
			close(started)
			<-release
			return nil
		})
		close(release)
		<-finished

		const want = "connection lost: client stopped before the command finished"
		if got == nil || errors.Is(got, context.Canceled) || !strings.Contains(got.Error(), want) {
			t.Errorf("run error = %v, want %q", got, want)
		}
	})
}
