package dl

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	pw "github.com/jedib0t/go-pretty/v6/progress"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/core/dcpool"
	"github.com/iyear/tdl/core/downloader"
	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/pkg/key"
)

type singleDownload struct {
	elem *iterElem
	next bool
}

func (i *singleDownload) Next(context.Context) bool {
	if i.next {
		return false
	}
	i.next = true
	return true
}

func (i *singleDownload) Value() downloader.Elem { return i.elem }

func (*singleDownload) Err() error { return nil }

type downloadTestPool struct {
	dcpool.Pool
	invokers map[int]tg.Invoker
}

func (p downloadTestPool) Client(_ context.Context, dc int) *tg.Client {
	if invoker := p.invokers[dc]; invoker != nil {
		return tg.NewClient(invoker)
	}
	return tg.NewClient(downloadTestInvoker{})
}

type downloadTestInvoker struct{ err error }

const downloadTestContent = "test download"

func (i downloadTestInvoker) Invoke(_ context.Context, input bin.Encoder, output bin.Decoder) error {
	if _, ok := input.(*tg.UploadGetFileRequest); !ok {
		return fmt.Errorf("unexpected request %T", input)
	}
	if i.err != nil {
		return i.err
	}
	var buf bin.Buffer
	if err := (&tg.UploadFile{Type: &tg.StorageFileUnknown{}, Bytes: []byte(downloadTestContent)}).Encode(&buf); err != nil {
		return err
	}
	return output.Decode(&buf)
}

func TestDownloadReportsPostTransferFailure(t *testing.T) {
	for _, test := range []struct {
		name      string
		collision bool
	}{
		{name: "rename fails", collision: true},
		{name: "rename succeeds"},
	} {
		t.Run(test.name, func(t *testing.T) {
			elem := newProgressTestElem(t, t.TempDir(), "a.bin")
			elem.file.InputFileLoc = &tg.InputDocumentFileLocation{}
			elem.file.Size = int64(len(downloadTestContent))
			elem.file.DC = 2
			dest := filepath.Join(filepath.Dir(elem.to.Name()), elem.file.Name)
			if test.collision {
				require.NoError(t, os.Mkdir(dest, 0o700))
			}

			it := &iter{mu: &sync.Mutex{}, finished: map[int]struct{}{}}
			p := newProgress(pw.NewWriter(), it, Options{})
			d := downloader.New(downloader.Options{
				Pool: downloadTestPool{}, Threads: 1, Iter: &singleDownload{elem: elem}, Progress: p,
			})
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			require.NoError(t, d.Download(ctx, 1))

			if test.collision {
				assert.ErrorContains(t, p.err(), "1 file(s) failed after download")
				assert.NotContains(t, it.Finished(), 7)
				dest = elem.to.Name()
			} else {
				assert.NoError(t, p.err())
				assert.Contains(t, it.Finished(), 7)
				assert.NoFileExists(t, elem.to.Name())
			}
			content, err := os.ReadFile(dest)
			require.NoError(t, err)
			assert.Equal(t, downloadTestContent, string(content))
		})
	}
}

type resumeTestStorage map[string][]byte

