package tutil

import (
	"context"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"
)

func TestGetSingleMessageExhaustedHistoryIsDeleted(t *testing.T) {
	peer := &tg.InputPeerChannel{ChannelID: 42}
	invoker := telegram.InvokeFunc(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		require.Equal(t, &tg.MessagesGetHistoryRequest{Peer: peer, OffsetID: 101, Limit: 1}, input)
		output.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{}
		return nil
	})

	message, err := GetSingleMessage(t.Context(), tg.NewClient(invoker), peer, 100)
	require.ErrorIs(t, err, ErrMessageDeleted)
	require.Nil(t, message)
}
