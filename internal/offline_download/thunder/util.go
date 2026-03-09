package thunder

import (
	"context"
	"time"

	"github.com/Xhofe/go-cache"
	"github.com/alist-org/alist/v3/drivers/thunder"
	"github.com/alist-org/alist/v3/internal/op"
	"github.com/alist-org/alist/v3/pkg/singleflight"
)

var taskCache = cache.NewMemCache(cache.WithShards[[]thunder.OfflineTask](16))
var taskG singleflight.Group[[]thunder.OfflineTask]

func (t *Thunder) getTasks(thunderDriver *thunder.Thunder) ([]thunder.OfflineTask, error) {
	key := op.Key(thunderDriver, "/drive/v1/tasks")
	if !t.refreshTaskCache {
		if tasks, ok := taskCache.Get(key); ok {
			return tasks, nil
		}
	}
	t.refreshTaskCache = false
	tasks, err, _ := taskG.Do(key, func() ([]thunder.OfflineTask, error) {
		tasks, err := thunderDriver.OfflineList(context.Background(), "")
		if err != nil {
			return nil, err
		}
		if len(tasks) > 0 {
			taskCache.Set(key, tasks, cache.WithEx[[]thunder.OfflineTask](10*time.Second))
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
