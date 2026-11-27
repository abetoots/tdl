package tclient

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestNormalizeRunError(t *testing.T) {
	lost := fmt.Errorf("engine forcibly closed: %w", context.Canceled)
	cleanup := errors.New("cleanup failed")
	ordinary := errors.New("command failed")

	for _, tt := range []struct {
		name           string
		err            error
		callerCanceled bool
		wantLost       bool
	}{
		{name: "live caller", err: lost, wantLost: true},
		{name: "live caller with appended cleanup error", err: errors.Join(lost, cleanup), wantLost: true},
		{name: "canceled caller", err: lost, callerCanceled: true},
		{name: "ordinary error", err: ordinary},
		{name: "success"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.callerCanceled {
				cancel()
			}

			got := normalizeRunError(ctx, tt.err)
			if tt.wantLost {
				if got == nil || errors.Is(got, context.Canceled) || !strings.HasPrefix(got.Error(), "connection lost: ") ||
					!strings.Contains(got.Error(), tt.err.Error()) {
					t.Errorf("run error = %v, want opaque connection loss with %v", got, tt.err)
				}
			} else if got != tt.err {
				t.Errorf("run error = %v, want original %v", got, tt.err)
			}
		})
	}
}
