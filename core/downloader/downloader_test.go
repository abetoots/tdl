package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

var (
	errIter = errors.New("iterator failed")
	errStub = errors.New("transfer failed")
)

type fakeIter struct {
	elems         []Elem
	next          int
	waitBeforeEnd <-chan struct{}
	recordCancel  bool
	err           error
}

func (i *fakeIter) Next(ctx context.Context) bool {
	if i.next < len(i.elems) {
		i.next++
		return true
	}
	if i.waitBeforeEnd != nil {
		select {
		case <-i.waitBeforeEnd:
		case <-ctx.Done():
			if i.recordCancel {
				i.err = ctx.Err()
			}
		}
	}
	return false
}

func (i *fakeIter) Value() Elem { return i.elems[i.next-1] }

func (i *fakeIter) Err() error { return i.err }

type fakeElem struct {
	data    [1 << 20]byte
	dc      int
	takeout bool
}

func (e *fakeElem) File() File { return e }

func (e *fakeElem) To() io.WriterAt { return e }

func (e *fakeElem) AsTakeout() bool { return e.takeout }

func (e *fakeElem) DC() int { return e.dc }

func (e *fakeElem) Size() int64 { return 1 << 20 }

func (e *fakeElem) Location() tg.InputFileLocationClass { return &tg.InputDocumentFileLocation{} }

func (e *fakeElem) WriteAt(p []byte, off int64) (int, error) {
	n := copy(e.data[off:], p)
	if n != len(p) {
		return n, io.ErrShortWrite
	}
	return n, nil
}

type doneResult struct {
	elem Elem
	err  error
}

type fakeProgress struct {
	done  chan doneResult
	extra atomic.Int32
}

func newFakeProgress(t *testing.T, expected int) *fakeProgress {
	t.Helper()
	p := &fakeProgress{done: make(chan doneResult, expected)}
	t.Cleanup(func() {
		if extra := p.extra.Load(); extra != 0 {
			t.Errorf("OnDone called %d extra times", extra)
		}
	})
	return p
}

func (*fakeProgress) OnAdd(Elem) {}

func (*fakeProgress) OnDownload(Elem, ProgressState) {}

func (p *fakeProgress) OnDone(elem Elem, err error) {
	select {
	case p.done <- doneResult{elem: elem, err: err}:
	default:
		p.extra.Add(1)
	}
}

func (p *fakeProgress) doneError(t *testing.T) error {
	t.Helper()
	if len(p.done) != 1 {
		t.Fatalf("OnDone called %d times, want 1", len(p.done))
	}
	return (<-p.done).err
}

type fakePool struct {
	clients  map[int]tg.Invoker
	takeouts map[int]tg.Invoker
}

func (p fakePool) Client(_ context.Context, dc int) *tg.Client { return tg.NewClient(p.clients[dc]) }

func (p fakePool) Default(ctx context.Context) *tg.Client { return p.Client(ctx, 2) }

func (p fakePool) Takeout(_ context.Context, dc int) *tg.Client {
	return tg.NewClient(p.takeouts[dc])
}

func (fakePool) Close() error { return nil }

type errInvoker struct{}

func (errInvoker) Invoke(context.Context, bin.Encoder, bin.Decoder) error {
	return tgerr.New(400, "CONNECTION_LAYER_INVALID")
}

type invokerFunc func(context.Context, bin.Encoder, bin.Decoder) error

func (f invokerFunc) Invoke(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
	return f(ctx, in, out)
}

type successInvoker struct{}

func (successInvoker) Invoke(_ context.Context, input bin.Encoder, out bin.Decoder) error {
	if _, ok := input.(*tg.UploadGetFileRequest); !ok {
		return fmt.Errorf("unexpected request %T", input)
	}
	var buf bin.Buffer
	if err := (&tg.UploadFile{Type: &tg.StorageFileUnknown{}, Bytes: []byte{'x'}}).Encode(&buf); err != nil {
		return err
	}
	return out.Decode(&buf)
}

