package downloader

import (
	"context"
	"errors"
	"io"
	"sync"
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
	data [1 << 20]byte
}

func (e *fakeElem) File() File { return e }

func (e *fakeElem) To() io.WriterAt { return e }

func (e *fakeElem) AsTakeout() bool { return false }

func (e *fakeElem) DC() int { return 2 }

func (e *fakeElem) Size() int64 { return 1 << 20 }

func (e *fakeElem) Location() tg.InputFileLocationClass { return &tg.InputDocumentFileLocation{} }

func (e *fakeElem) WriteAt(p []byte, off int64) (int, error) {
	n := copy(e.data[off:], p)
	if n != len(p) {
		return n, io.ErrShortWrite
	}
	return n, nil
}

type fakeProgress struct {
	mu   sync.Mutex
	errs []error
}

func (*fakeProgress) OnAdd(Elem) {}

func (*fakeProgress) OnDownload(Elem, ProgressState) {}

func (p *fakeProgress) OnDone(_ Elem, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.errs = append(p.errs, err)
}

func (p *fakeProgress) doneError(t *testing.T) error {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.errs) != 1 {
		t.Fatalf("OnDone called %d times, want 1", len(p.errs))
	}
	return p.errs[0]
}

type fakePool struct {
	inv tg.Invoker
}

func (p fakePool) Client(context.Context, int) *tg.Client { return tg.NewClient(p.inv) }

func (p fakePool) Default(context.Context) *tg.Client { return tg.NewClient(p.inv) }

func (p fakePool) Takeout(context.Context, int) *tg.Client { return tg.NewClient(p.inv) }

func (fakePool) Close() error { return nil }

type errInvoker struct{}

func (errInvoker) Invoke(context.Context, bin.Encoder, bin.Decoder) error {
	return tgerr.New(400, "CONNECTION_LAYER_INVALID")
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
	case <-ctx.Done():
	}
	return errStub
}

func TestDownloadReportsFailedTransfer(t *testing.T) {
	pool := fakePool{inv: errInvoker{}}
	iter := &fakeIter{elems: []Elem{&fakeElem{}}}
	progress := &fakeProgress{}
	d := New(Options{Pool: pool, Threads: 1, Iter: iter, Progress: progress})

	if err := d.Download(t.Context(), 1); err != nil {
		t.Fatalf("Download returned %v, want nil", err)
	}
	if err := progress.doneError(t); !tgerr.Is(err, "CONNECTION_LAYER_INVALID") {
		t.Fatalf("OnDone received %v, want CONNECTION_LAYER_INVALID", err)
	}
}

func TestDownloadIterErrorWaitsForWorkers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	release := make(chan struct{})
	inv := &releaseInvoker{entered: make(chan struct{}), release: release}
	pool := fakePool{inv: inv}
	iter := &fakeIter{elems: []Elem{&fakeElem{}}, waitBeforeEnd: inv.entered, err: errIter}
	progress := &fakeProgress{}
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
