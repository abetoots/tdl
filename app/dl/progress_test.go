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
	mu    sync.Mutex
	lines []string
}

func (w *logWriter) Log(msg string, args ...any) {
	if len(args) != 0 {
		msg = fmt.Sprintf(msg, args...)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lines = append(w.lines, msg)
}

func setupProgressTest(t *testing.T, w pw.Writer) (*progress, *iter, *iterElem, *pw.Tracker) {
	t.Helper()
	it := &iter{mu: &sync.Mutex{}, finished: map[int]struct{}{}}
	p := newProgress(w, it, Options{})
	elem := newProgressTestElem(t, t.TempDir(), "a.bin")
	p.OnAdd(elem)
	tracker, ok := p.trackers.Load(elem.id)
	require.True(t, ok)
	return p, it, elem, tracker.(*pw.Tracker)
}

func newProgressTestElem(t *testing.T, dir, name string) *iterElem {
	t.Helper()
	to, err := os.Create(filepath.Join(dir, name+tempExt))
	require.NoError(t, err)
	t.Cleanup(func() { _ = to.Close() })

	return &iterElem{
		id:         1,
		logicalPos: 7,
		to:         to,
		file:       &tmedia.Media{Name: name, Size: 1 << 20},
		fromMsg:    &tg.Message{ID: 1},
		from:       fakePeer{},
	}
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
	assert.Equal(t, elem.file.Size, tracker.Value())
	assert.Equal(t, elem.file.Size, tracker.Total)
	assert.Contains(t, it.Finished(), 7)
	st, err := os.Stat(filepath.Join(filepath.Dir(elem.to.Name()), "a.bin"))
	require.NoError(t, err)
	assert.Equal(t, elem.file.Date, st.ModTime().Unix())
	assert.NoFileExists(t, elem.to.Name())
}

func TestOnDoneKeepsLargestDownloadedCount(t *testing.T) {
	p, _, elem, tracker := setupProgressTest(t, pw.NewWriter())
	size := elem.file.Size
	p.OnDownload(elem, downloader.ProgressState{Downloaded: size, Total: size})
	p.OnDownload(elem, downloader.ProgressState{Downloaded: size / 2, Total: size})
	require.False(t, tracker.IsDone())

	p.OnDone(elem, nil)
	assert.True(t, tracker.IsDone())
	assert.Equal(t, size, tracker.Value())
	assert.Equal(t, size, tracker.Total)
}

func TestOnDoneIgnoresDuplicate(t *testing.T) {
	w := &logWriter{Writer: pw.NewWriter()}
	p, _, elem, tracker := setupProgressTest(t, w)
	p.OnDone(elem, nil)
	p.OnDone(elem, nil)

	assert.NoError(t, p.err())
	assert.False(t, tracker.IsErrored())
	assert.Empty(t, w.lines)
}

func TestOnDownloadUnknownSize(t *testing.T) {
	p, _, elem, tracker := setupProgressTest(t, pw.NewWriter())
	p.OnDownload(elem, downloader.ProgressState{Downloaded: 5, Total: 0})

	assert.EqualValues(t, 5, tracker.Value())
	assert.False(t, tracker.IsDone())
}

func TestOnDoneSuccessWithOneByte(t *testing.T) {
	p, _, elem, tracker := setupProgressTest(t, pw.NewWriter())
	elem.file.Size = 1
	p.OnDownload(elem, downloader.ProgressState{Downloaded: 1, Total: 1})
	assert.EqualValues(t, 0, tracker.Value())
	assert.False(t, tracker.IsDone())
	p.OnDone(elem, nil)

	assert.True(t, tracker.IsDone())
	assert.False(t, tracker.IsErrored())
	assert.EqualValues(t, 1, tracker.Value())
	assert.EqualValues(t, 1, tracker.Total)
}

func TestOnDoneSuccessUsesDownloadedBytes(t *testing.T) {
	p, _, elem, tracker := setupProgressTest(t, pw.NewWriter())
	p.OnDownload(elem, downloader.ProgressState{Downloaded: 5, Total: elem.file.Size})
	p.OnDone(elem, nil)

	assert.True(t, tracker.IsDone())
	assert.EqualValues(t, 5, tracker.Value())
	assert.EqualValues(t, 5, tracker.Total)
}

func TestOnDoneFailureAfterAllBytesStillErrors(t *testing.T) {
	p, it, elem, tracker := setupProgressTest(t, pw.NewWriter())
	p.OnDownload(elem, downloader.ProgressState{Downloaded: elem.file.Size, Total: elem.file.Size})
	p.OnDone(elem, errors.New("boom"))

	assert.True(t, tracker.IsDone())
	assert.True(t, tracker.IsErrored())
	assert.NotContains(t, it.Finished(), 7)
	assert.NoFileExists(t, elem.to.Name())
	assert.NoError(t, p.err())
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
	assert.ErrorContains(t, p.err(), "1 file(s) failed after download")
	require.Len(t, w.lines, 1)
	assert.Contains(t, w.lines[0], "rename file")
}

func TestOnDoneCloseError(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "close only"},
		{name: "transfer and close", err: io.ErrUnexpectedEOF},
		{name: "cancel and close", err: fmt.Errorf("download interrupted: %w", context.Canceled)},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := &logWriter{Writer: pw.NewWriter()}
			p, it, elem, tracker := setupProgressTest(t, w)
			require.NoError(t, elem.to.Close())
			p.OnDone(elem, test.err)

			assert.True(t, tracker.IsErrored())
			assert.NotContains(t, it.Finished(), 7)
			assert.NoFileExists(t, filepath.Join(filepath.Dir(elem.to.Name()), "a.bin"))
			assert.NoFileExists(t, elem.to.Name())
			require.Len(t, w.lines, 1)
			assert.Contains(t, w.lines[0], "close file")
			switch test.err {
			case nil:
				assert.ErrorContains(t, p.err(), "1 file(s) failed after download")
			case io.ErrUnexpectedEOF:
				assert.Contains(t, w.lines[0], test.err.Error())
				assert.NoError(t, p.err())
			default:
				assert.Contains(t, w.lines[0], "error: close file")
				assert.NotContains(t, w.lines[0], test.err.Error())
				assert.NotContains(t, w.lines[0], context.Canceled.Error())
				assert.NoError(t, p.err())
			}
		})
	}
}

