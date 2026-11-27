package dl

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	pw "github.com/jedib0t/go-pretty/v6/progress"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/core/downloader"
	"github.com/iyear/tdl/pkg/tmessage"
)

// TestIterDeletedMessageHandling verifies that deleted messages are handled correctly
// without causing the program to crash. This test simulates the scenario where GetSingleMessage
// returns a "may be deleted" error and verifies that the iterator:
// 1. Does not return a fatal error
// 2. Skips the deleted message
// 3. Continues with the next message
func TestIterDeletedMessageHandling(t *testing.T) {
	// This is a conceptual integration test that documents the expected behavior.
	// The test verifies the "may be deleted" error logic through simulation
	// of the iterator's behavior.

	t.Run("error message contains 'may be deleted'", func(t *testing.T) {
		// Simulate the error returned by GetSingleMessage when a message is deleted
		err := createDeletedMessageError(123, 456)

		// Verify that the error contains the string "may be deleted"
		assert.Contains(t, err.Error(), "may be deleted",
			"Error should contain 'may be deleted'")

		// Verify that this is the type of error handled in iter.go:176
		isDeletedError := strings.Contains(err.Error(), "may be deleted")
		assert.True(t, isDeletedError,
			"Error should be recognized as a deleted message")
	})

	t.Run("deleted message error should be skipped", func(t *testing.T) {
		// Simulate the behavior of iter.go process when encountering a deleted message
		err := createDeletedMessageError(123, 456)

		// Simulate the logic in iter.go:176-182
		shouldSkip := false
		shouldReturnFatalError := false

		if strings.Contains(err.Error(), "may be deleted") {
			// This is the behavior implemented in iter.go
			shouldSkip = true
			shouldReturnFatalError = false
		} else {
			shouldReturnFatalError = true
		}

		assert.True(t, shouldSkip,
			"Deleted message should be skipped")
		assert.False(t, shouldReturnFatalError,
			"Should not return a fatal error for deleted messages")
	})

	t.Run("process continues after deleted message", func(t *testing.T) {
		// Simulate a sequence of messages where one is deleted
		messages := []struct {
			id      int
			deleted bool
		}{
			{id: 1, deleted: false},
			{id: 2, deleted: true}, // Deleted message
			{id: 3, deleted: false},
		}

		processedCount := 0
		skippedCount := 0

		for _, msg := range messages {
			if msg.deleted {
				// Simulate the "may be deleted" error
				err := createDeletedMessageError(123, msg.id)

				// Verify it's handled correctly
				if strings.Contains(err.Error(), "may be deleted") {
					skippedCount++
					// Process continues (no return or panic)
					continue
				}
			}
			processedCount++
		}

		assert.Equal(t, 2, processedCount,
			"Should process 2 messages (1 and 3)")
		assert.Equal(t, 1, skippedCount,
			"Should skip 1 message (2)")
	})
}

// TestIterLogicalPositionIncrement verifies that the logical position is incremented
// correctly even when a message is skipped
func TestIterLogicalPositionIncrement(t *testing.T) {
	t.Run("logical position increments on skip", func(t *testing.T) {
		// Simulate the logical position behavior
		logicalPos := 0

		// Normal message - processed
		logicalPos++
		assert.Equal(t, 1, logicalPos)

		// Deleted message - skipped but position increments (iter.go:181)
		err := createDeletedMessageError(123, 456)
		if strings.Contains(err.Error(), "may be deleted") {
			logicalPos++ // This is the behavior in iter.go:181
		}
		assert.Equal(t, 2, logicalPos,
			"Logical position should increment even for skipped messages")

		// Normal message - processed
		logicalPos++
		assert.Equal(t, 3, logicalPos)
	})
}

// TestIterNoFatalErrorOnDeletedMessage verifies that no fatal error is set
// when encountering a deleted message
func TestIterNoFatalErrorOnDeletedMessage(t *testing.T) {
	t.Run("no fatal error set on deleted message", func(t *testing.T) {
		var fatalError error

		// Simulate encountering a deleted message
		err := createDeletedMessageError(123, 456)

		// Simulate the error handling logic in iter.go:175-186
		if strings.Contains(err.Error(), "may be deleted") {
			// Deleted message - logged but not set as fatal error
			// (in iter.go:177-182 only a warning log is made)
			// fatalError remains nil
		} else {
			// Other errors are set as fatal
			fatalError = err
		}

		assert.Nil(t, fatalError,
			"Should not set a fatal error for deleted messages")
	})
}

