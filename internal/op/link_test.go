package op

import (
	"context"
	"errors"
	"sync"
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
