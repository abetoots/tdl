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

type fakeProgress chan doneResult

func (fakeProgress) OnAdd(Elem) {}

func (fakeProgress) OnDownload(Elem, ProgressState) {}

func (p fakeProgress) OnDone(elem Elem, err error) {
	p <- doneResult{elem: elem, err: err}
}

func (p fakeProgress) doneError(t *testing.T) error {
	t.Helper()
	if len(p) != 1 {
		t.Fatalf("OnDone called %d times, want 1", len(p))
	}
	return (<-p).err
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
	progress := make(fakeProgress, len(elems))
	d := New(Options{
		Pool:    fakePool{clients: map[int]tg.Invoker{2: successInvoker{}, 3: successInvoker{}}},
		Threads: 1, Iter: &fakeIter{elems: elems}, Progress: progress,
	})
	if err := d.Download(ctx, 2); err != nil {
		t.Fatalf("Download returned %v, want nil", err)
	}
	if len(progress) != len(elems) {
		t.Fatalf("OnDone called %d times, want %d", len(progress), len(elems))
	}
	for range elems {
		done := <-progress
		if done.err != nil || done.elem.(*fakeElem).data[0] != 'x' {
			t.Errorf("DC %d did not write the downloaded byte, OnDone error = %v", done.elem.File().DC(), done.err)
		}
	}
}

func TestDownloadReportsTwoFailedTransfers(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	elems := []Elem{&fakeElem{dc: 2}, &fakeElem{dc: 3}}
	progress := make(fakeProgress, len(elems))
	d := New(Options{
		Pool:    fakePool{clients: map[int]tg.Invoker{2: errInvoker{}, 3: errInvoker{}}},
		Threads: 1, Iter: &fakeIter{elems: elems}, Progress: progress,
	})
	if err := d.Download(ctx, 2); err == nil || err.Error() != "2 file(s) failed to download" {
		t.Errorf("Download returned %v, want 2 file(s) failed to download", err)
	}
	if len(progress) != len(elems) {
		t.Fatalf("OnDone called %d times, want %d", len(progress), len(elems))
	}
	for range elems {
		if done := <-progress; !tgerr.Is(done.err, "CONNECTION_LAYER_INVALID") {
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
	progress := make(fakeProgress, 1)
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
	progress := make(fakeProgress, 2)
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

	failed, sibling := &fakeElem{dc: 2}, &fakeElem{dc: 3}
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
	}}
	progress := make(fakeProgress, 3)
	d := New(Options{Pool: pool, Threads: 1, Iter: &fakeIter{elems: []Elem{failed, sibling}}, Progress: progress})
	result := make(chan error, 1)
	go func() { result <- d.Download(ctx, 2) }()

	select {
	case done := <-progress:
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
	if len(progress) != 1 {
		t.Fatalf("OnDone called %d more times, want 1", len(progress))
	}
	if done := <-progress; done.elem != sibling || done.err != nil {
		t.Errorf("second OnDone was for DC %d with %v, want success for DC 3", done.elem.File().DC(), done.err)
	}
}

func TestDownloadUserCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	inv := &releaseInvoker{entered: make(chan struct{})}
	progress := make(fakeProgress, 2)
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
		if !strings.HasPrefix(err.Error(), "download: ") || errors.Unwrap(err) != doneErr {
			t.Errorf("Download returned %v, want the OnDone error wrapped with download", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Download did not return after cancel")
	}
}

func TestDownloadIterErrorWaitsForWorkers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	release := make(chan struct{})
	inv := &releaseInvoker{entered: make(chan struct{}), release: release}
	pool := fakePool{clients: map[int]tg.Invoker{2: inv}}
	iter := &fakeIter{elems: []Elem{&fakeElem{dc: 2}}, waitBeforeEnd: inv.entered, err: errIter}
	progress := make(fakeProgress, 2)
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
