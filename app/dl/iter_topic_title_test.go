package dl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/core/dcpool"
	"github.com/iyear/tdl/pkg/tmessage"
)

const documentedTopicTitleTemplate = `{{ .TopicID }}{{ with .TopicTitle }} {{ filenamify . 60 }}{{ end }}/{{ .DialogID }}_{{ .MessageID }}_{{ filenamify .FileName }}`

func TestProcessSingleTopicTitle(t *testing.T) {
	manager := peers.Options{}.Build(nil)
	forum := manager.Channel(&tg.Channel{ID: 42, Megagroup: true, Forum: true})
	otherForum := manager.Channel(&tg.Channel{ID: 43, Megagroup: true, Forum: true})
	topics := map[int64][]tg.ForumTopicClass{
		forum.ID(): {
			&tg.ForumTopic{ID: 1, Title: "Lobby"},
			&tg.ForumTopic{ID: 5, Title: "Releases/Builds"},
			&tg.ForumTopic{ID: 8},
		},
		otherForum.ID(): {&tg.ForumTopic{ID: 5, Title: "Support"}},
	}
	type lookup struct {
		channel int64
		topic   int
	}
	byID := map[lookup]tg.ForumTopicClass{
		{forum.ID(), 7}: &tg.ForumTopic{ID: 7, Title: "Announcements"},
		{forum.ID(), 9}: &tg.ForumTopicDeleted{ID: 9},
	}
	var lookups []lookup
	calls := make(map[int64]int)
	invoker := telegram.InvokeFunc(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		var page []tg.ForumTopicClass
		switch req := input.(type) {
		case *tg.MessagesGetForumTopicsRequest:
			channel, ok := req.Peer.(*tg.InputPeerChannel)
			require.True(t, ok && channel != nil, "unexpected peer %T", req.Peer)
			page, ok = topics[channel.ChannelID]
			require.True(t, ok, "unexpected channel %d", channel.ChannelID)
			calls[channel.ChannelID]++
		case *tg.MessagesGetForumTopicsByIDRequest:
			channel, ok := req.Peer.(*tg.InputPeerChannel)
			require.True(t, ok && channel != nil, "unexpected peer %T", req.Peer)
			require.Len(t, req.Topics, 1)
			key := lookup{channel.ChannelID, req.Topics[0]}
			lookups = append(lookups, key)
			if topic := byID[key]; topic != nil {
				page = []tg.ForumTopicClass{topic}
			}
		default:
			return fmt.Errorf("unexpected request %T", input)
		}
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
		{forum, 7, "7_Announcements_"},
		{forum, 7, "7_Announcements_"},
		{forum, 8, "8_topic 8_"},
		{forum, 9, "9_topic 9_"},
		{forum, 9, "9_topic 9_"},
		{otherForum, 0, "1_General_"},
		{otherForum, 0, "1_General_"},
		{otherForum, 5, "5_Support_"},
	}
	for index, tt := range tests {
		message := topicTitleTestMessage(100+index, tt.topicID)
		ok, skip := it.processSingle(t.Context(), message, tt.from, index)
		require.NoError(t, it.Err())
		require.True(t, ok)
		require.False(t, skip)
		require.NoError(t, it.Value().(*iterElem).to.Close())
		require.FileExists(t, filepath.Join(it.opts.Dir, tt.wantDir, fmt.Sprintf("%d.tmp", message.ID)))
	}
	require.Equal(t, map[int64]int{forum.ID(): 1, otherForum.ID(): 1}, calls)
	require.Equal(t, []lookup{{forum.ID(), 7}, {forum.ID(), 9}, {otherForum.ID(), 1}}, lookups)
}

func TestProcessSingleTopicTitleOutsideForums(t *testing.T) {
	manager := peers.Options{}.Build(nil)
	for _, tt := range nonForumTopicTestPeers(manager) {
		t.Run(tt.name, func(t *testing.T) {
			it := newTopicTitleTestIter(t, tt.peer, unexpectedTopicTitleRequest(t), documentedTopicTitleTemplate)
			message := topicTitleTestMessage(100, 5)
			ok, skip := it.processSingle(t.Context(), message, tt.peer, 0)
			require.NoError(t, it.Err())
			require.True(t, ok)
			require.False(t, skip)
			require.NoError(t, it.Value().(*iterElem).to.Close())
			require.FileExists(t, filepath.Join(it.opts.Dir, "0", fmt.Sprintf("%d_100_example.txt.tmp", tt.peer.ID())))
		})
	}
}

