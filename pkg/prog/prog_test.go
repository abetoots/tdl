package prog

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/jedib0t/go-pretty/v6/progress"
)

type stopCountingWriter struct {
	progress.Writer
	stops int
}

func (w *stopCountingWriter) Stop() {
	w.stops++
	// keep repeated calls from blocking the test before it can report them
	if w.stops == 1 {
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
		if w.stops != 1 {
			t.Errorf("Stop called %d times, want 1", w.stops)
		}
	})
}
