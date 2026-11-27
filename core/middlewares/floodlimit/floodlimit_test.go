package floodlimit

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

const (
	floodWait5        = "FLOOD_WAIT_5"
	premiumFloodWait5 = "FLOOD_PREMIUM_WAIT_5"
)

func TestFloodLimitExhaustion(t *testing.T) {
	read := &tg.UploadGetFileRequest{}
	for _, tt := range []struct {
		name         string
		input        bin.Encoder
		premiumWaits bool
		wantCalls    int
		wantElapsed  time.Duration
	}{
		{name: "file", input: read, wantCalls: 11, wantElapsed: 50 * time.Second},
		{name: "takeout", input: &tg.InvokeWithTakeoutRequest{Query: read}, wantCalls: 11, wantElapsed: 50 * time.Second},
		{name: "alternating with premium", input: read, premiumWaits: true, wantCalls: 22, wantElapsed: 105 * time.Second},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
				defer cancel()

				calls := 0
				floodErr := tgerr.New(420, floodWait5)
				next := telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
					calls++
					if tt.premiumWaits && calls%2 == 1 {
						return tgerr.New(420, premiumFloodWait5)
					}
					return floodErr
				})
				start := time.Now()
				err := New(10).Handle(next)(ctx, tt.input, &tg.UploadFileBox{})

				if _, ok := tgerr.AsFloodWait(err); ok {
					t.Errorf("Invoke error = %v, still matches a flood wait", err)
				}
				if err == nil || !strings.Contains(err.Error(), floodErr.Error()) {
					t.Errorf("Invoke error = %v, want %v in the error text", err, floodErr)
				}
				if calls != tt.wantCalls {
					t.Errorf("Invoke made %d attempts, want %d", calls, tt.wantCalls)
				}
				if elapsed := time.Since(start); elapsed != tt.wantElapsed {
					t.Errorf("Invoke took %v, want %v", elapsed, tt.wantElapsed)
				}
			})
		})
	}
}

func TestFloodLimitAllowsRepeatedWaits(t *testing.T) {
	for _, tt := range []struct {
		name   string
		input  bin.Encoder
		output bin.Decoder
		flood  string
	}{
		{name: "non-file", input: &tg.ChannelsGetMessagesRequest{}, output: &tg.MessagesMessagesBox{}, flood: floodWait5},
		{name: "premium", input: &tg.UploadGetFileRequest{}, output: &tg.UploadFileBox{}, flood: premiumFloodWait5},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				next := telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
					calls++
					if calls <= 15 {
						return tgerr.New(420, tt.flood)
					}
					return nil
				})
				start := time.Now()

				if err := New(10).Handle(next)(t.Context(), tt.input, tt.output); err != nil {
					t.Errorf("Invoke error = %v, want nil", err)
				}
				if calls != 16 {
					t.Errorf("Invoke made %d attempts, want 16", calls)
				}
				if elapsed := time.Since(start); elapsed != 75*time.Second {
					t.Errorf("Invoke took %v, want 75s", elapsed)
				}
			})
		})
	}
}

func TestFloodLimitWaitDuration(t *testing.T) {
	for _, tt := range []struct {
		flood       string
		wantElapsed time.Duration
	}{
		{flood: "FLOOD_WAIT_120", wantElapsed: 120 * time.Second},
		{flood: "FLOOD_WAIT_0", wantElapsed: time.Second},
	} {
		t.Run(tt.flood, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				next := telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
					calls++
					if calls == 1 {
						return tgerr.New(420, tt.flood)
					}
					return nil
				})
				start := time.Now()

				if err := New(10).Handle(next)(t.Context(), &tg.UploadGetFileRequest{}, &tg.UploadFileBox{}); err != nil {
					t.Errorf("Invoke error = %v, want nil", err)
				}
				if calls != 2 {
					t.Errorf("Invoke made %d attempts, want 2", calls)
				}
				if elapsed := time.Since(start); elapsed != tt.wantElapsed {
					t.Errorf("Invoke took %v, want %v", elapsed, tt.wantElapsed)
				}
			})
		})
	}
}

func TestFloodLimitSuccessAsContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	next := telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
		cancel()
		return nil
	})
	if err := New(10).Handle(next)(ctx, &tg.UploadGetFileRequest{}, &tg.UploadFileBox{}); err != nil {
		t.Errorf("Invoke error = %v, want nil after a successful call", err)
	}
}

func TestFloodLimitStopsWaitingAtDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()

		calls := 0
		next := telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
			calls++
			return tgerr.New(420, floodWait5)
		})
		start := time.Now()

		err := New(10).Handle(next)(ctx, &tg.UploadGetFileRequest{}, &tg.UploadFileBox{})
		if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), tgerr.New(420, floodWait5).Error()) {
			t.Errorf("Invoke error = %v, want %v with the last flood wait", err, context.DeadlineExceeded)
		}
		if _, ok := tgerr.As(err); ok {
			t.Errorf("Invoke error = %v, still a Telegram error", err)
		}
		if _, ok := tgerr.AsFloodWait(err); ok {
			t.Errorf("Invoke error = %v, still a flood wait", err)
		}
		var netErr net.Error
		if errors.As(err, &netErr) {
			t.Errorf("Invoke error = %v, still a network error", err)
		}
		if calls != 1 {
			t.Errorf("Invoke made %d attempts, want 1", calls)
		}
		if elapsed := time.Since(start); elapsed != time.Second {
			t.Errorf("Invoke took %v, want 1s", elapsed)
		}
	})
}

func TestFloodLimitPassesThroughNonFloodError(t *testing.T) {
	want := errors.New("connection lost")
	next := telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error { return want })

	if err := New(10).Handle(next)(t.Context(), &tg.ChannelsGetMessagesRequest{}, &tg.MessagesMessagesBox{}); err != want {
		t.Errorf("Invoke error = %v, want original error %v", err, want)
	}
}
