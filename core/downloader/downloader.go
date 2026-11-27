package downloader

import (
	"context"
	"sync/atomic"

	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram/downloader"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/iyear/tdl/core/dcpool"
	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/core/util/tutil"
)

// MaxPartSize refer to https://core.telegram.org/api/files#downloading-files
const MaxPartSize = 1024 * 1024

type Downloader struct {
	opts Options
}

type Options struct {
	Pool     dcpool.Pool
	Threads  int
	Iter     Iter
	Progress Progress
}

func New(opts Options) *Downloader {
	return &Downloader{
		opts: opts,
	}
}

func (d *Downloader) Download(ctx context.Context, limit int) error {
	wg, wgctx := errgroup.WithContext(ctx)
	wg.SetLimit(limit)
	var failed atomic.Int64

	for d.opts.Iter.Next(wgctx) {
		elem := d.opts.Iter.Value()

		wg.Go(func() error {
			d.opts.Progress.OnAdd(elem)
			err := tutil.ConnectionLost(ctx, d.download(wgctx, elem))
			d.opts.Progress.OnDone(elem, err)

			if err == nil {
				return nil
			}
			if errors.Is(err, context.Canceled) {
				return errors.Wrap(err, "download")
			}
			logctx.From(ctx).Error("Download error",
				zap.Any("element", elem),
				zap.Error(err),
			)
			failed.Add(1)
			return nil
		})
	}

	// worker errors only occur after caller cancellation.
	_ = wg.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := d.opts.Iter.Err(); err != nil {
		return errors.Wrap(err, "iter")
	}
	if count := failed.Load(); count > 0 {
		return errors.Errorf("%d file(s) failed to download", count)
	}
	return nil
}

func (d *Downloader) download(ctx context.Context, elem Elem) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	logctx.From(ctx).Debug("Start download elem",
		zap.Any("elem", elem))

	client := d.opts.Pool.Client(ctx, elem.File().DC())
	if elem.AsTakeout() {
		client = d.opts.Pool.Takeout(ctx, elem.File().DC())
	}

	_, err := downloader.NewDownloader().WithPartSize(MaxPartSize).
		Download(client, elem.File().Location()).
		WithThreads(tutil.BestThreads(elem.File().Size(), d.opts.Threads)).
		Parallel(ctx, newWriteAt(elem, d.opts.Progress, MaxPartSize))
	if err != nil {
		return errors.Wrap(err, "download")
	}

	return nil
}
