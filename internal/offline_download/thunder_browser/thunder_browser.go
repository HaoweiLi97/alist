package thunder_browser

import (
	"context"
	"fmt"

	"github.com/alist-org/alist/v3/drivers/thunder_browser"
	"github.com/alist-org/alist/v3/internal/errs"
	"github.com/alist-org/alist/v3/internal/model"
	"github.com/alist-org/alist/v3/internal/offline_download/tool"
	"github.com/alist-org/alist/v3/internal/op"
)

type ThunderBrowser struct {
	refreshTaskCache bool
}

func (t *ThunderBrowser) Name() string {
	return "ThunderBrowser"
}

func (t *ThunderBrowser) Items() []model.SettingItem {
	return nil
}

func (t *ThunderBrowser) Run(task *tool.DownloadTask) error {
	return errs.NotSupport
}

func (t *ThunderBrowser) Init() (string, error) {
	t.refreshTaskCache = false
	return "ok", nil
}

func (t *ThunderBrowser) IsReady() bool {
	return true
}

func (t *ThunderBrowser) AddURL(args *tool.AddUrlArgs) (string, error) {
	t.refreshTaskCache = true
	storage, actualPath, err := op.GetStorageAndActualPath(args.TempDir)
	if err != nil {
		return "", err
	}
	ctx := context.Background()
	if err := op.MakeDir(ctx, storage, actualPath); err != nil {
		if _, getErr := op.GetUnwrap(ctx, storage, actualPath); getErr != nil {
			return "", err
		}
	}
	parentDir, err := op.GetUnwrap(ctx, storage, actualPath)
	if err != nil {
		return "", err
	}

	var task *thunder_browser.OfflineTask
	switch driver := storage.(type) {
	case *thunder_browser.ThunderBrowser:
		task, err = driver.OfflineDownload(ctx, args.Url, parentDir, "")
	case *thunder_browser.ThunderBrowserExpert:
		task, err = driver.OfflineDownload(ctx, args.Url, parentDir, "")
	default:
		return "", fmt.Errorf("unsupported storage driver for offline download, only ThunderBrowser is supported")
	}
	if err != nil {
		return "", fmt.Errorf("failed to add offline download task: %w", err)
	}
	if task == nil {
		return "", fmt.Errorf("failed to add offline download task: task is nil")
	}
	return task.ID, nil
}

func (t *ThunderBrowser) Remove(task *tool.DownloadTask) error {
	storage, _, err := op.GetStorageAndActualPath(task.TempDir)
	if err != nil {
		return err
	}
	switch driver := storage.(type) {
	case *thunder_browser.ThunderBrowser:
		return driver.DeleteOfflineTasks(context.Background(), []string{task.GID})
	case *thunder_browser.ThunderBrowserExpert:
		return driver.DeleteOfflineTasks(context.Background(), []string{task.GID})
	default:
		return fmt.Errorf("unsupported storage driver for offline download, only ThunderBrowser is supported")
	}
}

func (t *ThunderBrowser) Status(task *tool.DownloadTask) (*tool.Status, error) {
	storage, _, err := op.GetStorageAndActualPath(task.TempDir)
	if err != nil {
		return nil, err
	}

	var tasks []thunder_browser.OfflineTask
	switch driver := storage.(type) {
	case *thunder_browser.ThunderBrowser:
		tasks, err = t.getTasks(driver)
	case *thunder_browser.ThunderBrowserExpert:
		tasks, err = t.getTasksExpert(driver)
	default:
		return nil, fmt.Errorf("unsupported storage driver for offline download, only ThunderBrowser is supported")
	}
	if err != nil {
		return nil, err
	}

	s := &tool.Status{
		Progress:  0,
		Completed: false,
		Status:    "the task has been deleted",
	}
	for _, taskInfo := range tasks {
		if taskInfo.ID == task.GID {
			s.Progress = float64(taskInfo.Progress)
			s.Status = taskInfo.Message
			s.Completed = taskInfo.Phase == "PHASE_TYPE_COMPLETE"
			if taskInfo.Phase == "PHASE_TYPE_ERROR" {
				s.Err = fmt.Errorf(taskInfo.Message)
			}
			return s, nil
		}
	}
	s.Err = fmt.Errorf("the task has been deleted")
	return s, nil
}

func init() {
	tool.Tools.Add(&ThunderBrowser{})
}
