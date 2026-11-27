package tclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/iyear/tdl/core/middlewares/takeout"
)

const (
	rpcCallFail     = "RPC_CALL_FAIL"
	floodWait5      = "FLOOD_WAIT_5"
	reconnectBudget = 5 * time.Minute
)

func withDefaultMiddlewares(ctx context.Context, timeout time.Duration, next tg.Invoker) tg.Invoker {
	chain := NewDefaultMiddlewares(ctx, timeout)
	for i := len(chain) - 1; i >= 0; i-- {
		next = chain[i].Handle(next)
	}
	return next
}

type chainReply func(context.Context, int, time.Time) error

func failForCalls(err error, failures int, finishAt time.Duration) chainReply {
	return func(_ context.Context, call int, start time.Time) error {
		if call <= failures {
			return err
		}
		time.Sleep(time.Until(start.Add(finishAt)))
		return nil
	}
}

func failAt(err error, delayedCall int, finishAt time.Duration) chainReply {
	return func(_ context.Context, call int, start time.Time) error {
		if call == delayedCall {
			time.Sleep(time.Until(start.Add(finishAt)))
		}
		return err
	}
}

type chainCase struct {
	name           string
	chunk          bool
	unlimited      bool
	failure        error
	reply          chainReply
	cancelAt       time.Duration
	deadlineAt     time.Duration
	wantCalls      int
	wantElapsed    time.Duration
	wantRPC        string
	wantText       string
	wantCanceled   bool
	wantDeadline   bool
	wantNetTimeout bool
}

