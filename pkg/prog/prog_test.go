package prog

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jedib0t/go-pretty/v6/progress"
)

type stopCountingWriter struct {
	progress.Writer
	stops atomic.Int32
}

func (w *stopCountingWriter) Stop() {
	// keep repeated calls from blocking the test before it can report them
	if w.stops.Add(1) == 1 {
		w.Writer.Stop()
	}
}

type slowProgressOutput struct{}

func (slowProgressOutput) Write(p []byte) (int, error) {
	time.Sleep(50 * time.Millisecond)
	return len(p), nil
}

func TestWaitStopsSlowRendererOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := &stopCountingWriter{Writer: progress.NewWriter()}
		w.SetAutoStop(false)
		w.SetOutputWriter(slowProgressOutput{})
		w.SetUpdateFrequency(time.Millisecond)
		tracker := &progress.Tracker{Message: "done"}
		w.AppendTracker(tracker)
		tracker.MarkAsDone()
		go w.Render()
		synctest.Wait()
		// let render consume the completed tracker and block on its output
		time.Sleep(2 * time.Millisecond)
		if w.LengthActive() != 0 || !w.IsRenderInProgress() {
			t.Error("expected the renderer to be writing with no active trackers")
		}

		Wait(t.Context(), w)
		if w.IsRenderInProgress() {
			t.Error("Wait returned before Render exited")
		}
		if n := w.stops.Load(); n != 1 {
			t.Errorf("Stop called %d times, want 1", n)
		}
	})
}

type startupWindowWriter struct {
	progress.Writer
	releaseFirst <-chan struct{}
	stops        atomic.Int32
	rendering    atomic.Bool
}

func (*startupWindowWriter) LengthActive() int { return 0 }

func (w *startupWindowWriter) IsRenderInProgress() bool { return w.rendering.Load() }

func (w *startupWindowWriter) Stop() {
	if w.stops.Add(1) == 1 {
		<-w.releaseFirst
		return
	}
	w.rendering.Store(false)
}

func TestWaitRetriesBlockedStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		releaseFirst := make(chan struct{})
		w := &startupWindowWriter{releaseFirst: releaseFirst}
		w.rendering.Store(true)
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)

		returned := make(chan struct{})
		go func() {
			Wait(ctx, w)
			close(returned)
		}()
		defer func() {
			cancel()
			close(releaseFirst) // release the blocked goroutine only for test teardown
			<-returned
		}()
		select {
		case <-returned:
		case <-ctx.Done():
			t.Fatal("Wait hung after the first Stop blocked")
		}
		if n := w.stops.Load(); n < 2 || w.IsRenderInProgress() {
			t.Errorf("Wait returned with %d stop requests and rendering = %v", n, w.IsRenderInProgress())
		}
	})
}