func TestOnDoneChtimesFailureStillFinishes(t *testing.T) {
	original := chtimes
	t.Cleanup(func() { chtimes = original })
	chtimes = func(string, time.Time, time.Time) error { return errors.New("timestamps unavailable") }

	w := &logWriter{Writer: pw.NewWriter()}
	p, it, elem, tracker := setupProgressTest(t, w)
	elem.file.Date = 1_700_000_000
	p.OnDone(elem, nil)

	assert.True(t, tracker.IsDone())
	assert.False(t, tracker.IsErrored())
	assert.Contains(t, it.Finished(), 7)
	assert.NoError(t, p.err())
	assert.FileExists(t, filepath.Join(filepath.Dir(elem.to.Name()), elem.file.Name))
	assert.NoFileExists(t, elem.to.Name())
	require.Len(t, w.lines, 1)
	assert.Contains(t, w.lines[0], "set file time")
	assert.NotContains(t, w.lines[0], " error: ")
}

type renderSignal chan struct{}

func (r renderSignal) Write(p []byte) (int, error) {
	select {
	case r <- struct{}{}:
	default:
	}
	return len(p), nil
}

func TestOnDoneCancelLetsProgressWaitReturn(t *testing.T) {
	w := prog.New(utils.Byte.FormatBinaryBytes)
	rendered := make(renderSignal, 1)
	w.SetOutputWriter(rendered)
	p, _, elem, _ := setupProgressTest(t, w)
	go w.Render()
	select {
	case <-rendered:
	case <-time.After(2 * time.Second):
		t.Fatal("progress did not render")
	}
	t.Cleanup(w.Stop)

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
