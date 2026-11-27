package tutil

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestConnectionLost(t *testing.T) {
	lost := fmt.Errorf("engine stopped: %w", context.Canceled)
	plain := errors.New("ordinary failure")
	for _, tt := range []struct {
		name     string
		err      error
		canceled bool
		wantLost bool
	}{
		{name: "success"},
		{name: "plain error", err: plain},
		{name: "lost connection", err: lost, wantLost: true},
		{name: "caller canceled", err: lost, canceled: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.canceled {
				cancel()
			}
			got := ConnectionLost(ctx, tt.err)
			if tt.wantLost {
				if got == nil || errors.Is(got, context.Canceled) || got.Error() != "connection lost: "+lost.Error() {
					t.Errorf("ConnectionLost = %v, want opaque connection loss with %v", got, lost)
				}
			} else if got != tt.err {
				t.Errorf("ConnectionLost = %v, want input %v", got, tt.err)
			}
		})
	}
}
