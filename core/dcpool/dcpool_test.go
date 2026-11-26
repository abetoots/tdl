package dcpool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

var (
	errStub        = errors.New("stub error")
	errClosed      = errors.New("invoker closed")
	errTakeoutInit = tgerr.New(420, "TAKEOUT_INIT_DELAY_3600")
)

type fakeAPI struct {
	invoker   fakeInvoker
	dcs       map[int]*fakeInvoker
	initErr   error
	initCalls atomic.Int32
	entered   chan struct{}
	gate      <-chan struct{}
	blockDC   bool
}

func (a *fakeAPI) Config() tg.Config {
	return tg.Config{ThisDC: 2}
}

func (a *fakeAPI) Pool(_ int64) (telegram.CloseInvoker, error) {
	return &a.invoker, nil
}

func (a *fakeAPI) DC(ctx context.Context, dc int, _ int64) (telegram.CloseInvoker, error) {
	if a.blockDC {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if a.dcs == nil {
		a.dcs = make(map[int]*fakeInvoker)
	}
	if a.dcs[dc] == nil {
		a.dcs[dc] = &fakeInvoker{}
	}
	return a.dcs[dc], nil
}

func (a *fakeAPI) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	if _, ok := input.(*tg.AccountInitTakeoutSessionRequest); !ok {
		return fmt.Errorf("unexpected request %T", input)
	}
	if a.initCalls.Add(1) == 1 && a.entered != nil {
		close(a.entered)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.gate != nil {
		select {
		case <-a.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
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
	deadline   time.Time
}

func (i *fakeInvoker) Invoke(ctx context.Context, input bin.Encoder, _ bin.Decoder) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.inputs = append(i.inputs, input)
	if i.closeCalls != 0 {
		return errClosed
	}
	i.deadline, _ = ctx.Deadline()
	return errStub
}

func (i *fakeInvoker) Close() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.closeCalls++
	return nil
}

func takeoutWithin(t *testing.T, p *pool, dc int, timeout time.Duration) *tg.Client {
	t.Helper()
	ctx := t.Context()
	result := make(chan *tg.Client, 1)
	go func() {
		result <- p.Takeout(ctx, dc)
	}()
	select {
	case c := <-result:
		return c
	case <-time.After(timeout):
		t.Fatal("Takeout deadlocked")
		return nil
	}
}

func closeWithin(t *testing.T, p *pool, timeout time.Duration) error {
	t.Helper()
	result := make(chan error, 1)
	go func() { result <- p.Close() }()
	select {
	case err := <-result:
		return err
	case <-time.After(timeout):
		t.Fatal("Close deadlocked")
		return nil
	}
}

func TestTakeoutInitErrors(t *testing.T) {
	for _, test := range []struct {
		name         string
		initErr      error
		cancelCaller bool
		wantCalls    int32
	}{
		{name: "network", initErr: errStub, wantCalls: 1},
		{name: "connection canceled", initErr: context.Canceled, wantCalls: 1},
		{name: "timed out", initErr: context.DeadlineExceeded, wantCalls: 1},
		{name: "wrapped RPC", initErr: fmt.Errorf("rpcDoRequest: %w", errTakeoutInit), wantCalls: 1},
		{name: "caller canceled", cancelCaller: true, wantCalls: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			api := &fakeAPI{initErr: test.initErr}
			p := newPool(api, 1)
			ctx := t.Context()
			initCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			if test.cancelCaller {
				cancel()
			}
			first := p.Takeout(initCtx, 2)
			if _, err := first.HelpGetConfig(ctx); !errors.Is(err, errStub) {
				t.Fatalf("first HelpGetConfig error = %v, want %v", err, errStub)
			}
			if wantLatched := !test.cancelCaller; p.takeoutFailed != wantLatched || p.takeout != 0 {
				t.Errorf("takeout after first failure: latched = %t, id = %d, want %t and 0", p.takeoutFailed, p.takeout, wantLatched)
			}
			api.initErr = nil
			second := takeoutWithin(t, p, 2, 2*time.Second)
			if _, err := second.HelpGetConfig(ctx); !errors.Is(err, errStub) {
				t.Fatalf("second HelpGetConfig error = %v, want %v", err, errStub)
			}
			if calls := api.initCalls.Load(); calls != test.wantCalls {
				t.Errorf("takeout init calls = %d, want %d", calls, test.wantCalls)
			}
			if len(api.invoker.inputs) != 2 {
				t.Fatalf("recorded %d requests on DC 2, want 2", len(api.invoker.inputs))
			}
			if input := api.invoker.inputs[0]; !isPlainConfigRequest(input) {
				t.Errorf("first request = %T, want *tg.HelpGetConfigRequest", input)
			}
			secondInput := api.invoker.inputs[1]
			if test.wantCalls == 1 {
				if !isPlainConfigRequest(secondInput) {
					t.Errorf("second request = %T, want *tg.HelpGetConfigRequest", secondInput)
				}
			} else if req, ok := secondInput.(*tg.InvokeWithTakeoutRequest); !ok || req.TakeoutID != 42 {
				t.Errorf("second request = %v, want takeout request with ID 42", secondInput)
			}
		})
	}
}