type releaseInvoker struct {
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (i *releaseInvoker) Invoke(ctx context.Context, _ bin.Encoder, _ bin.Decoder) error {
	i.once.Do(func() { close(i.entered) })
	select {
	case <-i.release:
		return errStub
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestDownloadAllSucceed(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	elems := []Elem{&fakeElem{dc: 2}, &fakeElem{dc: 3}}
	progress := newFakeProgress(t, len(elems))
	d := New(Options{
		Pool:    fakePool{clients: map[int]tg.Invoker{2: successInvoker{}, 3: successInvoker{}}},
		Threads: 1, Iter: &fakeIter{elems: elems}, Progress: progress,
	})
	if err := d.Download(ctx, 2); err != nil {
		t.Fatalf("Download returned %v, want nil", err)
	}
	if len(progress.done) != len(elems) {
		t.Fatalf("OnDone called %d times, want %d", len(progress.done), len(elems))
	}
	for range elems {
		done := <-progress.done
		if done.err != nil || done.elem.(*fakeElem).data[0] != 'x' {
			t.Errorf("DC %d did not write the downloaded byte, OnDone error = %v", done.elem.File().DC(), done.err)
		}
	}
}

func TestDownloadReportsTwoFailedTransfers(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	elems := []Elem{&fakeElem{dc: 2}, &fakeElem{dc: 3}}
	progress := newFakeProgress(t, len(elems))
	d := New(Options{
		Pool:    fakePool{clients: map[int]tg.Invoker{2: errInvoker{}, 3: errInvoker{}}},
		Threads: 1, Iter: &fakeIter{elems: elems}, Progress: progress,
	})
	if err := d.Download(ctx, 2); err == nil || err.Error() != "2 file(s) failed to download" {
		t.Errorf("Download returned %v, want 2 file(s) failed to download", err)
	}
	if len(progress.done) != len(elems) {
		t.Fatalf("OnDone called %d times, want %d", len(progress.done), len(elems))
	}
	for range elems {
		if done := <-progress.done; !tgerr.Is(done.err, "CONNECTION_LAYER_INVALID") {
			t.Errorf("OnDone received %v, want CONNECTION_LAYER_INVALID", done.err)
		}
	}
}

func TestDownloadRoutesTakeoutToDC(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	var calls atomic.Int32
	inv := invokerFunc(func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		calls.Add(1)
		return successInvoker{}.Invoke(ctx, input, output)
	})
	progress := newFakeProgress(t, 1)
	d := New(Options{
		Pool:    fakePool{clients: map[int]tg.Invoker{4: errInvoker{}}, takeouts: map[int]tg.Invoker{4: inv}},
		Threads: 1, Iter: &fakeIter{elems: []Elem{&fakeElem{dc: 4, takeout: true}}}, Progress: progress,
	})
	if err := d.Download(ctx, 1); err != nil {
		t.Fatalf("Download returned %v, want nil", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("DC 4 takeout invoker called %d times, want 1", got)
	}
	if err := progress.doneError(t); err != nil {
		t.Errorf("OnDone received %v, want nil", err)
	}
}

func TestDownloadReportsFailedTransfer(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	pool := fakePool{clients: map[int]tg.Invoker{2: errInvoker{}}}
	iter := &fakeIter{elems: []Elem{&fakeElem{dc: 2}}}
	progress := newFakeProgress(t, 1)
	d := New(Options{Pool: pool, Threads: 1, Iter: iter, Progress: progress})

	if err := d.Download(ctx, 1); err == nil || err.Error() != "1 file(s) failed to download" {
		t.Errorf("Download returned %v, want 1 file(s) failed to download", err)
	}
	if err := progress.doneError(t); !tgerr.Is(err, "CONNECTION_LAYER_INVALID") {
		t.Fatalf("OnDone received %v, want CONNECTION_LAYER_INVALID", err)
	}
}

func TestDownloadDeadConnectionIsAFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	failed, sibling, third := &fakeElem{dc: 2}, &fakeElem{dc: 3}, &fakeElem{dc: 4}
	entered, release := make(chan struct{}), make(chan struct{})
	var enteredOnce sync.Once
	pool := fakePool{clients: map[int]tg.Invoker{
		2: invokerFunc(func(ctx context.Context, _ bin.Encoder, _ bin.Decoder) error {
			select {
			case <-entered:
				return fmt.Errorf("engine forcibly closed: %w", context.Canceled)
			case <-ctx.Done():
				return ctx.Err()
			}
		}),
		3: invokerFunc(func(ctx context.Context, _ bin.Encoder, out bin.Decoder) error {
			enteredOnce.Do(func() { close(entered) })
			select {
			case <-release:
				if err := ctx.Err(); err != nil {
					return err
				}
				var buf bin.Buffer
				if err := (&tg.UploadFile{Type: &tg.StorageFileUnknown{}}).Encode(&buf); err != nil {
					return err
				}
				return out.Decode(&buf)
			case <-ctx.Done():
				return ctx.Err()
			}
		}),
		4: successInvoker{},
	}}
	progress := newFakeProgress(t, 3)
	d := New(Options{Pool: pool, Threads: 1, Iter: &fakeIter{elems: []Elem{failed, sibling, third}}, Progress: progress})
	result := make(chan error, 1)
	go func() { result <- d.Download(ctx, 2) }()

	select {
	case done := <-progress.done:
		if done.elem != failed || done.err == nil || errors.Is(done.err, context.Canceled) ||
			!strings.Contains(done.err.Error(), "connection lost") {
			t.Errorf("first OnDone was for DC %d with %v, want connection loss for DC 2", done.elem.File().DC(), done.err)
		}
	case <-ctx.Done():
		t.Fatal("failed transfer did not report completion")
	}
	close(release)
	select {
	case err := <-result:
		if err == nil || err.Error() != "1 file(s) failed to download" {
			t.Errorf("Download returned %v, want 1 file(s) failed to download", err)
		}
	case <-ctx.Done():
		t.Fatal("Download did not return after releasing sibling")
	}
	if len(progress.done) != 2 {
		t.Fatalf("OnDone called %d more times, want 2", len(progress.done))
	}
	want := map[Elem]bool{sibling: true, third: true}
	for range 2 {
		done := <-progress.done
		if !want[done.elem] || done.err != nil {
			t.Errorf("unexpected OnDone for DC %d with %v", done.elem.File().DC(), done.err)
		}
		want[done.elem] = false
	}
	if third.data[0] != 'x' {
		t.Error("third element did not write the downloaded byte")
	}
}

func TestDownloadUserCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	inv := &releaseInvoker{entered: make(chan struct{})}
	progress := newFakeProgress(t, 1)
	d := New(Options{
		Pool: fakePool{clients: map[int]tg.Invoker{2: inv}}, Threads: 1,
		Iter: &fakeIter{elems: []Elem{&fakeElem{dc: 2}}}, Progress: progress,
	})
	result := make(chan error, 1)
	go func() { result <- d.Download(ctx, 1) }()

	select {
	case <-inv.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("worker never started")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Download returned %v, want context.Canceled", err)
		}
		doneErr := progress.doneError(t)
		if !errors.Is(doneErr, context.Canceled) {
			t.Fatalf("OnDone received %v, want context.Canceled", doneErr)
		}
		if err != ctx.Err() {
			t.Errorf("Download returned %v, want the caller's context error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Download did not return after cancel")
	}
}

type blockedCompletionProgress struct {
	*fakeProgress
	entered chan struct{}
	release chan struct{}
}

func (p *blockedCompletionProgress) OnDone(elem Elem, err error) {
	close(p.entered)
	<-p.release
	p.fakeProgress.OnDone(elem, err)
}

func TestDownloadUserCancelDuringJoinAfterSuccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		progress := &blockedCompletionProgress{
			fakeProgress: newFakeProgress(t, 1),
			entered:      make(chan struct{}),
			release:      make(chan struct{}),
		}
		d := New(Options{
			Pool: fakePool{clients: map[int]tg.Invoker{2: successInvoker{}}}, Threads: 1,
			Iter: &fakeIter{elems: []Elem{&fakeElem{dc: 2}}}, Progress: progress,
		})
		result := make(chan error, 1)
		go func() { result <- d.Download(ctx, 1) }()

		<-progress.entered
		synctest.Wait()
		cancel()
		close(progress.release)

		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Errorf("Download returned %v, want context.Canceled", err)
		}
		if err := progress.doneError(t); err != nil {
			t.Errorf("OnDone received %v, want worker success", err)
		}
	})
}