func TestDefaultMiddlewaresErrorContract(t *testing.T) {
	rpcErr := tgerr.New(500, rpcCallFail)
	floodErr := tgerr.New(420, floodWait5)
	premiumErr := tgerr.New(420, "FLOOD_PREMIUM_WAIT_5")
	const layerInvalid = "CONNECTION_LAYER_INVALID"
	layerErr := tgerr.New(400, layerInvalid)
	deadlineErr := fmt.Errorf("transport request expired: %w", context.DeadlineExceeded)
	netTimeout := &net.DNSError{Err: "transport timed out", IsTimeout: true}
	transportErr := errors.New("connection lost")
	cases := []chainCase{
		{name: "chunk ordinary flood", chunk: true, failure: floodErr, wantCalls: 11, wantElapsed: 50 * time.Second, wantText: floodErr.Error()},
		{name: "other ordinary flood", reply: failForCalls(floodErr, 12, time.Minute), wantCalls: 13, wantElapsed: time.Minute},
		{name: "chunk premium flood", chunk: true, reply: failForCalls(premiumErr, 12, time.Minute), wantCalls: 13, wantElapsed: time.Minute},
		{name: "other premium flood", reply: failForCalls(premiumErr, 12, time.Minute), wantCalls: 13, wantElapsed: time.Minute},
		{name: "chunk Telegram 400", chunk: true, failure: layerErr, wantCalls: 1, wantRPC: layerInvalid, wantText: layerErr.Error()},
		{name: "other Telegram 400", failure: layerErr, wantCalls: 1, wantRPC: layerInvalid, wantText: layerErr.Error()},
		{
			name: "other RPC recovers after 70s",
			reply: func(_ context.Context, _ int, start time.Time) error {
				if time.Since(start) < 70*time.Second {
					return rpcErr
				}
				// finish at a fixed time after the jittered eighth attempt starts.
				time.Sleep(time.Until(start.Add(73 * time.Second)))
				return nil
			},
			wantCalls: 8, wantElapsed: 73 * time.Second,
		},
		{name: "chunk cancel during retry", chunk: true, failure: rpcErr, cancelAt: time.Second, wantCalls: 1, wantElapsed: time.Second, wantText: rpcErr.Error(), wantCanceled: true},
		{name: "other cancel during flood", failure: floodErr, cancelAt: time.Second, wantCalls: 1, wantElapsed: time.Second, wantText: floodErr.Error(), wantCanceled: true},
		{name: "chunk deadline during retry", chunk: true, failure: rpcErr, deadlineAt: time.Second, wantCalls: 1, wantElapsed: time.Second, wantText: rpcErr.Error(), wantDeadline: true},
		{name: "other deadline during flood", failure: floodErr, deadlineAt: time.Second, wantCalls: 1, wantElapsed: time.Second, wantText: floodErr.Error(), wantDeadline: true},
		{
			name: "chunk succeeds as context ends", chunk: true, cancelAt: time.Second,
			reply: func(ctx context.Context, _ int, _ time.Time) error {
				<-ctx.Done()
				return nil
			},
			wantCalls: 1, wantElapsed: time.Second,
		},
	}

	retryTypes := []string{
		"Timedout",
		"No workers running",
		rpcCallFail,
		"RPC_MCGET_FAIL",
		"WORKER_BUSY_TOO_LONG_RETRY",
		"memory limit exit",
		tg.ErrTimeout,
	}
	for _, typ := range retryTypes {
		err := tgerr.New(500, typ)
		wantRPC := typ
		if typ == tg.ErrTimeout {
			wantRPC = ""
		}
		cases = append(cases, chainCase{
			name: "chunk " + typ, chunk: true, failure: err, wantCalls: 5, wantElapsed: time.Minute,
			wantRPC: wantRPC, wantText: err.Error(),
		})
	}

	for _, tt := range cases {
		tt.name += " unlimited"
		tt.unlimited = true
		cases = append(cases, tt)
	}

	for _, typ := range retryTypes {
		err := tgerr.New(500, typ)
		// start the second retry batch at 4m regardless of recovery jitter.
		reply := failAt(err, 6, 4*time.Minute)
		cases = append(cases,
			chainCase{name: "other " + typ, reply: reply, wantCalls: 10, wantElapsed: reconnectBudget, wantText: err.Error()},
			// the unlimited case starts another batch after the finite budget.
			chainCase{
				name: "other " + typ + " unlimited", unlimited: true, reply: reply, cancelAt: 301 * time.Second,
				wantCalls: 11, wantElapsed: 301 * time.Second, wantText: err.Error(), wantCanceled: true,
			},
		)
	}

	cases = append(cases, []chainCase{
		{name: "chunk transport deadline", chunk: true, reply: failAt(deadlineErr, 2, reconnectBudget), wantCalls: 2, wantElapsed: reconnectBudget, wantText: deadlineErr.Error()},
		{name: "other transport deadline", reply: failAt(deadlineErr, 2, reconnectBudget), wantCalls: 2, wantElapsed: reconnectBudget, wantText: deadlineErr.Error(), wantDeadline: true, wantNetTimeout: true},
		{name: "chunk net timeout", chunk: true, reply: failAt(netTimeout, 2, reconnectBudget), wantCalls: 2, wantElapsed: reconnectBudget, wantText: netTimeout.Error()},
		{name: "other net timeout", reply: failAt(netTimeout, 2, reconnectBudget), wantCalls: 2, wantElapsed: reconnectBudget, wantText: netTimeout.Error(), wantNetTimeout: true},
		{name: "chunk non-timeout transport", chunk: true, reply: failAt(transportErr, 2, reconnectBudget), wantCalls: 2, wantElapsed: reconnectBudget, wantText: transportErr.Error()},
		{name: "other non-timeout transport", reply: failAt(transportErr, 2, reconnectBudget), wantCalls: 2, wantElapsed: reconnectBudget, wantText: transportErr.Error()},
		{name: "other transport recovery", reply: failForCalls(transportErr, 2, 2*time.Second), wantCalls: 3, wantElapsed: 2 * time.Second},
		{
			name:  "chunk canceled transport",
			chunk: true,
			reply: func(ctx context.Context, _ int, _ time.Time) error {
				<-ctx.Done()
				return deadlineErr
			},
			cancelAt: time.Second, wantCalls: 1, wantElapsed: time.Second, wantText: context.Canceled.Error(), wantCanceled: true,
		},
		{name: "other RPC with unlimited recovery", unlimited: true, failure: rpcErr, cancelAt: 320 * time.Second, wantCalls: 28, wantElapsed: 320 * time.Second, wantText: rpcErr.Error(), wantCanceled: true},
	}...)
	const lateFailure = 310 * time.Second
	for _, tt := range []struct {
		name string
		err  error
	}{
		{name: "plain transport", err: transportErr},
		{name: "transport deadline", err: deadlineErr},
		{name: "net timeout", err: netTimeout},
	} {
		reply := func(_ context.Context, call int, start time.Time) error {
			if call == 1 {
				time.Sleep(time.Until(start.Add(lateFailure)))
				return tt.err
			}
			time.Sleep(time.Until(start.Add(lateFailure + time.Second)))
			return nil
		}
		cases = append(cases,
			chainCase{
				name: "chunk " + tt.name + " with unlimited recovery", chunk: true, unlimited: true, reply: reply,
				wantCalls: 2, wantElapsed: lateFailure + time.Second,
			},
			chainCase{
				name: "other " + tt.name + " with unlimited recovery", unlimited: true, reply: reply,
				wantCalls: 2, wantElapsed: lateFailure + time.Second,
			},
		)
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.chunk {
				runChainCase(t, tt, false)
				return
			}
			t.Run("plain", func(t *testing.T) { runChainCase(t, tt, false) })
			t.Run("takeout", func(t *testing.T) { runChainCase(t, tt, true) })
		})
	}
}