func isPlainConfigRequest(input bin.Encoder) bool {
	_, ok := input.(*tg.HelpGetConfigRequest)
	return ok
}

func TestTakeoutInitTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := &fakeAPI{gate: make(chan struct{})}
		p := newPool(api, 1)
		ctx, cancel := context.WithTimeout(t.Context(), 31*time.Second)
		defer cancel()

		start := time.Now()
		client := p.Takeout(ctx, 2)
		if elapsed := time.Since(start); elapsed != 30*time.Second {
			t.Errorf("takeout init took %v, want 30s", elapsed)
		}
		if err := ctx.Err(); err != nil {
			t.Errorf("parent context error = %v, want nil", err)
		}
		if _, err := client.HelpGetConfig(t.Context()); !errors.Is(err, errStub) {
			t.Fatalf("HelpGetConfig error = %v, want %v", err, errStub)
		}
		if len(api.invoker.inputs) != 1 || !isPlainConfigRequest(api.invoker.inputs[0]) {
			t.Errorf("requests after timeout = %v, want one plain config request", api.invoker.inputs)
		}
		if !p.takeoutFailed || p.takeout != 0 {
			t.Error("timeout did not latch takeout off")
		}
	})
}

func TestClientBoundsDCCreation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := &fakeAPI{blockDC: true}
		p := newPool(api, 1)
		ctx, cancel := context.WithTimeout(t.Context(), 31*time.Second)
		defer cancel()

		start := time.Now()
		client := p.Client(ctx, 4)
		if elapsed := time.Since(start); elapsed != 30*time.Second {
			t.Errorf("DC creation took %v, want 30s", elapsed)
		}
		if err := ctx.Err(); err != nil {
			t.Errorf("parent context error = %v, want nil", err)
		}
		if client.Invoker() != api {
			t.Errorf("client invoker = %T, want degraded API client", client.Invoker())
		}
	})
}

func TestTakeoutRoutesRequestsToDC(t *testing.T) {
	for _, test := range []struct {
		name    string
		initErr error
	}{
		{name: "init fails", initErr: errTakeoutInit},
		{name: "init succeeds"},
	} {
		t.Run(test.name, func(t *testing.T) {
			api := &fakeAPI{initErr: test.initErr}
			p := newPool(api, 1)
			client := takeoutWithin(t, p, 4, 2*time.Second)
			if _, err := client.HelpGetConfig(t.Context()); !errors.Is(err, errStub) {
				t.Fatalf("HelpGetConfig error = %v, want %v", err, errStub)
			}
			inv := api.dcs[4]
			if inv == nil || len(inv.inputs) != 1 || len(api.invoker.inputs) != 0 {
				t.Fatalf("expected one request on DC 4 and none on the default DC")
			}
			input := inv.inputs[0]
			if test.initErr != nil {
				if _, ok := input.(*tg.HelpGetConfigRequest); !ok {
					t.Errorf("request = %T, want *tg.HelpGetConfigRequest", input)
				}
			} else if req, ok := input.(*tg.InvokeWithTakeoutRequest); !ok || req.TakeoutID != 42 {
				t.Errorf("request = %v, want takeout request with ID 42", input)
			}
		})
	}
}

