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
}

func (e forwardTestElem) From() peers.Peer { return e.from }
func (e forwardTestElem) To() peers.Peer   { return e.to }
func (e forwardTestElem) Msg() *tg.Message { return e.msg }
func (e forwardTestElem) AsGrouped() bool  { return e.grouped }
func (forwardTestElem) Mode() Mode         { return ModeDirect }
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

func TestForwardGroupedLookupFailure(t *testing.T) {
	for _, tt := range []struct {
		name     string
		canceled bool
	}{
		{name: "reports failure and forwards next element"},
		{name: "stops on caller cancellation", canceled: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			lookupErr := errors.New("album history unavailable")
			calls := 0
			var forwarded []int
			next := telegram.InvokeFunc(func(ctx context.Context, input bin.Encoder, _ bin.Decoder) error {
				switch req := input.(type) {
				case *tg.MessagesGetHistoryRequest:
					calls++
					if tt.canceled {
						cancel()
						return ctx.Err()
					}
					return lookupErr
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
			grouped := &tg.Message{ID: 100}
			grouped.SetGroupedID(10)
			second := &tg.Message{ID: 101}
			it := &forwardTestIter{elems: []Elem{
				forwardTestElem{from: from, to: to, msg: grouped, grouped: true},
				forwardTestElem{from: from, to: to, msg: second},
			}}
			progress := &forwardTestProgress{errors: make(map[int]error)}
			err := New(Options{Pool: forwardTestPool{client: client}, Iter: it, Progress: progress}).Forward(ctx)
			if calls != 1 {
				t.Errorf("history calls = %d, want 1", calls)
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
			if err := progress.errors[grouped.ID]; !errors.Is(err, lookupErr) ||
				!strings.Contains(err.Error(), "resolve grouped message") || !strings.Contains(err.Error(), lookupErr.Error()) {
				t.Errorf("album progress error = %v, want grouped lookup error", err)
			}
			if !slices.Equal(forwarded, []int{second.ID}) || progress.errors[second.ID] != nil {
				t.Errorf("forwarded %v with error %v, want successful message %d", forwarded, progress.errors[second.ID], second.ID)
			}
		})
	}
}
