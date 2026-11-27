package forwarder

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"

	"github.com/iyear/tdl/core/dcpool"
)

type forwardTestPool struct {
	dcpool.Pool
	client *tg.Client
}

func (p forwardTestPool) Default(context.Context) *tg.Client { return p.client }

type forwardTestIter struct {
	elems []Elem
	pos   int
}

func (it *forwardTestIter) Next(context.Context) bool {
	it.pos++
	return it.pos <= len(it.elems)
}

func (it *forwardTestIter) Value() Elem { return it.elems[it.pos-1] }
func (*forwardTestIter) Err() error     { return nil }

type forwardTestElem struct {
	from, to peers.Peer
	msg      *tg.Message
	grouped  bool
	mode     Mode
}

func (e forwardTestElem) From() peers.Peer { return e.from }
func (e forwardTestElem) To() peers.Peer   { return e.to }
func (e forwardTestElem) Msg() *tg.Message { return e.msg }
func (e forwardTestElem) AsGrouped() bool  { return e.grouped }
func (e forwardTestElem) Mode() Mode       { return e.mode }
func (forwardTestElem) Thread() int        { return 0 }
func (forwardTestElem) AsSilent() bool     { return false }
func (forwardTestElem) AsDryRun() bool     { return false }

type forwardTestProgress struct {
	events []string
	errors map[int]error
}

func (p *forwardTestProgress) OnAdd(e Elem) {
	p.events = append(p.events, fmt.Sprintf("add %d", e.Msg().ID))
}

func (p *forwardTestProgress) OnDone(e Elem, err error) {
	p.events = append(p.events, fmt.Sprintf("done %d", e.Msg().ID))
	p.errors[e.Msg().ID] = err
}

func (*forwardTestProgress) OnClone(Elem, ProgressState) {}

func TestForwardMessageFailure(t *testing.T) {
	plain := errors.New("request failed")
	for _, tt := range []struct {
		name         string
		canceled     bool
		grouped      bool
		albumForward bool
		failure      error
	}{
		{name: "album lookup failure", grouped: true, failure: errors.New("album history unavailable")},
		{name: "album connection lost", grouped: true, failure: fmt.Errorf("album history RPC: %w", context.Canceled)},
		{name: "album canceled by caller", grouped: true, canceled: true, failure: context.Canceled},
		{name: "caller cancels during plain album lookup failure", grouped: true, canceled: true, failure: plain},
		{name: "caller cancels during plain album forward failure", grouped: true, albumForward: true, canceled: true, failure: plain},
		{name: "single message connection lost", failure: fmt.Errorf("send message RPC: %w", context.Canceled)},
		{name: "single message canceled by caller", canceled: true, failure: context.Canceled},
		{name: "caller cancels during plain single message failure", canceled: true, failure: plain},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failures := 0
			fail := func() error {
				failures++
				if tt.canceled {
					cancel()
				}
				return tt.failure
			}
			first := forwardTestElem{msg: &tg.Message{ID: 100, Message: "first"}, grouped: tt.grouped}
			if tt.grouped {
				first.msg.SetGroupedID(10)
			}
			if !tt.grouped || tt.albumForward {
				first.mode = ModeClone
			}
			var album []tg.MessageClass
			if tt.albumForward {
				peer := &tg.PeerUser{UserID: 1}
				first.msg.PeerID = peer
				first.msg.SetMedia(&tg.MessageMediaDocument{Document: &tg.Document{ID: 1, Size: 10}})
				previous := &tg.Message{ID: first.msg.ID - 1, PeerID: peer}
				previous.SetGroupedID(10)
				previous.SetMedia(&tg.MessageMediaDocument{Document: &tg.Document{ID: 2, Size: 10}})
				album = []tg.MessageClass{first.msg, previous}
			}
			var forwarded []int
			next := telegram.InvokeFunc(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
				switch req := input.(type) {
				case *tg.MessagesGetHistoryRequest:
					if !tt.albumForward {
						return fail()
					}
					result := &tg.MessagesMessages{}
					if req.OffsetID > first.msg.ID {
						result.Messages = album
					}
					output.(*tg.MessagesMessagesBox).Messages = result
					return nil
				case *tg.MessagesSendMessageRequest:
					if tt.albumForward {
						t.Error("sent a single message, want an album")
					}
					return fail()
				case *tg.MessagesSendMultiMediaRequest:
					if !tt.albumForward || len(req.MultiMedia) != len(album) {
						t.Errorf("sent %d media items, want a two-item album", len(req.MultiMedia))
					}
					return fail()
				case *tg.MessagesForwardMessagesRequest:
					forwarded = append(forwarded, req.ID...)
					return nil
				default:
					return fmt.Errorf("unexpected request %T", input)
				}
			})
			client := tg.NewClient(next)
			manager := (peers.Options{}).Build(client)
			from, to := manager.User(&tg.User{ID: 1}), manager.User(&tg.User{ID: 2})
			first.from, first.to = from, to
			second := &tg.Message{ID: 101}
			it := &forwardTestIter{elems: []Elem{
				first,
				forwardTestElem{from: from, to: to, msg: second},
			}}
			progress := &forwardTestProgress{errors: make(map[int]error)}
			err := New(Options{Pool: forwardTestPool{client: client}, Iter: it, Progress: progress}).Forward(ctx)
			if failures != 1 {
				t.Errorf("failed requests = %d, want 1", failures)
			}
			if tt.canceled {
				if !errors.Is(err, context.Canceled) || len(forwarded) != 0 {
					t.Errorf("Forward error = %v, forwarded %v, want caller cancellation and no forwarded messages", err, forwarded)
				}
				return
			}
			if err != nil {
				t.Errorf("Forward error = %v, want nil", err)
			}
			if want := []string{"add 100", "done 100", "add 101", "done 101"}; !slices.Equal(progress.events, want) {
				t.Errorf("progress events = %v, want %v", progress.events, want)
			}
			if err := progress.errors[first.msg.ID]; !errors.Is(err, tt.failure) || !strings.Contains(err.Error(), tt.failure.Error()) ||
				tt.grouped && !strings.Contains(err.Error(), "resolve grouped message") {
				t.Errorf("progress error = %v, want context and text for %v", err, tt.failure)
			}
			if !slices.Equal(forwarded, []int{second.ID}) || progress.errors[second.ID] != nil {
				t.Errorf("forwarded %v with error %v, want successful message %d", forwarded, progress.errors[second.ID], second.ID)
			}
		})
	}
}