func runChainCase(t *testing.T, tt chainCase, wrapped bool) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		deadline := tt.deadlineAt
		if deadline == 0 {
			deadline = 6 * time.Minute
		}
		parent, cancelParent := context.WithTimeout(t.Context(), deadline)
		defer cancelParent()
		ctx, cancel := context.WithCancel(parent)
		defer cancel()
		if tt.cancelAt != 0 {
			timer := time.NewTimer(tt.cancelAt)
			defer timer.Stop()
			go func() {
				select {
				case <-timer.C:
					cancel()
				case <-ctx.Done():
				}
			}()
		}

		var input bin.Encoder = &tg.ChannelsGetMessagesRequest{}
		var output bin.Decoder = &tg.MessagesMessagesBox{}
		if tt.chunk {
			input, output = &tg.UploadGetFileRequest{Location: &tg.InputDocumentFileLocation{ID: 1}, Limit: 1 << 20}, &tg.UploadFileBox{}
		}
		timeout := reconnectBudget
		if tt.unlimited {
			timeout = 0
		}
		calls := 0
		start := time.Now()
		next := telegram.InvokeFunc(func(ctx context.Context, input bin.Encoder, _ bin.Decoder) error {
			if tt.chunk {
				if _, err := decodeFileRead(input, wrapped); err != nil {
					t.Fatal(err)
				}
			}
			calls++
			if tt.reply != nil {
				return tt.reply(ctx, calls, start)
			}
			return tt.failure
		})
		chain := withDefaultMiddlewares(t.Context(), timeout, next)
		if wrapped {
			chain = takeout.Middleware(1).Handle(chain)
		}
		err := chain.Invoke(ctx, input, output)

		if calls != tt.wantCalls {
			t.Errorf("Invoke made %d calls, want %d", calls, tt.wantCalls)
		}
		if elapsed := time.Since(start); elapsed != tt.wantElapsed {
			t.Errorf("Invoke took %v, want %v", elapsed, tt.wantElapsed)
		}
		if tt.wantText == "" {
			if err != nil {
				t.Errorf("Invoke error = %v, want nil", err)
			}
		} else if err == nil || !strings.Contains(err.Error(), tt.wantText) {
			t.Errorf("Invoke error = %v, want text %q", err, tt.wantText)
		}
		if _, ok := tgerr.As(err); ok != (tt.wantRPC != "") || tt.wantRPC != "" && !tgerr.Is(err, tt.wantRPC) {
			t.Errorf("Invoke error = %v, want Telegram type %q", err, tt.wantRPC)
		}
		if _, ok := tgerr.AsFloodWait(err); ok {
			t.Errorf("Invoke returned a retryable flood wait: %v", err)
		}
		if got := errors.Is(err, context.Canceled); got != tt.wantCanceled {
			t.Errorf("Invoke error = %v, matches cancellation = %t, want %t", err, got, tt.wantCanceled)
		}
		if got := errors.Is(err, context.DeadlineExceeded); got != tt.wantDeadline {
			t.Errorf("Invoke error = %v, matches deadline exceeded = %t, want %t", err, got, tt.wantDeadline)
		}
		var netErr net.Error
		if got := errors.As(err, &netErr); got != tt.wantNetTimeout || got && !netErr.Timeout() {
			t.Errorf("Invoke error = %v, matches net timeout = %t, want %t", err, got, tt.wantNetTimeout)
		}
	})
}
