package thunderx

import (
	"context"
	"fmt"

	"github.com/alist-org/alist/v3/drivers/thunderx"
	"github.com/alist-org/alist/v3/internal/errs"
	"github.com/alist-org/alist/v3/internal/model"
	"github.com/alist-org/alist/v3/internal/offline_download/tool"
	"github.com/alist-org/alist/v3/internal/op"
)

type ThunderX struct {
	refreshTaskCache bool
}

func (t *ThunderX) Name() string {
	return "ThunderX"
}

func (t *ThunderX) Items() []model.SettingItem {
	return nil
}

func (t *ThunderX) Init() (string, error) {
	t.refreshTaskCache = false
	return "ok", nil
}

func (t *ThunderX) IsReady() bool {
	return true
}

func (t *ThunderX) AddURL(args *tool.AddUrlArgs) (string, error) {
	t.refreshTaskCache = true
	storage, actualPath, err := op.GetStorageAndActualPath(args.TempDir)
	if err != nil {
		return "", err
	}
	driver, ok := storage.(*thunderx.ThunderX)
	if !ok {
		return "", fmt.Errorf("unsupported storage driver for offline download, only ThunderX is supported")
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
	task, err := driver.OfflineDownload(ctx, args.Url, parentDir, "")
	if err != nil {
		return "", fmt.Errorf("failed to add offline download task: %w", err)
	}
	if task == nil {
		return "", fmt.Errorf("failed to add offline download task: task is nil")
	}
	return task.ID, nil
}

func (t *ThunderX) Remove(task *tool.DownloadTask) error {
	storage, _, err := op.GetStorageAndActualPath(task.TempDir)
	if err != nil {
		return err
	}
	driver, ok := storage.(*thunderx.ThunderX)
	if !ok {
		return fmt.Errorf("unsupported storage driver for offline download, only ThunderX is supported")
	}
	return driver.DeleteOfflineTasks(context.Background(), []string{task.GID}, false)
}

func (t *ThunderX) Status(task *tool.DownloadTask) (*tool.Status, error) {
	storage, _, err := op.GetStorageAndActualPath(task.TempDir)
	if err != nil {
		return nil, err
	}
	driver, ok := storage.(*thunderx.ThunderX)
	if !ok {
		return nil, fmt.Errorf("unsupported storage driver for offline download, only ThunderX is supported")
	}
	tasks, err := t.getTasks(driver)
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

func (t *ThunderX) Run(task *tool.DownloadTask) error {
	return errs.NotSupport
}

func init() {
	tool.Tools.Add(&ThunderX{})
}
