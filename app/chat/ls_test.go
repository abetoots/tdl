package chat

import (
	"context"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"
)

func TestProcessChannelListsForumTopicPages(t *testing.T) {
	channel := &tg.Channel{ID: 42, AccessHash: 123, Megagroup: true, Forum: true}
	entities := peer.NewEntities(nil, nil, map[int64]*tg.Channel{channel.ID: channel})
	firstPage := make([]tg.ForumTopicClass, 100)
	for index := range firstPage {
		firstPage[index] = &tg.ForumTopic{ID: index + 1, Title: "Existing"}
	}
	firstPage[99].(*tg.ForumTopic).TopMessage = 200
	pages := []tg.MessagesForumTopics{
		{Count: 101, Topics: firstPage, Messages: []tg.MessageClass{&tg.Message{ID: 200, Date: 300}}},
		{Count: 101, Topics: []tg.ForumTopicClass{&tg.ForumTopic{ID: 101, Title: "Release notes"}}},
	}
	wantRequests := []*tg.MessagesGetForumTopicsRequest{
		{Peer: channel.AsInputPeer(), Limit: 100},
		{Peer: channel.AsInputPeer(), Limit: 100, OffsetTopic: 100, OffsetID: 200, OffsetDate: 300},
	}
	var calls int
	api := tg.NewClient(telegram.InvokeFunc(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		require.Less(t, calls, len(pages))
		require.Equal(t, wantRequests[calls], input)
		*output.(*tg.MessagesForumTopics) = pages[calls]
		calls++
		return nil
	}))

	dialog := processChannel(t.Context(), api, channel.ID, entities)
	require.NotNil(t, dialog)
	require.Len(t, dialog.Topics, 101)
	require.Equal(t, Topic{ID: 1, Title: "Existing"}, dialog.Topics[0])
	require.Equal(t, Topic{ID: 101, Title: "Release notes"}, dialog.Topics[100])
	require.Equal(t, len(pages), calls)
}
