package thunderx

import (
	"context"
	"time"

	"github.com/Xhofe/go-cache"
	"github.com/alist-org/alist/v3/drivers/thunderx"
	"github.com/alist-org/alist/v3/internal/op"
	"github.com/alist-org/alist/v3/pkg/singleflight"
)

var taskCache = cache.NewMemCache(cache.WithShards[[]thunderx.OfflineTask](16))
var taskG singleflight.Group[[]thunderx.OfflineTask]

func (t *ThunderX) getTasks(driver *thunderx.ThunderX) ([]thunderx.OfflineTask, error) {
	key := op.Key(driver, "/drive/v1/tasks")
	if !t.refreshTaskCache {
		if tasks, ok := taskCache.Get(key); ok {
			return tasks, nil
		}
	}
	t.refreshTaskCache = false
	tasks, err, _ := taskG.Do(key, func() ([]thunderx.OfflineTask, error) {
		tasks, err := driver.OfflineList(context.Background(), "", nil)
		if err != nil {
			return nil, err
		}
		if len(tasks) > 0 {
			taskCache.Set(key, tasks, cache.WithEx[[]thunderx.OfflineTask](10*time.Second))
		} else {
			taskCache.Del(key)
		}
		return tasks, nil
	})
	if err != nil {
		return nil, err
	}
	return tasks, nil
}