func TestProcessSingleTemplateWithoutTopicTitleMakesNoRequest(t *testing.T) {
	manager := peers.Options{}.Build(nil)
	forum := manager.Channel(&tg.Channel{ID: 42, Megagroup: true, Forum: true})
	it := newTopicTitleTestIter(t, forum, unexpectedTopicTitleRequest(t), `{{ .TopicID }}/{{ .FileName }}`)

	ok, skip := it.processSingle(t.Context(), topicTitleTestMessage(100, 5), forum, 0)
	require.NoError(t, it.Err())
	require.True(t, ok)
	require.False(t, skip)
	require.NoError(t, it.Value().(*iterElem).to.Close())
	require.FileExists(t, filepath.Join(it.opts.Dir, "5", "example.txt.tmp"))
}

func unexpectedTopicTitleRequest(t *testing.T) tg.Invoker {
	t.Helper()
	return telegram.InvokeFunc(func(_ context.Context, input bin.Encoder, _ bin.Decoder) error {
		t.Errorf("unexpected request %T", input)
		return fmt.Errorf("unexpected request %T", input)
	})
}

func TestProcessSingleTopicTitleLookupError(t *testing.T) {
	manager := peers.Options{}.Build(nil)
	forum := manager.Channel(&tg.Channel{ID: 42, Megagroup: true, Forum: true})
	wantErr := errors.New("topics unavailable")
	for _, tt := range []struct {
		name     string
		requests int
	}{
		{"list fails", 1},
		{"by-id fails", 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var requests int
			invoker := telegram.InvokeFunc(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
				requests++
				if requests == 1 {
					require.IsType(t, &tg.MessagesGetForumTopicsRequest{}, input)
					if tt.requests == 1 {
						return wantErr
					}
					*output.(*tg.MessagesForumTopics) = tg.MessagesForumTopics{}
					return nil
				}
				require.Equal(t, &tg.MessagesGetForumTopicsByIDRequest{Peer: forum.InputPeer(), Topics: []int{5}}, input)
				return wantErr
			})
			it := newTopicTitleTestIter(t, forum, invoker, `{{ .TopicID }}/{{ .TopicTitle }}`)

			ok, skip := it.processSingle(t.Context(), topicTitleTestMessage(100, 5), forum, 0)
			require.ErrorIs(t, it.Err(), wantErr)
			require.ErrorContains(t, it.Err(), "execute template")
			require.ErrorContains(t, it.Err(), "get forum topics")
			require.False(t, ok)
			require.False(t, skip)
			require.Empty(t, it.elem)
			entries, err := os.ReadDir(it.opts.Dir)
			require.NoError(t, err)
			require.Empty(t, entries)
			require.Equal(t, tt.requests, requests)
		})
	}
}

func TestProcessSingleDocumentedTopicTitleTemplate(t *testing.T) {
	manager := peers.Options{}.Build(nil)
	forum := manager.Channel(&tg.Channel{ID: 42, Megagroup: true, Forum: true})
	for _, tt := range []struct {
		name, title, folder string
	}{
		{"slash", "Releases/Builds", "5 Releases!Builds"},
		{"long multibyte title", strings.Repeat("界", 128), "5 " + strings.Repeat("界", 60)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			invoker := telegram.InvokeFunc(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
				require.IsType(t, &tg.MessagesGetForumTopicsRequest{}, input)
				*output.(*tg.MessagesForumTopics) = tg.MessagesForumTopics{
					Count: 1, Topics: []tg.ForumTopicClass{&tg.ForumTopic{ID: 5, Title: tt.title}},
				}
				return nil
			})
			it := newTopicTitleTestIter(t, forum, invoker, documentedTopicTitleTemplate)

			ok, skip := it.processSingle(t.Context(), topicTitleTestMessage(100, 5), forum, 0)
			require.NoError(t, it.Err())
			require.True(t, ok)
			require.False(t, skip)
			file := it.Value().(*iterElem).to
			require.NoError(t, file.Close())
			folder := filepath.Base(filepath.Dir(file.Name()))
			require.LessOrEqual(t, len(folder), 255)
			require.FileExists(t, filepath.Join(it.opts.Dir, tt.folder, "42_100_example.txt.tmp"))
		})
	}
}

