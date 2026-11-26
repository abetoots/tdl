package dl

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	pw "github.com/jedib0t/go-pretty/v6/progress"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/core/downloader"
	"github.com/iyear/tdl/core/tmedia"
)

type fakePeer struct {
	peers.Peer
}

func (fakePeer) ID() int64 { return 1 }

func (fakePeer) VisibleName() string { return "test peer" }

func setupProgressTest(t *testing.T) (*progress, *iter, *iterElem, *pw.Tracker) {
	t.Helper()
	it := &iter{mu: &sync.Mutex{}, finished: map[int]struct{}{}}
	p := newProgress(pw.NewWriter(), it, Options{})
	to, err := os.Create(filepath.Join(t.TempDir(), "a.bin.tmp"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = to.Close() })

	elem := &iterElem{
		id:         1,
		logicalPos: 7,
		to:         to,
		file:       &tmedia.Media{Name: "a.bin", Size: 1 << 20},
		fromMsg:    &tg.Message{ID: 1},
		from:       fakePeer{},
	}
	p.OnAdd(elem)
	tracker, ok := p.trackers.Load(1)
	require.True(t, ok)
	return p, it, elem, tracker.(*pw.Tracker)
}

func TestOnDoneClosesBarOnError(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "canceled", err: context.Canceled},
		{name: "failed", err: errors.New("boom")},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, it, elem, tracker := setupProgressTest(t)
			p.OnDone(elem, test.err)

			assert.True(t, tracker.IsDone(), "tracker should be done")
			assert.True(t, tracker.IsErrored(), "tracker should be errored")
			assert.NotContains(t, it.Finished(), 7)
			assert.NoFileExists(t, elem.to.Name())
		})
	}
}

func TestOnDoneSuccessClosesBar(t *testing.T) {
	p, it, elem, tracker := setupProgressTest(t)
	p.OnDone(elem, nil)

	assert.True(t, tracker.IsDone(), "tracker should be done")
	assert.False(t, tracker.IsErrored(), "tracker should not be errored")
	assert.Contains(t, it.Finished(), 7)
	assert.FileExists(t, filepath.Join(filepath.Dir(elem.to.Name()), "a.bin"))
}

func TestOnDoneRenameFailureNotFinished(t *testing.T) {
	p, it, elem, tracker := setupProgressTest(t)
	require.NoError(t, os.Mkdir(filepath.Join(filepath.Dir(elem.to.Name()), "a.bin"), 0o700))
	p.OnDownload(elem, downloader.ProgressState{Downloaded: 1 << 20, Total: 1 << 20})
	require.True(t, tracker.IsDone(), "tracker should be full before post-processing")
	p.OnDone(elem, nil)

	assert.NotContains(t, it.Finished(), 7)
}
