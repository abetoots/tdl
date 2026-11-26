package retry

import (
	"context"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"go.uber.org/zap"

	"github.com/iyear/tdl/core/logctx"
)

var internalErrors = []string{
	"Timedout", // #373
	"No workers running",
	"RPC_CALL_FAIL",
	"RPC_MCGET_FAIL",
	"WORKER_BUSY_TOO_LONG_RETRY", // #462
	"memory limit exit",          // #504
}

type retry struct {
	max    int
	errors []string
}

func (r retry) Handle(next tg.Invoker) telegram.InvokeFunc {
	return func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		var lastErr error
		delay := 4 * time.Second
		for retries := 0; retries < r.max; retries++ {
			if retries > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-timer.C:
				case <-ctx.Done():
					timer.Stop()
				}
				if err := ctx.Err(); err != nil {
					return err
				}
				delay *= 2
			}

			err := next.Invoke(ctx, input, output)
			if err == nil {
				return nil
			}
			if !tgerr.Is(err, r.errors...) {
				return errors.Wrap(err, "retry middleware skip")
			}
			logctx.From(ctx).Debug("retry middleware", zap.Int("retries", retries), zap.Error(err))
			lastErr = err
		}

		return errors.Wrapf(lastErr, "retry limit reached after %d attempts", r.max)
	}
}

// New returns middleware that retries request if it fails with one of provided errors.
func New(max int, errors ...string) telegram.Middleware {
	return retry{
		max:    max,
		errors: append(errors, internalErrors...), // #373
	}
}