func TestTakeoutConcurrentCallers(t *testing.T) {
	for _, tt := range []struct {
		name    string
		initErr error
	}{
		{name: "init fails", initErr: errTakeoutInit},
		{name: "network failure", initErr: errStub},
		{name: "init succeeds"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			entered, gate := make(chan struct{}), make(chan struct{})
			api := &fakeAPI{initErr: tt.initErr, entered: entered, gate: gate}
			p := newPool(api, 1)
			ctx := t.Context()
			var wg sync.WaitGroup
			for range 8 {
				wg.Go(func() {
					_, _ = p.Takeout(ctx, 2).HelpGetConfig(ctx)
				})
			}
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("takeout init never started")
			}
			time.Sleep(20 * time.Millisecond)
			close(gate)

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

func TestClientAndDefaultStayPlain(t *testing.T) {
	for _, tt := range []struct {
		name    string
		init    bool
		initErr error
	}{
		{name: "never tried"},
		{name: "init failed", init: true, initErr: errTakeoutInit},
		{name: "started", init: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			api := &fakeAPI{initErr: tt.initErr}
			p := newPool(api, 1)
			ctx := t.Context()
			if tt.init {
				takeoutWithin(t, p, 2, 2*time.Second)
			}
			_, _ = p.Client(ctx, 2).HelpGetConfig(ctx)
			_, _ = p.Default(ctx).HelpGetConfig(ctx)
			if len(api.invoker.inputs) != 2 {
				t.Fatalf("recorded %d requests, want 2", len(api.invoker.inputs))
			}
			for idx, input := range api.invoker.inputs {
				if _, ok := input.(*tg.HelpGetConfigRequest); !ok {
					t.Errorf("request %d = %T, want *tg.HelpGetConfigRequest", idx, input)
				}
			}
			if !tt.init || tt.initErr != nil {
				if err := closeWithin(t, p, 2*time.Second); err != nil {
					t.Errorf("Close error = %v, want nil", err)
				}
				if len(api.invoker.inputs) != 2 {
					t.Errorf("Close sent %d requests, want 0", len(api.invoker.inputs)-2)
				}
			}
		})
	}
}

func TestCloseDoesNotRaceClient(t *testing.T) {
	if !raceEnabled {
		t.Skip("needs -race")
	}
	p := newPool(&fakeAPI{}, 1)
	ctx := t.Context()
	p.Client(ctx, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for dc := 3; dc <= 10; dc++ {
		wg.Go(func() {
			<-start
			p.Client(ctx, dc)
		})
	}
	close(start)
	err := closeWithin(t, p, 2*time.Second)
	wg.Wait()
	if err != nil {
		t.Errorf("Close error = %v, want nil", err)
	}
}

func TestCloseDoesNotRaceTakeout(t *testing.T) {
	if !raceEnabled {
		t.Skip("needs -race")
	}
	p := newPool(&fakeAPI{}, 1)
	ctx := t.Context()
	start, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		<-start
		p.Takeout(ctx, 4)
	}()
	close(start)
	if err := closeWithin(t, p, 2*time.Second); err != nil && !errors.Is(err, errStub) {
		t.Errorf("Close error = %v, want nil or %v", err, errStub)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Takeout deadlocked")
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.takeout != 0 || len(p.closes) != 0 {
		t.Errorf("pool after Close and Takeout has takeout ID %d and %d closers, want zero", p.takeout, len(p.closes))
	}
}

func TestClientsAfterClose(t *testing.T) {
	for _, test := range []struct {
		name   string
		client func(*pool, context.Context) *tg.Client
	}{
		{name: "Takeout", client: func(p *pool, ctx context.Context) *tg.Client { return p.Takeout(ctx, 4) }},
		{name: "Client", client: func(p *pool, ctx context.Context) *tg.Client { return p.Client(ctx, 4) }},
		{name: "Default", client: (*pool).Default},
	} {
		t.Run(test.name, func(t *testing.T) {
			api := &fakeAPI{}
			p := newPool(api, 1)
			ctx := t.Context()
			p.Default(ctx)
			if err := closeWithin(t, p, 2*time.Second); err != nil {
				t.Fatalf("Close error = %v, want nil", err)
			}
			before := api.initCalls.Load()

			_, err := test.client(p, ctx).HelpGetConfig(ctx)
			if !errors.Is(err, errPoolClosed) {
				t.Errorf("HelpGetConfig error = %v, want %v", err, errPoolClosed)
			}
			if calls := api.initCalls.Load(); calls != before {
				t.Errorf("takeout init calls changed from %d to %d", before, calls)
			}
			if len(api.dcs) != 0 {
				t.Errorf("created %d new DC invokers, want 0", len(api.dcs))
			}
		})
	}
}

func TestCloseFinishesTakeoutOnlyWhenStarted(t *testing.T) {
	t.Run("failed init", func(t *testing.T) {
		api := &fakeAPI{initErr: errTakeoutInit}
		p := newPool(api, 1)
		takeoutWithin(t, p, 2, 2*time.Second)
		if err := closeWithin(t, p, 2*time.Second); err != nil {
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
		api := &fakeAPI{}
		p := newPool(api, 1)
		takeoutWithin(t, p, 2, 2*time.Second)
		before := time.Now()
		if err := closeWithin(t, p, 2*time.Second); errors.Is(err, errClosed) {
			t.Error("Close sent the finish request after closing the invoker")
		} else if !errors.Is(err, errStub) {
			t.Errorf("Close error = %v, want %v", err, errStub)
		}
		deadline := api.invoker.deadline
		if deadline.Before(before.Add(10*time.Second)) || deadline.After(time.Now().Add(10*time.Second)) {
			t.Errorf("finish request deadline = %v, want 10 seconds after Close started", deadline)
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

func TestCloseIsIdempotent(t *testing.T) {
	api := &fakeAPI{}
	p := newPool(api, 1)
	takeoutWithin(t, p, 4, 2*time.Second)
	if err := closeWithin(t, p, 2*time.Second); !errors.Is(err, errStub) {
		t.Fatalf("first Close error = %v, want %v", err, errStub)
	}
	if err := closeWithin(t, p, 2*time.Second); err != nil {
		t.Errorf("second Close error = %v, want nil", err)
	}
	if len(api.invoker.inputs) != 1 {
		t.Errorf("recorded %d finish requests, want 1", len(api.invoker.inputs))
	}
	if api.invoker.closeCalls != 1 || api.dcs[4].closeCalls != 1 {
		t.Errorf("close calls = %d on the default DC and %d on DC 4, want 1 each",
			api.invoker.closeCalls, api.dcs[4].closeCalls)
	}
}
