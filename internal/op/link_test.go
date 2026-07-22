package op

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/model"
)

type slowLinkDriver struct {
	model.Storage
	root    driver.RootID
	file    model.Obj
	started chan struct{}
	once    sync.Once
}

type objectLinkDriver struct {
	model.Storage
	root  driver.RootID
	gets  atomic.Int32
	links atomic.Int32
}

func (d *objectLinkDriver) Config() driver.Config {
	return driver.Config{Name: "object-link", OnlyLocal: true}
}
func (d *objectLinkDriver) GetAddition() driver.Additional { return &d.root }
func (d *objectLinkDriver) Init(context.Context) error     { return nil }
func (d *objectLinkDriver) Drop(context.Context) error     { return nil }
func (d *objectLinkDriver) Get(context.Context, string) (model.Obj, error) {
	d.gets.Add(1)
	return &model.Object{ID: "file", Name: "file"}, nil
}
func (d *objectLinkDriver) List(context.Context, model.Obj, model.ListArgs) ([]model.Obj, error) {
	return nil, nil
}
func (d *objectLinkDriver) Link(context.Context, model.Obj, model.LinkArgs) (*model.Link, error) {
	d.links.Add(1)
	return &model.Link{URL: "https://example.com/file"}, nil
}

func TestLinkWithObjSkipsObjectLookup(t *testing.T) {
	d := &objectLinkDriver{Storage: model.Storage{MountPath: "/object-link-test"}}
	file := &model.Object{ID: "file", Name: "file"}
	link, err := LinkWithObj(context.Background(), d, "/file", file, model.LinkArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if link.URL == "" {
		t.Fatal("LinkWithObj returned an empty URL")
	}
	if got := d.gets.Load(); got != 0 {
		t.Fatalf("Get calls = %d, want 0", got)
	}
	if got := d.links.Load(); got != 1 {
		t.Fatalf("Link calls = %d, want 1", got)
	}
}

func newSlowLinkDriver(mountPath string) *slowLinkDriver {
	return &slowLinkDriver{
		Storage: model.Storage{MountPath: mountPath},
		file:    &model.Object{ID: "file", Path: "/file", Name: "file"},
		started: make(chan struct{}),
	}
}

func (d *slowLinkDriver) Config() driver.Config {
	return driver.Config{Name: "slow-link", NoCache: true}
}
func (d *slowLinkDriver) GetAddition() driver.Additional                 { return &d.root }
func (d *slowLinkDriver) Init(context.Context) error                     { return nil }
func (d *slowLinkDriver) Drop(context.Context) error                     { return nil }
func (d *slowLinkDriver) Get(context.Context, string) (model.Obj, error) { return d.file, nil }
func (d *slowLinkDriver) List(context.Context, model.Obj, model.ListArgs) ([]model.Obj, error) {
	return nil, nil
}
func (d *slowLinkDriver) Link(ctx context.Context, _ model.Obj, _ model.LinkArgs) (*model.Link, error) {
	d.once.Do(func() { close(d.started) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestLinkAppliesTotalTimeoutToSharedCallAndWaiters(t *testing.T) {
	oldTimeout := linkTimeout
	linkTimeout = 40 * time.Millisecond
	t.Cleanup(func() { linkTimeout = oldTimeout })

	d := newSlowLinkDriver("/link-timeout-test")
	firstDone := make(chan error, 1)
	go func() {
		_, _, err := Link(context.Background(), d, "/file", model.LinkArgs{})
		firstDone <- err
	}()

	select {
	case <-d.started:
	case <-time.After(time.Second):
		t.Fatal("shared link call did not start")
	}

	start := time.Now()
	_, _, err := Link(context.Background(), d, "/file", model.LinkArgs{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting Link() error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 150*time.Millisecond {
		t.Fatalf("waiting Link() took %v, want bounded wait", elapsed)
	}

	if err := <-firstDone; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shared Link() error = %v, want context deadline exceeded", err)
	}
}

type parallelLinkDriver struct {
	model.Storage
	root    driver.RootID
	file    model.Obj
	started chan struct{}
	release chan struct{}
	active  atomic.Int32
	max     atomic.Int32
}

func newParallelLinkDriver(mountPath string) *parallelLinkDriver {
	return &parallelLinkDriver{
		Storage: model.Storage{MountPath: mountPath},
		file:    &model.Object{ID: "file", Path: "/file", Name: "file"},
		started: make(chan struct{}, maxConcurrentLinkResolutions),
		release: make(chan struct{}),
	}
}

func (d *parallelLinkDriver) Config() driver.Config {
	return driver.Config{Name: "parallel-link", NoCache: true}
}
func (d *parallelLinkDriver) GetAddition() driver.Additional {
	return &d.root
}
func (d *parallelLinkDriver) Init(context.Context) error { return nil }
func (d *parallelLinkDriver) Drop(context.Context) error { return nil }
func (d *parallelLinkDriver) Get(context.Context, string) (model.Obj, error) {
	return d.file, nil
}
func (d *parallelLinkDriver) List(context.Context, model.Obj, model.ListArgs) ([]model.Obj, error) {
	return nil, nil
}
func (d *parallelLinkDriver) Link(context.Context, model.Obj, model.LinkArgs) (*model.Link, error) {
	active := d.active.Add(1)
	defer d.active.Add(-1)
	for {
		maximum := d.max.Load()
		if active <= maximum || d.max.CompareAndSwap(maximum, active) {
			break
		}
	}
	d.started <- struct{}{}
	<-d.release
	return &model.Link{URL: "https://example.com/file"}, nil
}

func TestLinkLimitsParallelResolutionsPerPath(t *testing.T) {
	d := newParallelLinkDriver("/parallel-link-test")
	errs := make(chan error, maxConcurrentLinkResolutions+2)
	for range cap(errs) {
		go func() {
			_, _, err := Link(context.Background(), d, "/file", model.LinkArgs{})
			errs <- err
		}()
	}

	for range maxConcurrentLinkResolutions {
		select {
		case <-d.started:
		case <-time.After(time.Second):
			t.Fatal("did not start the allowed number of parallel resolutions")
		}
	}
	if got := d.max.Load(); got != maxConcurrentLinkResolutions {
		t.Fatalf("maximum parallel resolutions = %d, want %d", got, maxConcurrentLinkResolutions)
	}

	close(d.release)
	for range cap(errs) {
		if err := <-errs; err != nil {
			t.Fatalf("Link() error = %v, want nil", err)
		}
	}
}
