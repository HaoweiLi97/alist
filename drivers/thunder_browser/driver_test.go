package thunder_browser

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alist-org/alist/v3/internal/model"
	"github.com/go-resty/resty/v2"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestDirectLinkExpiration(t *testing.T) {
	now := time.Now()

	t.Run("query timestamp", func(t *testing.T) {
		expiresAt := now.Add(5 * time.Minute).Unix()
		ttl, ok := directLinkExpiration("https://download.example/file?e="+strconv.FormatInt(expiresAt, 10), time.Time{}, now)
		if !ok {
			t.Fatal("directLinkExpiration() did not parse query expiry")
		}
		want := 5*time.Minute - directLinkCacheSafetyMargin
		if delta := ttl - want; delta < -time.Second || delta > time.Second {
			t.Fatalf("ttl = %v, want about %v", ttl, want)
		}
	})

	t.Run("payload expiry takes precedence", func(t *testing.T) {
		expiresAt := now.Add(2 * time.Minute)
		ttl, ok := directLinkExpiration("https://download.example/file?e=1", expiresAt, now)
		if !ok {
			t.Fatal("directLinkExpiration() did not use payload expiry")
		}
		want := 2*time.Minute - directLinkCacheSafetyMargin
		if delta := ttl - want; delta < -time.Second || delta > time.Second {
			t.Fatalf("ttl = %v, want about %v", ttl, want)
		}
	})

	t.Run("expired or near-expiry link", func(t *testing.T) {
		if _, ok := directLinkExpiration("https://download.example/file?e="+strconv.FormatInt(now.Add(20*time.Second).Unix(), 10), time.Time{}, now); ok {
			t.Fatal("directLinkExpiration() cached a near-expiry link")
		}
	})
}

func TestHasRepeatedThunderRoot(t *testing.T) {
	if !hasRepeatedThunderRoot("/迅雷云盘/迅雷云盘") {
		t.Fatal("duplicate Thunder root was not detected")
	}
	if !hasRepeatedThunderRoot("/迅雷云盘/迅雷云盘/Downloads") {
		t.Fatal("nested duplicate Thunder root was not detected")
	}
	if hasRepeatedThunderRoot("/迅雷云盘/Downloads") {
		t.Fatal("valid Thunder path was treated as a duplicate root")
	}
}

func TestDirectoryFilesKeySeparatesVirtualAndMainRoots(t *testing.T) {
	xc := &XunLeiBrowserCommon{}
	virtualRoot := &model.Object{}
	mainRoot := &Files{Kind: FOLDER}
	if got, want := xc.directoryFilesKey(virtualRoot), xc.directoryFilesKey(mainRoot); got == want {
		t.Fatalf("directory cache keys collide: %q", got)
	}
}

func TestGetFilesSharesConcurrentDirectoryRequests(t *testing.T) {
	var requests atomic.Int32
	client := resty.New().SetTransport(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		requests.Add(1)
		if got := req.URL.Query().Get("with"); got != "url" {
			t.Errorf("directory request with = %q, want url", got)
		}
		time.Sleep(50 * time.Millisecond)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{
				"files":[{"id":"child","name":"video.mp4","kind":"drive#file","space":"SPACE_BROWSER"}]
			}`)),
		}, nil
	}))
	xc := &XunLeiBrowserCommon{
		Common:    &Common{client: client},
		TokenResp: &TokenResp{},
	}
	dir := &Files{ID: "parent", Space: ThunderBrowserDriveSpace, Kind: FOLDER}

	start := make(chan struct{})
	errs := make(chan error, 3)
	for range 3 {
		go func() {
			<-start
			files, err := xc.getFiles(context.Background(), dir, false)
			if err == nil && (len(files) != 1 || files[0].GetName() != "video.mp4") {
				err = io.ErrUnexpectedEOF
			}
			errs <- err
		}()
	}
	close(start)
	for range 3 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("upstream directory requests = %d, want 1", got)
	}

	if _, err := xc.getFiles(context.Background(), dir, false); err != nil {
		t.Fatal(err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("cached directory requests = %d, want 1", got)
	}

	if _, err := xc.getFiles(context.Background(), dir, true); err != nil {
		t.Fatal(err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("refreshed directory requests = %d, want 2", got)
	}
}

func TestGetFilesFiltersUnexpectedNestedDriveRoot(t *testing.T) {
	client := resty.New().SetTransport(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{
				"files":[
					{"name":"迅雷云盘","kind":"drive#folder","folder_type":"DEFAULT_ROOT"},
					{"id":"duplicate","name":"迅雷云盘","kind":"drive#folder","space":"SPACE_BROWSER","folder_type":"NORMAL"},
					{"name":"漫画","kind":"drive#folder","folder_type":"DEFAULT_ROOT"},
					{"id":"child","name":"video.mp4","kind":"drive#file","space":"SPACE_BROWSER"}
				]
			}`)),
		}, nil
	}))
	xc := &XunLeiBrowserCommon{Common: &Common{client: client}, TokenResp: &TokenResp{}}

	nestedFiles, err := xc.getFilesUncached(context.Background(), &Files{Kind: FOLDER})
	if err != nil {
		t.Fatal(err)
	}
	if len(nestedFiles) != 2 || nestedFiles[0].GetName() != "漫画" || nestedFiles[1].GetName() != "video.mp4" {
		t.Fatalf("nested files = %#v, want 漫画 and video.mp4", nestedFiles)
	}

	rootFiles, err := xc.getFilesUncached(context.Background(), &model.Object{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rootFiles) != 3 || rootFiles[0].GetName() != "迅雷云盘" {
		t.Fatalf("root files = %#v, want root entry retained", rootFiles)
	}
}

