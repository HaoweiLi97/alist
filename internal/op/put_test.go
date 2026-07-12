package op

import (
	"bytes"
	"context"
	"errors"
	"path"
	"testing"

	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/errs"
	"github.com/alist-org/alist/v3/internal/model"
	"github.com/alist-org/alist/v3/internal/stream"
)

type overwriteTestDriver struct {
	model.Storage
	root    driver.RootID
	objects map[string]*model.Object
	putErr  error
}

func newOverwriteTestDriver(dirIsFolder bool, putErr error) *overwriteTestDriver {
	return &overwriteTestDriver{
		Storage: model.Storage{MountPath: "/test", CacheExpiration: 1},
		objects: map[string]*model.Object{
			"/":             {ID: "root", Path: "/", Name: "root", IsFolder: true},
			"/dir":          {ID: "dir", Path: "/dir", Name: "dir", IsFolder: dirIsFolder},
			"/dir/file.txt": {ID: "old", Path: "/dir/file.txt", Name: "file.txt", Size: 10},
		},
		putErr: putErr,
	}
}

func (d *overwriteTestDriver) Config() driver.Config {
	return driver.Config{Name: "overwrite-test", NoCache: true, NoOverwriteUpload: true}
}
func (d *overwriteTestDriver) GetAddition() driver.Additional { return &d.root }
func (d *overwriteTestDriver) Init(context.Context) error     { return nil }
func (d *overwriteTestDriver) Drop(context.Context) error     { return nil }
func (d *overwriteTestDriver) Link(context.Context, model.Obj, model.LinkArgs) (*model.Link, error) {
	return nil, errs.NotImplement
}
func (d *overwriteTestDriver) Get(_ context.Context, p string) (model.Obj, error) {
	if obj, ok := d.objects[p]; ok {
		return obj, nil
	}
	return nil, errs.ObjectNotFound
}
func (d *overwriteTestDriver) List(_ context.Context, dir model.Obj, _ model.ListArgs) ([]model.Obj, error) {
	var result []model.Obj
	for p, obj := range d.objects {
		if p != dir.GetPath() && path.Dir(p) == dir.GetPath() {
			result = append(result, obj)
		}
	}
	return result, nil
}
func (d *overwriteTestDriver) Rename(_ context.Context, src model.Obj, newName string) error {
	oldPath := src.GetPath()
	obj, ok := d.objects[oldPath]
	if !ok {
		return errs.ObjectNotFound
	}
	newPath := path.Join(path.Dir(oldPath), newName)
	delete(d.objects, oldPath)
	obj.Name = newName
	obj.Path = newPath
	d.objects[newPath] = obj
	return nil
}
func (d *overwriteTestDriver) Remove(_ context.Context, obj model.Obj) error {
	delete(d.objects, obj.GetPath())
	return nil
}
func (d *overwriteTestDriver) Put(_ context.Context, dstDir model.Obj, file model.FileStreamer, _ driver.UpdateProgress) error {
	p := path.Join(dstDir.GetPath(), file.GetName())
	d.objects[p] = &model.Object{ID: "partial", Path: p, Name: file.GetName()}
	return d.putErr
}

func newOverwriteTestStream() model.FileStreamer {
	return &stream.FileStream{
		Obj:    &model.Object{Name: "file.txt", Size: 3},
		Reader: bytes.NewReader([]byte("new")),
	}
}

func assertOriginalRestored(t *testing.T, d *overwriteTestDriver) {
	t.Helper()
	obj, ok := d.objects["/dir/file.txt"]
	if !ok || obj.ID != "old" || obj.Size != 10 {
		t.Fatalf("original file was not restored: %#v", obj)
	}
	if _, ok := d.objects["/dir/file.txt.alist_to_delete"]; ok {
		t.Fatal("overwrite backup was not cleaned up")
	}
}

func TestPutRestoresOverwriteAfterDriverFailure(t *testing.T) {
	wantErr := errors.New("upload failed")
	d := newOverwriteTestDriver(true, wantErr)
	err := Put(context.Background(), d, "/dir", newOverwriteTestStream(), nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Put() error = %v, want %v", err, wantErr)
	}
	assertOriginalRestored(t, d)
}

func TestPutRestoresOverwriteAfterEarlyFailure(t *testing.T) {
	d := newOverwriteTestDriver(false, nil)
	if err := Put(context.Background(), d, "/dir", newOverwriteTestStream(), nil); err == nil {
		t.Fatal("Put() error = nil, want directory validation error")
	}
	assertOriginalRestored(t, d)
}
