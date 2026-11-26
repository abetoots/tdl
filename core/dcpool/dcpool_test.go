package dcpool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

var (
	errStub        = errors.New("stub error")
	errTakeoutInit = tgerr.New(420, "TAKEOUT_INIT_DELAY_3600")
)

type fakeAPI struct {
	t         *testing.T
	invoker   fakeInvoker
	initErr   error
	initCalls atomic.Int32
}

func (a *fakeAPI) Config() tg.Config {
	return tg.Config{ThisDC: 2}
}

func (a *fakeAPI) Pool(_ int64) (telegram.CloseInvoker, error) {
	return &a.invoker, nil
}

func (a *fakeAPI) DC(_ context.Context, dc int, _ int64) (telegram.CloseInvoker, error) {
	a.t.Errorf("unexpected DC call for dc %d", dc)
	return nil, errStub
}

func (a *fakeAPI) Invoke(_ context.Context, input bin.Encoder, output bin.Decoder) error {
	if _, ok := input.(*tg.AccountInitTakeoutSessionRequest); !ok {
		return fmt.Errorf("unexpected request %T", input)
	}
	a.initCalls.Add(1)
	if a.initErr != nil {
		return a.initErr
	}
	var buf bin.Buffer
	if err := (&tg.AccountTakeout{ID: 42}).Encode(&buf); err != nil {
		return err
	}
	return output.Decode(&buf)
}

type fakeInvoker struct {
	mu         sync.Mutex
	inputs     []bin.Encoder
	closeCalls int
}

func (i *fakeInvoker) Invoke(_ context.Context, input bin.Encoder, _ bin.Decoder) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.inputs = append(i.inputs, input)
	return errStub
}

func (i *fakeInvoker) Close() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.closeCalls++
	return nil
}

func takeoutWithin(t *testing.T, p *pool, timeout time.Duration) *tg.Client {
	t.Helper()
	ctx := t.Context()
	result := make(chan *tg.Client, 1)
	go func() {
		result <- p.Takeout(ctx, 2)
	}()
	select {
	case c := <-result:
		return c
	case <-time.After(timeout):
		t.Fatal("Takeout deadlocked")
		return nil
	}
}

func TestTakeoutInitFailureFallsBack(t *testing.T) {
	api := &fakeAPI{t: t, initErr: errTakeoutInit}
	p := newPool(api, 1)
	c := takeoutWithin(t, p, 2*time.Second)
	if _, err := c.HelpGetConfig(t.Context()); !errors.Is(err, errStub) {
		t.Fatalf("HelpGetConfig error = %v, want %v", err, errStub)
	}
	if len(api.invoker.inputs) != 1 {
		t.Fatalf("recorded %d requests, want 1", len(api.invoker.inputs))
	}
	if _, ok := api.invoker.inputs[0].(*tg.HelpGetConfigRequest); !ok {
		t.Errorf("request = %T, want *tg.HelpGetConfigRequest", api.invoker.inputs[0])
	}
}

func TestTakeoutInitFailureIsNotRetried(t *testing.T) {
	api := &fakeAPI{t: t, initErr: errTakeoutInit}
	p := newPool(api, 1)
	takeoutWithin(t, p, 2*time.Second)
	takeoutWithin(t, p, 2*time.Second)
	if calls := api.initCalls.Load(); calls != 1 {
		t.Errorf("takeout init calls = %d, want 1", calls)
	}
}

func TestTakeoutConcurrentCallers(t *testing.T) {
	for _, tt := range []struct {
		name    string
		initErr error
	}{
		{name: "init fails", initErr: errTakeoutInit},
		{name: "init succeeds"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			api := &fakeAPI{t: t, initErr: tt.initErr}
			p := newPool(api, 1)
			ctx := t.Context()
			var wg sync.WaitGroup
			for range 8 {
				wg.Go(func() {
					_, _ = p.Takeout(ctx, 2).HelpGetConfig(ctx)
				})
			}
			done := make(chan struct{})
			go func() {
				wg.Wait()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("Takeout deadlocked")
			}
			if calls := api.initCalls.Load(); calls != 1 {
				t.Errorf("takeout init calls = %d, want 1", calls)
			}
			if len(api.invoker.inputs) != 8 {
				t.Fatalf("recorded %d requests, want 8", len(api.invoker.inputs))
			}
			for idx, input := range api.invoker.inputs {
				if tt.initErr != nil {
					if _, ok := input.(*tg.HelpGetConfigRequest); !ok {
						t.Errorf("request %d = %T, want *tg.HelpGetConfigRequest", idx, input)
					}
					continue
				}
				wrapped, ok := input.(*tg.InvokeWithTakeoutRequest)
				if !ok {
					t.Fatalf("request %d = %T, want *tg.InvokeWithTakeoutRequest", idx, input)
				}
				if wrapped.TakeoutID != 42 {
					t.Errorf("request %d takeout ID = %d, want 42", idx, wrapped.TakeoutID)
				}
			}
		})
	}
}

func TestCloseFinishesTakeoutOnlyWhenStarted(t *testing.T) {
	t.Run("failed init", func(t *testing.T) {
		api := &fakeAPI{t: t, initErr: errTakeoutInit}
		p := newPool(api, 1)
		takeoutWithin(t, p, 2*time.Second)
		if err := p.Close(); err != nil {
			t.Errorf("Close error = %v, want nil", err)
		}
		for _, input := range api.invoker.inputs {
			if _, ok := input.(*tg.InvokeWithTakeoutRequest); ok {
				t.Error("Close sent a takeout request after failed init")
			}
		}
		if api.invoker.closeCalls != 1 {
			t.Errorf("invoker Close calls = %d, want 1", api.invoker.closeCalls)
		}
	})

	t.Run("started", func(t *testing.T) {
		api := &fakeAPI{t: t}
		p := newPool(api, 1)
		takeoutWithin(t, p, 2*time.Second)
		if err := p.Close(); !errors.Is(err, errStub) {
			t.Errorf("Close error = %v, want %v", err, errStub)
		}
		if len(api.invoker.inputs) != 1 {
			t.Fatalf("recorded %d requests, want 1", len(api.invoker.inputs))
		}
		wrapped, ok := api.invoker.inputs[0].(*tg.InvokeWithTakeoutRequest)
		if !ok {
			t.Fatalf("request = %T, want *tg.InvokeWithTakeoutRequest", api.invoker.inputs[0])
		}
		if wrapped.TakeoutID != 42 {
			t.Errorf("takeout ID = %d, want 42", wrapped.TakeoutID)
		}
		var buf bin.Buffer
		if err := wrapped.Query.Encode(&buf); err != nil {
			t.Fatalf("encode query: %v", err)
		}
		id, err := buf.PeekID()
		if err != nil || id != tg.AccountFinishTakeoutSessionRequestTypeID {
			t.Errorf("query ID = %#x, error = %v, want %#x with no error", id, err, tg.AccountFinishTakeoutSessionRequestTypeID)
		}
		if api.invoker.closeCalls != 1 {
			t.Errorf("invoker Close calls = %d, want 1", api.invoker.closeCalls)
		}
	})
}
