package tclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/iyear/tdl/core/middlewares/takeout"
)

type discardWriterAt struct{}

func (discardWriterAt) WriteAt(p []byte, _ int64) (int, error) {
	return len(p), nil
}

func decodeFileRead(input bin.Encoder, wrapped bool) (*tg.UploadGetFileRequest, error) {
	if !wrapped {
		if req, ok := input.(*tg.UploadGetFileRequest); ok {
			return req, nil
		}
		return nil, fmt.Errorf("unexpected plain request %T", input)
	}

	takeoutReq, ok := input.(*tg.InvokeWithTakeoutRequest)
	if !ok || takeoutReq.TakeoutID != 1 {
		return nil, fmt.Errorf("unexpected takeout request %v", input)
	}
	var buf bin.Buffer
	if err := takeoutReq.Query.Encode(&buf); err != nil {
		return nil, fmt.Errorf("encode takeout query: %w", err)
	}
	req := &tg.UploadGetFileRequest{}
	if err := req.Decode(&buf); err != nil {
		return nil, fmt.Errorf("decode takeout query: %w", err)
	}
	return req, nil
}

func TestDefaultMiddlewaresDownloadRefusedChunk(t *testing.T) {
	floodErr := tgerr.New(420, floodWait5)
	rpcErr := tgerr.New(500, rpcCallFail)
	timeoutErr := tgerr.New(500, tg.ErrTimeout)
	transportErr := fmt.Errorf("chunk transport timed out: %w", context.DeadlineExceeded)
	netTimeout := &net.DNSError{Err: "chunk network timed out", IsTimeout: true}
	for _, tt := range []struct {
		name    string
		wrapped bool
		refusal error
	}{
		{name: "plain_flood", refusal: floodErr},
		{name: "takeout_flood", wrapped: true, refusal: floodErr},
		{name: "takeout_rpc", wrapped: true, refusal: rpcErr},
		{name: "takeout_timeout", wrapped: true, refusal: timeoutErr},
		{name: "takeout_transport_timeout", wrapped: true, refusal: transportErr},
		{name: "takeout_net_timeout", wrapped: true, refusal: netTimeout},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, parallel := range []bool{false, true} {
				name := "stream"
				if parallel {
					name = "parallel"
				}
				t.Run(name, func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						const partSize = 1 << 20
						ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
						defer cancel()

						part := make([]byte, partSize)
						var goodParts atomic.Int32
						next := telegram.InvokeFunc(func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
							if err := ctx.Err(); err != nil {
								return err
							}
							req, err := decodeFileRead(input, tt.wrapped)
							if err != nil {
								return err
							}
							if req.Limit != partSize || req.Offset < 0 || req.Offset%partSize != 0 {
								return fmt.Errorf("unexpected file part at offset %d with limit %d", req.Offset, req.Limit)
							}
							if req.Offset == 3*partSize {
								// allow virtual time to advance even if the downloader retries immediately.
								time.Sleep(time.Second)
								return tt.refusal
							}
							file := &tg.UploadFile{Type: &tg.StorageFileUnknown{}}
							if req.Offset < 3*partSize {
								file.Bytes = part
								goodParts.Add(1)
							}
							output.(*tg.UploadFileBox).File = file
							return nil
						})
						chain := withDefaultMiddlewares(t.Context(), reconnectBudget, next)
						if tt.wrapped {
							chain = takeout.Middleware(1).Handle(chain)
						}
						loc := &tg.InputDocumentFileLocation{ID: 1}
						builder := downloader.NewDownloader().WithPartSize(partSize).Download(tg.NewClient(chain), loc)
						var err error
						if parallel {
							_, err = builder.WithThreads(4).Parallel(ctx, discardWriterAt{})
						} else {
							_, err = builder.Stream(ctx, io.Discard)
						}

						if calls := goodParts.Load(); calls != 3 {
							t.Errorf("download got %d good parts, want 3", calls)
						}
						if ctx.Err() != nil {
							t.Errorf("download reached its deadline: %v", err)
						}
						if err == nil || !strings.Contains(err.Error(), tt.refusal.Error()) {
							t.Errorf("download error = %v, want %v", err, tt.refusal)
						}
						var netErr net.Error
						if _, flood := tgerr.AsFloodWait(err); flood || tgerr.Is(err, tg.ErrTimeout) ||
							errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) && netErr.Timeout() {
							t.Errorf("download returned a retryable error: %v", err)
						}
					})
				})
			}
		})
	}
}