func TestDownloadUserCancelStopsSiblingsAndWins(t *testing.T) {
	for _, test := range []struct {
		name         string
		recordCancel bool
	}{
		{name: "worker cancellation"},
		{name: "iterator records cancellation", recordCancel: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			failed, first, second := &fakeElem{dc: 2}, &fakeElem{dc: 3}, &fakeElem{dc: 4}
			a := &releaseInvoker{entered: make(chan struct{})}
			b := &releaseInvoker{entered: make(chan struct{})}
			pool := fakePool{clients: map[int]tg.Invoker{2: errInvoker{}, 3: a, 4: b}}
			iter := &fakeIter{elems: []Elem{failed, first, second}, recordCancel: test.recordCancel}
			if test.recordCancel {
				iter.waitBeforeEnd = make(chan struct{})
			}
			progress := newFakeProgress(t, 3)
			d := New(Options{Pool: pool, Threads: 1, Iter: iter, Progress: progress})
			result := make(chan error, 1)
			go func() { result <- d.Download(ctx, 3) }()

			deadline := time.After(2 * time.Second)
			for _, entered := range []<-chan struct{}{a.entered, b.entered} {
				select {
				case <-entered:
				case <-deadline:
					t.Fatal("sibling never entered its RPC")
				}
			}
			select {
			case done := <-progress.done:
				if done.elem != failed || !tgerr.Is(done.err, "CONNECTION_LAYER_INVALID") {
					t.Fatalf("first OnDone was for DC %d with %v, want CONNECTION_LAYER_INVALID for DC 2", done.elem.File().DC(), done.err)
				}
			case <-deadline:
				t.Fatal("failed transfer did not report completion")
			}
			cancel()

			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "failed to download") {
					t.Errorf("Download returned %v, want cancellation without a failure count", err)
				}
			case <-deadline:
				t.Fatal("Download did not return after cancel")
			}
			if test.recordCancel && !errors.Is(iter.Err(), context.Canceled) {
				t.Errorf("iterator returned %v, want context.Canceled", iter.Err())
			}
			if len(progress.done) != 2 {
				t.Fatalf("OnDone called %d more times, want 2", len(progress.done))
			}
			want := map[Elem]bool{first: true, second: true}
			for range 2 {
				done := <-progress.done
				if !want[done.elem] || !errors.Is(done.err, context.Canceled) || strings.Contains(done.err.Error(), "connection lost") {
					t.Errorf("unexpected OnDone for DC %d with %v", done.elem.File().DC(), done.err)
				}
				want[done.elem] = false
			}
		})
	}
}

func TestDownloadIterErrorWaitsForWorkers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	release := make(chan struct{})
	inv := &releaseInvoker{entered: make(chan struct{}), release: release}
	pool := fakePool{clients: map[int]tg.Invoker{2: inv}}
	iter := &fakeIter{elems: []Elem{&fakeElem{dc: 2}}, waitBeforeEnd: inv.entered, err: errIter}
	progress := newFakeProgress(t, 1)
	d := New(Options{Pool: pool, Threads: 1, Iter: iter, Progress: progress})
	result := make(chan error, 1)
	go func() { result <- d.Download(ctx, 1) }()

	select {
	case <-inv.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("worker never started")
	}
	select {
	case <-result:
		t.Fatal("Download returned before its worker finished")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-result:
		if !errors.Is(err, errIter) {
			t.Fatalf("Download returned %v, want %v", err, errIter)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Download did not return after its worker finished")
	}
	if err := progress.doneError(t); !errors.Is(err, errStub) {
		t.Fatalf("OnDone received %v, want %v", err, errStub)
	}
}
