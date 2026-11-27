package tutil

import (
	"context"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"
)

func TestGetForumTopicsPaginatesShortPages(t *testing.T) {
	peer := &tg.InputPeerChannel{ChannelID: 42, AccessHash: 123}
	for _, tt := range []struct {
		name       string
		byCreation bool
		wantID     int
		wantDate   int
	}{
		{name: "top message of last live topic", wantID: 55, wantDate: 550},
		{name: "creation date of last live topic", byCreation: true, wantDate: 500},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pages := []tg.MessagesForumTopics{
				{
					Count:             3,
					OrderByCreateDate: tt.byCreation,
					Topics: []tg.ForumTopicClass{
						&tg.ForumTopic{ID: 1, Title: "Lobby", TopMessage: 11, Date: 600},
						&tg.ForumTopic{ID: 5, Title: "Releases", TopMessage: 55, Date: 500},
						&tg.ForumTopicDeleted{ID: 9},
					},
					Messages: []tg.MessageClass{&tg.Message{ID: 55, Date: 550}, &tg.Message{ID: 11, Date: 660}},
				},
				{Count: 3, Topics: []tg.ForumTopicClass{&tg.ForumTopic{ID: 13, Title: "Support"}}},
			}
			requests := []*tg.MessagesGetForumTopicsRequest{
				{Peer: peer, Limit: 100},
				{Peer: peer, Limit: 100, OffsetTopic: 5, OffsetID: tt.wantID, OffsetDate: tt.wantDate},
			}
			var calls int
			api := tg.NewClient(telegram.InvokeFunc(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
				require.Less(t, calls, len(pages))
				require.Equal(t, requests[calls], input)
				*output.(*tg.MessagesForumTopics) = pages[calls]
				calls++
				return nil
			}))

			got, err := GetForumTopics(t.Context(), api, peer)
			require.NoError(t, err)
			require.Equal(t, []ForumTopic{{ID: 1, Title: "Lobby"}, {ID: 5, Title: "Releases"}, {ID: 13, Title: "Support"}}, got)
			require.Equal(t, len(pages), calls)
		})
	}
}
