package dl

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/core/dcpool"
	"github.com/iyear/tdl/pkg/tmessage"
)

func TestProcessSingleTopicTitle(t *testing.T) {
	manager := peers.Options{}.Build(nil)
	forum := manager.Channel(&tg.Channel{ID: 42, Megagroup: true, Forum: true})
	otherForum := manager.Channel(&tg.Channel{ID: 43, Megagroup: true, Forum: true})
	channel := manager.Channel(&tg.Channel{ID: 44, Broadcast: true})
	topics := map[int64][]tg.ForumTopicClass{
		forum.ID(): {
			&tg.ForumTopic{ID: 1, Title: "Lobby"},
			&tg.ForumTopic{ID: 5, Title: "Releases/Builds"},
			&tg.ForumTopicDeleted{ID: 9},
		},
		otherForum.ID(): {&tg.ForumTopic{ID: 5, Title: "Support"}},
	}
	calls := make(map[int64]int)
	invoker := telegram.InvokeFunc(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		req, ok := input.(*tg.MessagesGetForumTopicsRequest)
		require.True(t, ok, "unexpected request %T", input)
		id := req.Peer.(*tg.InputPeerChannel).ChannelID
		page, ok := topics[id]
		require.True(t, ok, "unexpected channel %d", id)
		calls[id]++
		*output.(*tg.MessagesForumTopics) = tg.MessagesForumTopics{Count: len(page), Topics: page}
		return nil
	})
	it := newTopicTitleTestIter(t, forum, invoker, `{{ .TopicID }}_{{ replace .TopicTitle "/" " at " }}_/{{ .MessageID }}`)

	tests := []struct {
		from    peers.Channel
		topicID int
		wantDir string
	}{
		{forum, 0, "1_Lobby_"},
		{forum, 5, "5_Releases at Builds_"},
		{forum, 5, "5_Releases at Builds_"},
		{forum, 9, "9_topic 9_"},
		{otherForum, 0, "1_General_"},
		{otherForum, 5, "5_Support_"},
		{channel, 0, "0__"},
	}
	for index, tt := range tests {
		message := topicTitleTestMessage(100+index, tt.topicID, tt.from)
		ok, skip := it.processSingle(t.Context(), message, tt.from, index)
		require.NoError(t, it.Err())
		require.True(t, ok)
		require.False(t, skip)
		require.NoError(t, it.Value().(*iterElem).to.Close())
		require.FileExists(t, filepath.Join(it.opts.Dir, tt.wantDir, fmt.Sprintf("%d.tmp", message.ID)))
	}
	require.Equal(t, map[int64]int{forum.ID(): 1, otherForum.ID(): 1}, calls)
}

func TestProcessSingleTopicTitleLookupError(t *testing.T) {
	manager := peers.Options{}.Build(nil)
	forum := manager.Channel(&tg.Channel{ID: 42, Megagroup: true, Forum: true})
	wantErr := errors.New("topics unavailable")
	invoker := telegram.InvokeFunc(func(_ context.Context, input bin.Encoder, _ bin.Decoder) error {
		require.IsType(t, &tg.MessagesGetForumTopicsRequest{}, input)
		return wantErr
	})
	it := newTopicTitleTestIter(t, forum, invoker, `{{ .TopicTitle }}`)

	ok, skip := it.processSingle(t.Context(), topicTitleTestMessage(100, 5, forum), forum, 0)
	require.ErrorIs(t, it.Err(), wantErr)
	require.ErrorContains(t, it.Err(), "execute template")
	require.ErrorContains(t, it.Err(), "get forum topics")
	require.False(t, ok)
	require.False(t, skip)
}

type topicTitleTestPool struct {
	dcpool.Pool
	api *tg.Client
}

func (p topicTitleTestPool) Default(context.Context) *tg.Client { return p.api }

func newTopicTitleTestIter(t *testing.T, from peers.Channel, invoker tg.Invoker, nameTemplate string) *iter {
	t.Helper()
	pool := topicTitleTestPool{api: tg.NewClient(invoker)}
	it, err := newIter(pool, nil, [][]*tmessage.Dialog{{{
		Peer:     from.InputPeer(),
		Messages: []int{100},
	}}}, Options{Dir: t.TempDir(), Template: nameTemplate}, 0)
	require.NoError(t, err)
	return it
}

func topicTitleTestMessage(id, topicID int, from peers.Channel) *tg.Message {
	message := &tg.Message{ID: id, PeerID: &tg.PeerChannel{ChannelID: from.ID()}}
	if topicID != 0 {
		message.SetReplyTo(&tg.MessageReplyHeader{ForumTopic: true, ReplyToMsgID: topicID})
	}
	message.SetMedia(&tg.MessageMediaDocument{Document: &tg.Document{
		Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "example.txt"}},
	}})
	return message
}
