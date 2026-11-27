package dl

import (
	"path/filepath"
	"testing"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/pkg/tmessage"
)

func TestMessageTopicID(t *testing.T) {
	manager := peers.Options{}.Build(nil)
	forum := manager.Channel(&tg.Channel{ID: 42, Megagroup: true, Forum: true})
	tests := []struct {
		name  string
		reply *tg.MessageReplyHeader
		want  int
	}{
		{"reply inside a topic", &tg.MessageReplyHeader{ForumTopic: true, ReplyToTopID: 5, ReplyToMsgID: 8}, 5},
		{"message directly in a topic", &tg.MessageReplyHeader{ForumTopic: true, ReplyToMsgID: 5}, 5},
		{"topic header with no ids", &tg.MessageReplyHeader{ForumTopic: true}, 1},
		{"cross-chat reply", &tg.MessageReplyHeader{ForumTopic: true, ReplyToMsgID: 8, ReplyToPeerID: &tg.PeerChannel{ChannelID: 99}}, 1},
		{"cross-chat reply inside a topic", &tg.MessageReplyHeader{ForumTopic: true, ReplyToTopID: 5, ReplyToMsgID: 8, ReplyToPeerID: &tg.PeerChannel{ChannelID: 99}}, 5},
		{"ordinary reply in General", &tg.MessageReplyHeader{ReplyToMsgID: 8}, 1},
		{"no reply header", nil, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message := &tg.Message{}
			if tt.reply != nil {
				message.SetReplyTo(tt.reply)
			}
			require.Equal(t, tt.want, messageTopicID(message, forum))
		})
	}
	for _, tt := range nonForumTopicTestPeers(manager) {
		t.Run(tt.name, func(t *testing.T) {
			message := &tg.Message{}
			require.Zero(t, messageTopicID(message, tt.peer))
			message.SetReplyTo(&tg.MessageReplyHeader{ForumTopic: true, ReplyToTopID: 5, ReplyToMsgID: 8})
			require.Zero(t, messageTopicID(message, tt.peer))
		})
	}
}

func nonForumTopicTestPeers(manager *peers.Manager) []struct {
	name string
	peer peers.Peer
} {
	return []struct {
		name string
		peer peers.Peer
	}{
		{"broadcast channel", manager.Channel(&tg.Channel{ID: 43, Broadcast: true})},
		{"supergroup without topics", manager.Channel(&tg.Channel{ID: 44, Megagroup: true})},
		{"basic group", manager.Chat(&tg.Chat{ID: 45})},
		{"private chat", manager.User(&tg.User{ID: 46})},
		{"bot chat with topics", manager.User(&tg.User{ID: 47, Bot: true, BotForumView: true})},
	}
}

func TestProcessSingleCreatesTopicPath(t *testing.T) {
	manager := peers.Options{}.Build(nil)
	forum := manager.Channel(&tg.Channel{ID: 42, Megagroup: true, Forum: true})
	message := &tg.Message{ID: 12, PeerID: &tg.PeerChannel{ChannelID: 42}}
	message.SetReplyTo(&tg.MessageReplyHeader{ForumTopic: true, ReplyToMsgID: 5})
	message.SetMedia(&tg.MessageMediaDocument{Document: &tg.Document{
		Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "example.txt"}},
	}})
	dir := t.TempDir()
	it, err := newIter(nil, manager, [][]*tmessage.Dialog{{{
		Peer:     forum.InputPeer(),
		Messages: []int{message.ID},
	}}}, Options{
		Dir:      dir,
		Template: "{{ .TopicID }}/{{ .DialogID }}_{{ .MessageID }}_{{ filenamify .FileName }}",
	}, 0)
	require.NoError(t, err)

	ok, skip := it.processSingle(t.Context(), message, forum, 0)
	require.NoError(t, it.Err())
	require.True(t, ok)
	require.False(t, skip)
	require.NoError(t, it.Value().(*iterElem).to.Close())
	require.DirExists(t, filepath.Join(dir, "5"))
	require.FileExists(t, filepath.Join(dir, "5", "42_12_example.txt.tmp"))
}
