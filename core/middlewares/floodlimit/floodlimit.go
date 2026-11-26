package floodlimit

import (
	"context"

	"github.com/go-faster/errors"
	"github.com/gotd/contrib/middleware/floodwait"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

func New(maxRetries uint) telegram.Middleware {
	waiter := floodwait.NewSimpleWaiter().WithMaxRetries(maxRetries)
	return telegram.MiddlewareFunc(func(next tg.Invoker) telegram.InvokeFunc {
		invoke := waiter.Handle(next)
		return func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
			err := invoke(ctx, input, output)
			if _, ok := tgerr.AsFloodWait(err); ok {
				// use %v so the downloader cannot unwrap the flood wait and retry it.
				return errors.Errorf("telegram kept asking to wait, gave up after %d flood waits: %v", maxRetries, err)
			}
			return err
		}
	})
}
