package tclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

const (
	rpcCallFail = "RPC_CALL_FAIL"
	floodWait5  = "FLOOD_WAIT_5"
)

func withDefaultMiddlewares(ctx context.Context, next tg.Invoker) tg.Invoker {
	chain := NewDefaultMiddlewares(ctx, 5*time.Minute)
	for i := len(chain) - 1; i >= 0; i-- {
		next = chain[i].Handle(next)
	}
	return next
}

func TestDefaultMiddlewaresRPCFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		next := telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
			return tgerr.New(500, rpcCallFail)
		})
		start := time.Now()

		err := withDefaultMiddlewares(ctx, next).Invoke(ctx, nil, nil)
		if !tgerr.Is(err, rpcCallFail) {
			t.Errorf("Invoke error = %v, want %s", err, rpcCallFail)
		}
		if elapsed := time.Since(start); elapsed != time.Minute {
			t.Errorf("Invoke took %v, want 1m", elapsed)
		}
	})
}

func TestDefaultMiddlewaresFloodWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
		defer cancel()

		calls := 0
		next := telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
			calls++
			return tgerr.New(420, floodWait5)
		})
		start := time.Now()

		err := withDefaultMiddlewares(ctx, next).Invoke(ctx, &tg.UploadGetFileRequest{}, &tg.UploadFileBox{})
		if _, ok := tgerr.AsFloodWait(err); ok {
			t.Errorf("Invoke error = %v, still matches a flood wait", err)
		}
		if err == nil || !strings.Contains(err.Error(), tgerr.ErrFloodWait) {
			t.Errorf("Invoke error = %v, want %s", err, tgerr.ErrFloodWait)
		}
		if calls != 11 {
			t.Errorf("Invoke made %d attempts, want 11", calls)
		}
		if elapsed := time.Since(start); elapsed != 50*time.Second {
			t.Errorf("Invoke took %v, want 50s", elapsed)
		}
	})
}

func TestDefaultMiddlewaresWaitsForMessages(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		calls := 0
		next := telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
			calls++
			if calls <= 12 {
				return tgerr.New(420, floodWait5)
			}
			return nil
		})
		start := time.Now()

		if err := withDefaultMiddlewares(ctx, next).Invoke(ctx, &tg.ChannelsGetMessagesRequest{}, &tg.MessagesMessagesBox{}); err != nil {
			t.Errorf("Invoke error = %v, want nil", err)
		}
		if calls != 13 {
			t.Errorf("Invoke made %d attempts, want 13", calls)
		}
		if elapsed := time.Since(start); elapsed != time.Minute {
			t.Errorf("Invoke took %v, want 1m", elapsed)
		}
	})
}

func TestDefaultMiddlewaresRecoversTransport(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		calls := 0
		transportErr := errors.New("connection lost")
		next := telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
			calls++
			if calls < 3 {
				return transportErr
			}
			return nil
		})

		if err := withDefaultMiddlewares(ctx, next).Invoke(ctx, nil, nil); err != nil {
			t.Errorf("Invoke error = %v, want nil", err)
		}
		if calls != 3 {
			t.Errorf("Invoke made %d attempts, want 3", calls)
		}
	})
}

func TestDefaultMiddlewaresDownloadRefusedChunk(t *testing.T) {
	for _, refusal := range []*tgerr.Error{tgerr.New(420, floodWait5), tgerr.New(500, rpcCallFail)} {
		t.Run(refusal.Message, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				const partSize = 1 << 20
				ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
				defer cancel()

				part := make([]byte, partSize)
				goodParts := 0
				next := telegram.InvokeFunc(func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
					if err := ctx.Err(); err != nil {
						return err
					}
					req, ok := input.(*tg.UploadGetFileRequest)
					if !ok {
						return fmt.Errorf("unexpected request %T", input)
					}
					if req.Offset == 3*partSize {
						return refusal
					}
					if req.Offset != int64(goodParts)*partSize || goodParts >= 3 || req.Limit != partSize {
						return fmt.Errorf("unexpected file part at offset %d with limit %d", req.Offset, req.Limit)
					}
					goodParts++
					output.(*tg.UploadFileBox).File = &tg.UploadFile{Type: &tg.StorageFileUnknown{}, Bytes: part}
					return nil
				})
				rpc := tg.NewClient(withDefaultMiddlewares(t.Context(), next))
				loc := &tg.InputDocumentFileLocation{ID: 1}
				_, err := downloader.NewDownloader().WithPartSize(partSize).Download(rpc, loc).Stream(ctx, io.Discard)

				if goodParts != 3 {
					t.Errorf("download got %d good parts, want 3", goodParts)
				}
				if ctx.Err() != nil {
					t.Errorf("download reached its deadline: %v", err)
				}
				if err == nil || !strings.Contains(err.Error(), refusal.Type) {
					t.Errorf("download error = %v, want %s", err, refusal.Type)
				}
			})
		})
	}
}