func TestProcessGroupedUsesOneTopicFolder(t *testing.T) {
	channel := &tg.Channel{ID: 42, Megagroup: true, Forum: true}
	channel.SetAccessHash(123)
	peer := channel.AsInputPeer()
	first, second := topicTitleTestMessage(100, 5), topicTitleTestMessage(101, 5)
	for _, message := range []*tg.Message{first, second} {
		message.PeerID = &tg.PeerChannel{ChannelID: channel.ID}
		message.SetGroupedID(1234)
	}
	var historyCalls, topicCalls int
	api := tg.NewClient(telegram.InvokeFunc(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		switch req := input.(type) {
		case *tg.MessagesGetHistoryRequest:
			historyCalls++
			var messages []tg.MessageClass
			switch historyCalls {
			case 1:
				require.Equal(t, &tg.MessagesGetHistoryRequest{Peer: peer, OffsetID: 101, Limit: 1}, req)
				messages = []tg.MessageClass{first}
			case 2:
				require.Equal(t, &tg.MessagesGetHistoryRequest{Peer: peer, OffsetID: 111, Limit: 20}, req)
				messages = []tg.MessageClass{second, first}
			case 3:
				require.Equal(t, &tg.MessagesGetHistoryRequest{Peer: peer, OffsetID: 100, Limit: 20}, req)
			default:
				return fmt.Errorf("unexpected history request: %v", req)
			}
			output.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{
				Messages: messages, Chats: []tg.ChatClass{channel},
			}
		case *tg.MessagesGetForumTopicsRequest:
			require.Equal(t, peer, req.Peer)
			topicCalls++
			*output.(*tg.MessagesForumTopics) = tg.MessagesForumTopics{
				Count: 1, Topics: []tg.ForumTopicClass{&tg.ForumTopic{ID: 5, Title: "Album"}},
			}
		default:
			return fmt.Errorf("unexpected request %T", input)
		}
		return nil
	}))
	ctx := t.Context()
	manager := peers.Options{Cache: &peers.InmemoryCache{}}.Build(api)
	require.NoError(t, manager.Apply(ctx, nil, []tg.ChatClass{channel}))
	it, err := newIter(topicTitleTestPool{api: api}, manager, [][]*tmessage.Dialog{{{
		Peer: peer, Messages: []int{first.ID},
	}}}, Options{Dir: t.TempDir(), Template: documentedTopicTitleTemplate, Group: true}, 0)
	require.NoError(t, err)

	ok, skip := it.process(ctx)
	require.NoError(t, it.Err())
	require.True(t, ok)
	require.False(t, skip)
	require.Len(t, it.elem, 2)
	for _, message := range []*tg.Message{first, second} {
		file := it.Value().(*iterElem).to
		require.NoError(t, file.Close())
		require.Equal(t, filepath.Join(it.opts.Dir, "5 Album", fmt.Sprintf("42_%d_example.txt.tmp", message.ID)), file.Name())
	}
	// album paging belongs to GetGroupedMessages; it may stop before the third page.
	require.GreaterOrEqual(t, historyCalls, 2)
	require.Equal(t, 1, topicCalls)
}

type topicTitleTestPool struct {
	dcpool.Pool
	api *tg.Client
}

func (p topicTitleTestPool) Default(context.Context) *tg.Client { return p.api }

func newTopicTitleTestIter(t *testing.T, from peers.Peer, invoker tg.Invoker, nameTemplate string) *iter {
	t.Helper()
	pool := topicTitleTestPool{api: tg.NewClient(invoker)}
	it, err := newIter(pool, nil, [][]*tmessage.Dialog{{{
		Peer:     from.InputPeer(),
		Messages: []int{100},
	}}}, Options{Dir: t.TempDir(), Template: nameTemplate}, 0)
	require.NoError(t, err)
	return it
}

func topicTitleTestMessage(id, topicID int) *tg.Message {
	message := &tg.Message{ID: id}
	if topicID != 0 {
		message.SetReplyTo(&tg.MessageReplyHeader{ForumTopic: true, ReplyToMsgID: topicID})
	}
	message.SetMedia(&tg.MessageMediaDocument{Document: &tg.Document{
		Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "example.txt"}},
	}})
	return message
}
