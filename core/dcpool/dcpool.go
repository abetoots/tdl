package dcpool

import (
	"context"
	"sync"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"go.uber.org/multierr"
	"go.uber.org/zap"

	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/core/middlewares/takeout"
)

var testMode = false

// EnableTestMode enables test mode, which disables takeout and pooling and directly returns original client.
func EnableTestMode() {
	testMode = true
}

type Pool interface {
	Client(ctx context.Context, dc int) *tg.Client
	Takeout(ctx context.Context, dc int) *tg.Client
	Default(ctx context.Context) *tg.Client
	Close() error
}

type apiClient interface {
	tg.Invoker
	Config() tg.Config
	Pool(max int64) (telegram.CloseInvoker, error)
	DC(ctx context.Context, dc int, max int64) (telegram.CloseInvoker, error)
}

type pool struct {
	api         apiClient
	size        int64
	mu          *sync.Mutex
	middlewares []telegram.Middleware

	invokers      map[int]tg.Invoker
	closes        map[int]func() error
	takeout       int64
	takeoutFailed bool
}

func NewPool(c *telegram.Client, size int64, middlewares ...telegram.Middleware) Pool {
	return newPool(c, size, middlewares...)
}

func newPool(c apiClient, size int64, middlewares ...telegram.Middleware) *pool {
	return &pool{
		api:         c,
		size:        size,
		mu:          &sync.Mutex{},
		middlewares: middlewares,
		invokers:    make(map[int]tg.Invoker),
		closes:      make(map[int]func() error),
		takeout:     0,
	}
}

func (p *pool) current() int {
	return p.api.Config().ThisDC
}

func (p *pool) Client(ctx context.Context, dc int) *tg.Client {
	p.mu.Lock()
	defer p.mu.Unlock()

	return tg.NewClient(p.invoker(ctx, dc))
}

func (p *pool) invoker(ctx context.Context, dc int) tg.Invoker {
	// self-hosted Telegram server can't properly handle pooling connections,
	// so directly return original client
	if testMode {
		return p.api
	}

	if i, ok := p.invokers[dc]; ok {
		return i
	}

	// lazy init
	var (
		invoker telegram.CloseInvoker
		err     error
	)
	if dc == p.current() { // can't transfer dc to current dc
		invoker, err = p.api.Pool(p.size)
	} else {
		invoker, err = p.api.DC(ctx, dc, p.size)
	}

	if err != nil {
		logctx.From(ctx).Error("create invoker", zap.Error(err))
		return p.api // degraded
	}

	p.closes[dc] = invoker.Close
	p.invokers[dc] = chainMiddlewares(invoker, p.middlewares...)

	return p.invokers[dc]
}

func (p *pool) Default(ctx context.Context) *tg.Client {
	return p.Client(ctx, p.current())
}

func (p *pool) Close() (err error) {
	ctx := context.TODO()
	p.mu.Lock()
	id := p.takeout
	var finishInvoker tg.Invoker
	if id != 0 {
		finishInvoker = chainMiddlewares(p.invoker(ctx, p.current()), takeout.Middleware(id))
	}
	closes := make([]func() error, 0, len(p.closes))
	for _, c := range p.closes {
		closes = append(closes, c)
	}
	p.mu.Unlock()

	if id != 0 {
		err = takeout.UnTakeout(ctx, finishInvoker)
	}
	for _, c := range closes {
		err = multierr.Append(err, c())
	}

	return err
}

func (p *pool) Takeout(ctx context.Context, dc int) *tg.Client {
	p.mu.Lock()
	defer p.mu.Unlock()

	// lazy init
	if p.takeout == 0 && !p.takeoutFailed {
		sid, err := takeout.Takeout(ctx, p.api)
		if err != nil {
			p.takeoutFailed = true
			logctx.From(ctx).Warn("takeout error", zap.Error(err))
		} else {
			p.takeout = sid
			logctx.From(ctx).Info("get takeout id", zap.Int64("id", sid))
		}
	}

	if p.takeout == 0 {
		// p.mu is held: use p.invoker, not p.Client
		return tg.NewClient(p.invoker(ctx, dc))
	}

	return tg.NewClient(chainMiddlewares(p.invoker(ctx, dc), takeout.Middleware(p.takeout)))
}