func (s resumeTestStorage) Get(_ context.Context, key string) ([]byte, error) {
	b, ok := s[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return b, nil
}

func (s resumeTestStorage) Set(_ context.Context, key string, b []byte) error {
	s[key] = b
	return nil
}

func (s resumeTestStorage) Delete(_ context.Context, key string) error {
	delete(s, key)
	return nil
}

func TestRunDownloadOutcomes(t *testing.T) {
	const fingerprint = "download-outcomes"
	const collision = "collision.bin"
	const postFailure = "1 file(s) failed after download"
	resumeKey := key.Resume(fingerprint)
	for _, test := range []struct {
		name       string
		files      []string
		invokers   map[int]tg.Invoker
		iterErr    error
		cancel     bool
		wantErrors []string
		wantLogs   []string
		wantSaved  string
	}{
		{
			name:  "mixed failures",
			files: []string{"success.bin", "transfer.bin", "connection.bin", collision},
			invokers: map[int]tg.Invoker{
				3: downloadTestInvoker{err: io.ErrUnexpectedEOF},
				4: downloadTestInvoker{err: fmt.Errorf("engine forcibly closed: %w", context.Canceled)},
			},
			wantErrors: []string{"2 file(s) failed to download", postFailure},
			wantLogs:   []string{"transfer.bin", "connection.bin", collision},
			wantSaved:  `{"0":{}}`,
		},
		{name: "success", files: []string{"first.bin", "second.bin"}},
		{
			name:       "post-transfer failure only",
			files:      []string{collision},
			wantErrors: []string{postFailure},
			wantLogs:   []string{collision},
			wantSaved:  `{}`,
		},
		{
			name:      "user cancel",
			files:     []string{"success.bin", collision, "blocked.bin"},
			cancel:    true,
			wantLogs:  []string{collision},
			wantSaved: `{"0":{}}`,
		},
		{
			name:       "iterator cancellation with live run",
			files:      []string{collision},
			iterErr:    fmt.Errorf("resolve message: %w", context.Canceled),
			wantErrors: []string{"iter: resolve message: context canceled", postFailure},
			wantLogs:   []string{collision},
			wantSaved:  `{}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancelTimeout := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancelTimeout()
			kvd := resumeTestStorage{resumeKey: []byte(`{"9":{}}`)}
			dir := t.TempDir()
			it := &iter{
				mu: &sync.Mutex{}, finished: map[int]struct{}{}, fingerprint: fingerprint,
				elem: make(chan downloader.Elem, len(test.files)), err: test.iterErr,
			}
			for pos, name := range test.files {
				elem := newProgressTestElem(t, dir, name)
				elem.id, elem.logicalPos, elem.fromMsg.ID = pos+1, pos, pos+1
				elem.file.InputFileLoc = &tg.InputDocumentFileLocation{}
				elem.file.Size = int64(len(downloadTestContent))
				elem.file.DC = pos + 2
				it.elem <- elem
				if name == collision {
					require.NoError(t, os.Mkdir(filepath.Join(dir, name), 0o700))
				}
			}
			pool := downloadTestPool{invokers: test.invokers}
			if test.cancel {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
				blocked := make(chan struct{})
				pool.invokers = map[int]tg.Invoker{4: telegram.InvokeFunc(func(ctx context.Context, _ bin.Encoder, _ bin.Decoder) error {
					close(blocked)
					<-ctx.Done()
					return ctx.Err()
				})}
				go func() {
					select {
					case <-blocked:
						cancel()
					case <-ctx.Done():
					}
				}()
			}
			w := &logWriter{Writer: pw.NewWriter()}
			p := newProgress(w, it, Options{})
			d := downloader.New(downloader.Options{Pool: pool, Threads: 1, Iter: it, Progress: p})
			// one worker ensures the rename has finished before the blocked transfer starts
			err := runDownload(ctx, d, p, 1)
			if test.iterErr != nil {
				assert.NotErrorIs(t, err, context.Canceled)
				assert.ErrorContains(t, err, "connection lost")
			}

			switch {
			case test.cancel:
				require.ErrorIs(t, err, context.Canceled)
				assert.NotContains(t, err.Error(), "failed after download")
				assert.EqualError(t, p.err(), postFailure)
			case len(test.wantErrors) == 0:
				assert.NoError(t, err)
				assert.NoError(t, p.err())
				assert.Len(t, it.Finished(), len(test.files))
			case len(test.wantErrors) == 1:
				assert.EqualError(t, err, test.wantErrors[0])
			default:
				for _, want := range test.wantErrors {
					assert.ErrorContains(t, err, want)
				}
			}
			require.Len(t, w.lines, len(test.wantLogs))
			for _, name := range test.wantLogs {
				count := 0
				for _, line := range w.lines {
					if strings.Contains(line, name+" error: ") {
						count++
					}
				}
				assert.Equal(t, 1, count, name)
			}
			require.NoError(t, finishResume(ctx, kvd, it, err))
			saved, getErr := kvd.Get(ctx, resumeKey)
			if test.wantSaved == "" {
				assert.ErrorIs(t, getErr, storage.ErrNotFound)
			} else {
				require.NoError(t, getErr)
				assert.JSONEq(t, test.wantSaved, string(saved))
			}
		})
	}
}
