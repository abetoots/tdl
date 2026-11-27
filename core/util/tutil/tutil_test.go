package tutil

import (
	"context"
	"errors"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

func TestGetGroupedMessagesReturnsHistoryError(t *testing.T) {
	want := errors.New("history lookup failed")
	calls := 0
	client := tg.NewClient(telegram.InvokeFunc(func(_ context.Context, input bin.Encoder, _ bin.Decoder) error {
		calls++
		if _, ok := input.(*tg.MessagesGetHistoryRequest); !ok {
			t.Errorf("request = %T, want messages.getHistory", input)
		}
		return want
	}))
	msg := &tg.Message{ID: 20}
	msg.SetGroupedID(1)

	messages, err := GetGroupedMessages(t.Context(), client, &tg.InputPeerSelf{}, msg)
	if !errors.Is(err, want) {
		t.Errorf("GetGroupedMessages returned %v, %v, want history error %v", messages, err, want)
	}
	if calls != 1 {
		t.Errorf("history lookup made %d calls, want 1", calls)
	}
}

func TestGetGroupedMessagesStopsAtBatchLimit(t *testing.T) {
	const batchSize = 20
	peer := &tg.PeerUser{UserID: 1}
	msg := &tg.Message{ID: batchSize, PeerID: peer}
	msg.SetGroupedID(1)
	page := &tg.MessagesMessagesSlice{Count: batchSize + 1, Messages: []tg.MessageClass{msg}}
	for id := 1; id < batchSize; id++ {
		page.Messages = append(page.Messages, &tg.Message{ID: id, PeerID: peer})
	}
	calls := 0
	client := tg.NewClient(telegram.InvokeFunc(func(_ context.Context, _ bin.Encoder, output bin.Decoder) error {
		calls++
		if calls > 1 {
			return errors.New("second history page failed")
		}
		output.(*tg.MessagesMessagesBox).Messages = page
		return nil
	}))

	messages, err := GetGroupedMessages(t.Context(), client, &tg.InputPeerSelf{}, msg)
	if err != nil || len(messages) != 1 || messages[0] != msg {
		t.Errorf("GetGroupedMessages returned %v, %v, want the original grouped message", messages, err)
	}
	if calls != 1 {
		t.Errorf("history lookup made %d calls, want 1", calls)
	}
}
