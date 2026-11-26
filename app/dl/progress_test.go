package dl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	pw "github.com/jedib0t/go-pretty/v6/progress"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/core/downloader"
	"github.com/iyear/tdl/core/tmedia"
	"github.com/iyear/tdl/pkg/prog"
	"github.com/iyear/tdl/pkg/utils"
)

type fakePeer struct {
	peers.Peer
}

func (fakePeer) ID() int64 { return 1 }

func (fakePeer) VisibleName() string { return "test peer" }

type logWriter struct {
	pw.Writer
	lines []string
}

func (w *logWriter) Log(msg string, args ...any) {
	if len(args) != 0 {
		msg = fmt.Sprintf(msg, args...)
	}
	w.lines = append(w.lines, msg)
}

func setupProgressTest(t *testing.T, w pw.Writer) (*progress, *iter, *iterElem, *pw.Tracker) {
	t.Helper()
	it := &iter{mu: &sync.Mutex{}, finished: map[int]struct{}{}}
	p := newProgress(w, it, Options{})
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
		log  string
	}{
		{name: "canceled", err: fmt.Errorf("download: %w", context.Canceled)},
		{name: "failed", err: fmt.Errorf("download: %w", io.ErrUnexpectedEOF), log: "unexpected EOF"},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := &logWriter{Writer: pw.NewWriter()}
			p, it, elem, tracker := setupProgressTest(t, w)
			p.OnDone(elem, test.err)

			assert.True(t, tracker.IsDone(), "tracker should be done")
			assert.True(t, tracker.IsErrored(), "tracker should be errored")
			assert.NotContains(t, it.Finished(), 7)
			assert.NoFileExists(t, elem.to.Name())
			if test.log == "" {
				assert.Empty(t, w.lines)
			} else {
				require.Len(t, w.lines, 1)
				assert.Contains(t, w.lines[0], test.log)
			}
		})
	}
}

func TestOnDoneSuccessClosesBar(t *testing.T) {
	p, it, elem, tracker := setupProgressTest(t, pw.NewWriter())
	elem.file.Date = 1_700_000_000
	p.OnDownload(elem, downloader.ProgressState{Downloaded: elem.file.Size, Total: elem.file.Size})
	assert.False(t, tracker.IsDone(), "tracker should stay open until OnDone")
	p.OnDone(elem, nil)

	assert.True(t, tracker.IsDone(), "tracker should be done")
	assert.False(t, tracker.IsErrored(), "tracker should not be errored")
	assert.Contains(t, it.Finished(), 7)
	st, err := os.Stat(filepath.Join(filepath.Dir(elem.to.Name()), "a.bin"))
	require.NoError(t, err)
	assert.Equal(t, elem.file.Date, st.ModTime().Unix())
	assert.NoFileExists(t, elem.to.Name())
}

func TestOnDoneFailureClosesFullBar(t *testing.T) {
	p, _, elem, tracker := setupProgressTest(t, pw.NewWriter())
	p.OnDownload(elem, downloader.ProgressState{Downloaded: elem.file.Size, Total: elem.file.Size})
	p.OnDone(elem, errors.New("boom"))

	assert.True(t, tracker.IsDone())
	assert.True(t, tracker.IsErrored())
}

func TestOnDoneRenameFailureNotFinished(t *testing.T) {
	w := &logWriter{Writer: pw.NewWriter()}
	p, it, elem, tracker := setupProgressTest(t, w)
	require.NoError(t, os.Mkdir(filepath.Join(filepath.Dir(elem.to.Name()), "a.bin"), 0o700))
	p.OnDownload(elem, downloader.ProgressState{Downloaded: elem.file.Size, Total: elem.file.Size})
	p.OnDone(elem, nil)

	assert.True(t, tracker.IsErrored())
	assert.NotContains(t, it.Finished(), 7)
	assert.FileExists(t, elem.to.Name())
	require.Len(t, w.lines, 1)
	assert.Contains(t, w.lines[0], "rename file")
}

func TestOnDoneCloseError(t *testing.T) {
	for _, transferErr := range []error{nil, io.ErrUnexpectedEOF} {
		t.Run(fmt.Sprint(transferErr), func(t *testing.T) {
			w := &logWriter{Writer: pw.NewWriter()}
			p, it, elem, tracker := setupProgressTest(t, w)
			require.NoError(t, elem.to.Close())
			p.OnDone(elem, transferErr)

			assert.True(t, tracker.IsErrored())
			assert.NotContains(t, it.Finished(), 7)
			assert.NoFileExists(t, filepath.Join(filepath.Dir(elem.to.Name()), "a.bin"))
			assert.NoFileExists(t, elem.to.Name())
			require.Len(t, w.lines, 1)
			assert.Contains(t, w.lines[0], "close file")
			if transferErr != nil {
				assert.Contains(t, w.lines[0], transferErr.Error())
			}
		})
	}
}

func TestOnDoneCancelLetsProgressWaitReturn(t *testing.T) {
	w := prog.New(utils.Byte.FormatBinaryBytes)
	w.SetOutputWriter(io.Discard)
	go w.Render()
	t.Cleanup(w.Stop)
	require.Eventually(t, w.IsRenderInProgress, 2*time.Second, time.Millisecond)

	p, _, elem, _ := setupProgressTest(t, w)
	p.OnDone(elem, fmt.Errorf("download: %w", context.Canceled))

	done := make(chan struct{})
	go func() {
		prog.Wait(context.Background(), w)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("prog.Wait did not return after cancel")
	}
}