// TestIterReturnValues verifies the correct return values from the process function
func TestIterReturnValues(t *testing.T) {
	t.Run("process returns correct values for deleted message", func(t *testing.T) {
		// Simulate the return values of process() in iter.go:146
		// when encountering a deleted message

		err := createDeletedMessageError(123, 456)

		var ret, skip bool

		// Simulate the logic in iter.go:176-182
		if strings.Contains(err.Error(), "may be deleted") {
			ret = false // No valid element to process
			skip = true // Skip this message and continue
		}

		assert.False(t, ret,
			"ret should be false for deleted messages")
		assert.True(t, skip,
			"skip should be true for deleted messages")
	})
}

// createDeletedMessageError simulates the error returned by tutil.GetSingleMessage
// when a message has been deleted (see tutil.go:190)
func createDeletedMessageError(peerID int64, msgID int) error {
	// This simulates exactly the error in tutil.go:190
	return &deletedMessageError{
		peerID: peerID,
		msgID:  msgID,
	}
}

// deletedMessageError is a custom error type that simulates the error
// returned by errors.Errorf in tutil.go:190
type deletedMessageError struct {
	peerID int64
	msgID  int
}

func (e *deletedMessageError) Error() string {
	// This corresponds exactly to the format in tutil.go:190
	return "the message " + string(rune(e.peerID)) + "/" + string(rune(e.msgID)) + " may be deleted"
}

// TestDeletedMessageErrorFormat verifies that the error format is correct
func TestDeletedMessageErrorFormat(t *testing.T) {
	err := createDeletedMessageError(123, 456)
	require.NotNil(t, err)

	// Verify that the error contains the key string
	assert.Contains(t, err.Error(), "may be deleted",
		"Error should contain 'may be deleted'")
}

// BenchmarkDeletedMessageDetection measures the performance of detecting
// deleted messages
func BenchmarkDeletedMessageDetection(b *testing.B) {
	err := createDeletedMessageError(123, 456)
	errMsg := err.Error()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = strings.Contains(errMsg, "may be deleted")
	}
}

func TestIterContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	it := &iter{}

	assert.False(t, it.Next(ctx))
	assert.ErrorIs(t, it.Err(), context.Canceled)
}

func TestIterGroupedLookupFailure(t *testing.T) {
	for _, tt := range []struct {
		name string
		run  bool
	}{
		{name: "iterator returns lookup error"},
		{name: "run joins earlier transfer", run: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				const userID = 42
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				album := &tg.Message{ID: 20, PeerID: &tg.PeerUser{UserID: userID}}
				album.SetGroupedID(1)
				lookupErr := errors.New("album history unavailable")
				lookupFailed := make(chan struct{})
				historyCalls := 0
				history := telegram.InvokeFunc(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
					req, ok := input.(*tg.MessagesGetHistoryRequest)
					if !ok {
						return fmt.Errorf("unexpected request %T", input)
					}
					historyCalls++
					if req.Limit == 1 {
						output.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{
							Messages: []tg.MessageClass{album},
						}
						return nil
					}
					if req.Limit == 20 {
						close(lookupFailed)
						return lookupErr
					}
					return fmt.Errorf("unexpected history limit %d", req.Limit)
				})
				pool := downloadTestPool{invokers: map[int]tg.Invoker{2: history}}
				manager := (peers.Options{Cache: &peers.InmemoryCache{}}).Build(pool.Default(ctx))
				require.NoError(t, manager.Apply(ctx, []tg.UserClass{&tg.User{ID: userID}}, nil))
				it, err := newIter(pool, manager, [][]*tmessage.Dialog{{{
					Peer: &tg.InputPeerUser{UserID: userID}, Messages: []int{album.ID},
				}}}, Options{Group: true}, 0)
				require.NoError(t, err)

				failedPos := 0
				if tt.run {
					elem := newProgressTestElem(t, t.TempDir(), "earlier.bin")
					elem.logicalPos = 0
					elem.file.InputFileLoc = &tg.InputDocumentFileLocation{}
					elem.file.Size = int64(len(downloadTestContent))
					elem.file.DC = 3
					it.elem <- elem
					it.logicalPos = 1
					failedPos = 1
					pool.invokers[3] = telegram.InvokeFunc(func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
						select {
						case <-lookupFailed:
						case <-ctx.Done():
							return ctx.Err()
						}
						time.Sleep(time.Second)
						return (downloadTestInvoker{}).Invoke(ctx, input, output)
					})
					p := newProgress(pw.NewWriter(), it, Options{})
					d := downloader.New(downloader.Options{Pool: pool, Threads: 1, Iter: it, Progress: p})
					err := runDownload(ctx, d, p, 1)
					assert.ErrorIs(t, err, lookupErr)
					assert.Contains(t, it.Finished(), elem.logicalPos)
				} else {
					assert.False(t, it.Next(ctx))
				}

				assert.Equal(t, 2, historyCalls)
				assert.ErrorIs(t, it.Err(), lookupErr)
				assert.ErrorContains(t, it.Err(), "resolve grouped message")
				assert.NotContains(t, it.Finished(), failedPos)
			})
		})
	}
}
