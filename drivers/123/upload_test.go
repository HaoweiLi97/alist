package _123

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/alist-org/alist/v3/drivers/base"
)

func TestUploadS3ChunkReplaysBodyAfterURLRefresh(t *testing.T) {
	chunk := []byte("complete chunk body")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("ReadAll() error = %v", err)
		}
		if string(body) != string(chunk) {
			t.Errorf("request body = %q, want %q", body, chunk)
		}
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	oldClient := base.HttpClient
	base.HttpClient = server.Client()
	t.Cleanup(func() { base.HttpClient = oldClient })

	urls := &S3PreSignedURLs{}
	urls.Data.PreSignedUrls = map[string]string{strconv.Itoa(1): server.URL}
	var refreshes atomic.Int32
	err := (&Pan123{}).uploadS3Chunk(context.Background(), &UploadResp{}, urls, 1, 2, chunk,
		func(context.Context, *UploadResp, int, int) (*S3PreSignedURLs, error) {
			refreshes.Add(1)
			fresh := &S3PreSignedURLs{}
			fresh.Data.PreSignedUrls = map[string]string{"1": server.URL}
			return fresh, nil
		})
	if err != nil {
		t.Fatalf("uploadS3Chunk() error = %v", err)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d, want 2", requests.Load())
	}
	if refreshes.Load() != 1 {
		t.Fatalf("URL refreshes = %d, want 1", refreshes.Load())
	}
}
