package dl

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fatih/color"
	"github.com/gabriel-vasile/mimetype"
	"github.com/go-faster/errors"
	pw "github.com/jedib0t/go-pretty/v6/progress"

	"github.com/iyear/tdl/core/downloader"
	"github.com/iyear/tdl/core/util/fsutil"
	"github.com/iyear/tdl/pkg/prog"
	"github.com/iyear/tdl/pkg/utils"
)

var chtimes = os.Chtimes

type progress struct {
	pw         pw.Writer
	trackers   *sync.Map // map[ID]*pw.Tracker
	downloaded sync.Map  // map[ID]int64
	opts       Options
	it         *iter
	failed     atomic.Int64
}

func newProgress(p pw.Writer, it *iter, opts Options) *progress {
	return &progress{
		pw:       p,
		trackers: &sync.Map{},
		opts:     opts,
		it:       it,
	}
}

func (p *progress) OnAdd(elem downloader.Elem) {
	tracker := prog.AppendTracker(p.pw, utils.Byte.FormatBinaryBytes, p.processMessage(elem), elem.File().Size())
	p.trackers.Store(elem.(*iterElem).id, tracker)
}

func (p *progress) OnDownload(elem downloader.Elem, state downloader.ProgressState) {
	id := elem.(*iterElem).id
	tracker, ok := p.trackers.Load(id)
	if !ok {
		return
	}

	previous, loaded := p.downloaded.LoadOrStore(id, state.Downloaded)
	for loaded && previous.(int64) < state.Downloaded {
		if p.downloaded.CompareAndSwap(id, previous, state.Downloaded) {
			break
		}
		previous, loaded = p.downloaded.Load(id)
	}
	t := tracker.(*pw.Tracker)
	t.UpdateTotal(state.Total)
	value := state.Downloaded
	if state.Total > 0 {
		value = min(value, state.Total-1)
	}
	t.SetValue(value)
}

func (p *progress) OnDone(elem downloader.Elem, err error) {
	e := elem.(*iterElem)

	tracker, ok := p.trackers.LoadAndDelete(e.id)
	if !ok {
		return
	}
	t := tracker.(*pw.Tracker)
	downloaded, _ := p.downloaded.LoadAndDelete(e.id)

	canceled := errors.Is(err, context.Canceled)
	closeErr := e.to.Close()
	if closeErr != nil {
		closeErr = errors.Wrap(closeErr, "close file")
		switch {
		case err == nil:
			p.failed.Add(1)
			err = closeErr
		case !canceled:
			err = errors.Join(err, closeErr)
		}
	}

	if err != nil {
		switch {
		case canceled && closeErr == nil:
			t.MarkAsErrored()
		case canceled:
			p.fail(t, elem, closeErr)
		default:
			p.fail(t, elem, errors.Wrap(err, "progress"))
		}
		_ = os.Remove(e.to.Name()) // just try to remove temp file, ignore error
		return
	}

	if err := p.donePost(e); err != nil {
		p.failed.Add(1)
		p.fail(t, elem, errors.Wrap(err, "post file"))
		return
	}

	p.it.Finish(e.logicalPos)
	if count, ok := downloaded.(int64); ok && count > 0 {
		t.SetValue(count)
	}
	if !t.IsDone() {
		t.MarkAsDone()
	}
}

func (p *progress) err() error {
	if n := p.failed.Load(); n > 0 {
		return errors.Errorf("%d file(s) failed after download", n)
	}
	return nil
}

func (p *progress) donePost(elem *iterElem) error {
	newfile := strings.TrimSuffix(filepath.Base(elem.to.Name()), tempExt)

	if p.opts.RewriteExt {
		mime, err := mimetype.DetectFile(elem.to.Name())
		if err != nil {
			return errors.Wrap(err, "detect mime")
		}
		ext := mime.Extension()
		if ext != "" && (filepath.Ext(newfile) != ext) {
			newfile = fsutil.GetNameWithoutExt(newfile) + ext
		}
	}

	newpath := filepath.Join(filepath.Dir(elem.to.Name()), newfile)
	if err := os.Rename(elem.to.Name(), newpath); err != nil {
		return errors.Wrap(err, "rename file")
	}

	// keep the renamed file complete even if its timestamp cannot be set
	if elem.file.Date > 0 {
		fileTime := time.Unix(elem.file.Date, 0)
		if err := chtimes(newpath, fileTime, fileTime); err != nil {
			p.pw.Log(color.YellowString("%s: set file time: %s", p.elemString(elem), err))
		}
	}

	return nil
}

func (p *progress) fail(t *pw.Tracker, elem downloader.Elem, err error) {
	p.pw.Log(color.RedString("%s error: %s", p.elemString(elem), err.Error()))
	t.MarkAsErrored()
}

func (p *progress) processMessage(elem downloader.Elem) string {
	return p.elemString(elem)
}

func (p *progress) elemString(elem downloader.Elem) string {
	e := elem.(*iterElem)
	return fmt.Sprintf("%s(%d):%d -> %s",
		e.from.VisibleName(),
		e.from.ID(),
		e.fromMsg.ID,
		strings.TrimSuffix(e.to.Name(), tempExt))
}
