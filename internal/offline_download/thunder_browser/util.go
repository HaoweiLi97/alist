package thunder_browser

import (
	"context"
	"time"

	"github.com/Xhofe/go-cache"
	"github.com/alist-org/alist/v3/drivers/thunder_browser"
	"github.com/alist-org/alist/v3/internal/op"
	"github.com/alist-org/alist/v3/pkg/singleflight"
)

var taskCache = cache.NewMemCache(cache.WithShards[[]thunder_browser.OfflineTask](16))
var taskG singleflight.Group[[]thunder_browser.OfflineTask]

func (t *ThunderBrowser) getTasks(thunderDriver *thunder_browser.ThunderBrowser) ([]thunder_browser.OfflineTask, error) {
	key := op.Key(thunderDriver, "/drive/v1/tasks")
	if !t.refreshTaskCache {
		if tasks, ok := taskCache.Get(key); ok {
			return tasks, nil
		}
	}
	t.refreshTaskCache = false
	tasks, err, _ := taskG.Do(key, func() ([]thunder_browser.OfflineTask, error) {
		tasks, err := thunderDriver.OfflineList(context.Background(), "")
		if err != nil {
			return nil, err
		}
		if len(tasks) > 0 {
			taskCache.Set(key, tasks, cache.WithEx[[]thunder_browser.OfflineTask](10*time.Second))
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

func (t *ThunderBrowser) getTasksExpert(thunderDriver *thunder_browser.ThunderBrowserExpert) ([]thunder_browser.OfflineTask, error) {
	key := op.Key(thunderDriver, "/drive/v1/tasks")
	if !t.refreshTaskCache {
		if tasks, ok := taskCache.Get(key); ok {
			return tasks, nil
		}
	}
	t.refreshTaskCache = false
	tasks, err, _ := taskG.Do(key, func() ([]thunder_browser.OfflineTask, error) {
		tasks, err := thunderDriver.OfflineList(context.Background(), "")
		if err != nil {
			return nil, err
		}
		if len(tasks) > 0 {
			taskCache.Set(key, tasks, cache.WithEx[[]thunder_browser.OfflineTask](10*time.Second))
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
