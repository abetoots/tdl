package floodlimit

import (
	"context"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

func New(maxRetries uint) telegram.Middleware {
	return telegram.MiddlewareFunc(func(next tg.Invoker) telegram.InvokeFunc {
		return func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
			// takeout wraps the input and leaves the file-chunk decoder intact.
			_, fileChunk := output.(*tg.UploadFileBox)
			var waits uint
			var timer *time.Timer
			for {
				err := next.Invoke(ctx, input, output)
				delay, ok := tgerr.AsFloodWait(err)
				if !ok {
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
				if err := ctx.Err(); err != nil {
					return err
				}
			}
		}
	})
}
