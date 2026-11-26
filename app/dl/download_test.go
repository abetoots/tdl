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

	"github.com/gotd/td/bin"
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
	failures map[int]error
}

func (p downloadTestPool) Client(_ context.Context, dc int) *tg.Client {
	return tg.NewClient(downloadTestInvoker{err: p.failures[dc]})
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
			require.NoError(t, d.Download(t.Context(), 1))

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
	ctx := t.Context()
	kvd := resumeTestStorage{}
	const fingerprint = "download-outcomes"
	resumeKey := key.Resume(fingerprint)
	names := []string{"success.bin", "transfer.bin", "connection.bin", "collision.bin"}

	for _, mixed := range []bool{true, false} {
		dir := t.TempDir()
		it := &iter{
			mu: &sync.Mutex{}, finished: map[int]struct{}{}, fingerprint: fingerprint,
			elem: make(chan downloader.Elem, len(names)),
		}
		for pos, name := range names {
			elem := newProgressTestElem(t, dir, name)
			elem.id, elem.logicalPos, elem.fromMsg.ID = pos+1, pos, pos+1
			elem.file.InputFileLoc = &tg.InputDocumentFileLocation{}
			elem.file.Size = int64(len(downloadTestContent))
			elem.file.DC = pos + 2
			it.elem <- elem
		}
		w := &logWriter{Writer: pw.NewWriter()}
		p := newProgress(w, it, Options{})
		pool := downloadTestPool{}
		if mixed {
			pool.failures = map[int]error{3: io.ErrUnexpectedEOF, 4: fmt.Errorf("engine forcibly closed: %w", context.Canceled)}
			require.NoError(t, os.Mkdir(filepath.Join(dir, names[3]), 0o700))
		}
		d := downloader.New(downloader.Options{Pool: pool, Threads: 1, Iter: it, Progress: p})
		err := runDownload(ctx, d, p, len(names))

		if mixed {
			assert.ErrorContains(t, err, "2 file(s) failed to download")
			assert.ErrorContains(t, err, "1 file(s) failed after download")
			assert.ErrorContains(t, p.err(), "1 file(s) failed after download")
			assert.Equal(t, map[int]struct{}{0: {}}, it.Finished())
			require.Len(t, w.lines, 3)
			for _, name := range names[1:] {
				count := 0
				for _, line := range w.lines {
					if strings.Contains(line, name+" error: ") {
						count++
					}
				}
				assert.Equal(t, 1, count, name)
			}
			require.NoError(t, finishResume(ctx, kvd, it, err))
			saved, err := kvd.Get(ctx, resumeKey)
			require.NoError(t, err)
			assert.JSONEq(t, `{"0":{}}`, string(saved))
		} else {
			assert.NoError(t, err)
			assert.NoError(t, p.err())
			assert.Len(t, it.Finished(), len(names))
			assert.Empty(t, w.lines)
			require.NoError(t, finishResume(ctx, kvd, it, err))
			_, err := kvd.Get(ctx, resumeKey)
			assert.ErrorIs(t, err, storage.ErrNotFound)
		}
	}
}
