// package floodlimit guards file-chunk reads against gotd's retry loops.
package floodlimit

import (
	"context"
	"net"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/iyear/tdl/core/middlewares/retry"
	"github.com/iyear/tdl/core/util/tutil"
)

func New(maxRetries uint) telegram.Middleware {
	return telegram.MiddlewareFunc(func(next tg.Invoker) telegram.InvokeFunc {
		return func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			fileChunk := tutil.IsFileChunk(output)
			var waits uint
			var timer *time.Timer
			for {
				err := next.Invoke(ctx, input, output)
				if err == nil {
					return nil
				}
				if ctxErr := ctx.Err(); ctxErr != nil {
					return retry.WaitContextError(ctxErr, err)
				}
				delay, ok := tgerr.AsFloodWait(err)
				if !ok {
					if fileChunk && isRetryableTimeout(err) {
						return errors.Errorf("file chunk failed: %v", err)
					}
					return err
				}

				if fileChunk && maxRetries != 0 && tgerr.Is(err, tgerr.ErrFloodWait) {
					if waits == maxRetries {
						// use %v so the downloader cannot unwrap the flood wait and retry it.
						return errors.Errorf("telegram kept asking to wait, gave up after %d flood waits: %v", maxRetries, err)
					}
					waits++
				}

				if delay == 0 {
					delay = time.Second
				}
				if timer == nil {
					timer = time.NewTimer(delay)
				} else {
					timer.Reset(delay)
				}
				select {
				case <-timer.C:
				case <-ctx.Done():
					timer.Stop()
				}
				if ctxErr := ctx.Err(); ctxErr != nil {
					return retry.WaitContextError(ctxErr, err)
				}
			}
		}
	})
}

func isRetryableTimeout(err error) bool {
	if tgerr.Is(err, tg.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
