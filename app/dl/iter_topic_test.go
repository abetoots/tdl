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
	channel := manager.Channel(&tg.Channel{ID: 43, Broadcast: true})
	tests := []struct {
		name  string
		from  peers.Peer
		reply *tg.MessageReplyHeader
		want  int
	}{
		{
			name:  "reply inside a topic",
			from:  forum,
			reply: &tg.MessageReplyHeader{ForumTopic: true, ReplyToTopID: 5, ReplyToMsgID: 8},
			want:  5,
		},
		{
			name:  "message directly in a topic",
			from:  forum,
			reply: &tg.MessageReplyHeader{ForumTopic: true, ReplyToMsgID: 5},
			want:  5,
		},
		{
			name:  "ordinary reply in General",
			from:  forum,
			reply: &tg.MessageReplyHeader{ReplyToMsgID: 8},
			want:  1,
		},
		{
			name: "channel outside a forum",
			from: channel,
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message := &tg.Message{}
			if tt.reply != nil {
				message.SetReplyTo(tt.reply)
			}
			require.Equal(t, tt.want, messageTopicID(message, tt.from))
		})
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
