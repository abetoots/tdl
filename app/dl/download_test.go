package dl

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	pw "github.com/jedib0t/go-pretty/v6/progress"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/core/dcpool"
	"github.com/iyear/tdl/core/downloader"
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
}

func (downloadTestPool) Client(context.Context, int) *tg.Client {
	return tg.NewClient(downloadTestInvoker{})
}

type downloadTestInvoker struct{}

const downloadTestContent = "test download"

func (downloadTestInvoker) Invoke(_ context.Context, input bin.Encoder, output bin.Decoder) error {
	if _, ok := input.(*tg.UploadGetFileRequest); !ok {
		return fmt.Errorf("unexpected request %T", input)
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
			elem := newProgressTestElem(t)
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