func TestGetByIDDoesNotRequestDirectURL(t *testing.T) {
	client := resty.New().SetTransport(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.URL.Query().Get("with"); got != "" {
			t.Errorf("getByID unexpectedly requested a direct URL: with=%q", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{
				"id":"folder","name":"Hanime","kind":"drive#folder"
			}`)),
		}, nil
	}))
	xc := &XunLeiBrowserCommon{Common: &Common{client: client}, TokenResp: &TokenResp{}}
	obj, err := xc.getByID(context.Background(), "folder", ThunderDriveSpace)
	if err != nil {
		t.Fatal(err)
	}
	if obj.GetName() != "Hanime" || !obj.IsDir() {
		t.Fatalf("getByID result = %#v, want Hanime folder", obj)
	}
}

func TestListCachesObjectsForSubsequentGet(t *testing.T) {
	var requests atomic.Int32
	client := resty.New().SetTransport(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{
				"files":[{"id":"file","name":"video.mp4","kind":"drive#file","space":"SPACE_BROWSER"}]
			}`)),
		}, nil
	}))
	xc := &XunLeiBrowserCommon{Common: &Common{client: client}, TokenResp: &TokenResp{}}
	if _, err := xc.List(context.Background(), &model.Object{}, model.ListArgs{ReqPath: "/folder"}); err != nil {
		t.Fatal(err)
	}
	if _, err := xc.Get(context.Background(), "/folder/video.mp4"); err != nil {
		t.Fatal(err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("upstream requests after listing then getting = %d, want 1", got)
	}
}

func TestGetUsesStaleListedObjectBeforeByID(t *testing.T) {
	var requests atomic.Int32
	client := resty.New().SetTransport(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, io.ErrUnexpectedEOF
	}))
	xc := &XunLeiBrowserCommon{Common: &Common{client: client}, TokenResp: &TokenResp{}}
	path := "/folder/video.mp4"
	obj := &Files{ID: "file", Name: "video.mp4", Kind: FILE, Space: ThunderBrowserDriveSpace}
	xc.cachePathRef(path, pathRef{ID: obj.ID, Space: obj.Space, IsDir: false})
	xc.getObjCache.Store(path, cachedObj{
		obj:            obj,
		expiresAt:      time.Now().Add(-time.Second).UnixNano(),
		staleExpiresAt: time.Now().Add(time.Minute).UnixNano(),
	})

	got, err := xc.Get(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if got != obj {
		t.Fatalf("Get() = %#v, want cached object %#v", got, obj)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("upstream requests = %d, want 0", got)
	}
}

func TestCommonRequestResetsClientAfterTransportTimeout(t *testing.T) {
	originalClient := resty.New().SetTransport(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	}))
	common := &Common{
		client:        originalClient,
		clientFactory: resty.New,
	}
	t.Cleanup(common.Close)

	if _, err := common.Request("https://example.invalid", http.MethodGet, nil, nil); err == nil {
		t.Fatal("Request() error = nil, want transport timeout")
	}
	currentClient, generation, _, err := common.requestState()
	if err != nil {
		t.Fatal(err)
	}
	if currentClient == originalClient {
		t.Fatal("transport timeout did not replace the HTTP client")
	}
	if generation != 1 {
		t.Fatalf("client generation = %d, want 1", generation)
	}
}

func TestCommonRequestDoesNotResetClientForCallerCancellation(t *testing.T) {
	originalClient := resty.New().SetTransport(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return nil, req.Context().Err()
	}))
	common := &Common{
		client:        originalClient,
		clientFactory: resty.New,
	}
	t.Cleanup(common.Close)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := common.Request("https://example.invalid", http.MethodGet, func(req *resty.Request) {
		req.SetContext(ctx)
	}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Request() error = %v, want context canceled", err)
	}
	currentClient, generation, _, err := common.requestState()
	if err != nil {
		t.Fatal(err)
	}
	if currentClient != originalClient {
		t.Fatal("caller cancellation unexpectedly replaced the HTTP client")
	}
	if generation != 0 {
		t.Fatalf("client generation = %d, want 0", generation)
	}
}

func TestCommonCloseCancelsActiveRequest(t *testing.T) {
	started := make(chan struct{})
	client := resty.New().SetTransport(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		close(started)
		<-req.Context().Done()
		return nil, req.Context().Err()
	}))
	common := &Common{client: client}
	result := make(chan error, 1)
	go func() {
		_, err := common.Request("https://example.invalid", http.MethodGet, nil, nil)
		result <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request did not start")
	}
	common.Close()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Request() error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close() did not cancel the active request")
	}
	if _, err := common.Request("https://example.invalid", http.MethodGet, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Request() after Close() error = %v, want context canceled", err)
	}
}

func TestResetClientOnlyOncePerGeneration(t *testing.T) {
	common := &Common{client: resty.New(), clientFactory: resty.New}
	t.Cleanup(common.Close)
	const workers = 16
	start := make(chan struct{})
	results := make(chan bool, workers)
	for range workers {
		go func() {
			<-start
			results <- common.resetClientIfCurrent(0)
		}()
	}
	close(start)
	resets := 0
	for range workers {
		if <-results {
			resets++
		}
	}
	if resets != 1 {
		t.Fatalf("client resets = %d, want 1", resets)
	}
}
