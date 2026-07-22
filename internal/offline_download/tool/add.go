package tool

import (
	"context"
	"github.com/alist-org/alist/v3/internal/model"
	"github.com/alist-org/alist/v3/internal/task"
	"path/filepath"
	"strings"
	"sync"

	"github.com/alist-org/alist/v3/internal/conf"
	"github.com/alist-org/alist/v3/internal/errs"
	"github.com/alist-org/alist/v3/internal/op"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/xhofe/tache"
)

type DeletePolicy string

const (
	DeleteOnUploadSucceed DeletePolicy = "delete_on_upload_succeed"
	DeleteOnUploadFailed  DeletePolicy = "delete_on_upload_failed"
	DeleteNever           DeletePolicy = "delete_never"
	DeleteAlways          DeletePolicy = "delete_always"
)

type AddURLArgs struct {
	URL          string
	DstDirPath   string
	Tool         string
	DeletePolicy DeletePolicy
}

var addURLMu sync.Mutex

func isActiveDownloadTaskState(state tache.State) bool {
	switch state {
	case tache.StateSucceeded, tache.StateCanceled, tache.StateFailed:
		return false
	default:
		return true
	}
}

func sameTaskCreator(left, right *model.User) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.ID == right.ID
}

func findInFlightDownloadTask(url, dstDirPath, toolName string, deletePolicy DeletePolicy, creator *model.User) *DownloadTask {
	if DownloadTaskManager == nil {
		return nil
	}
	url = strings.TrimSpace(url)
	for _, existing := range DownloadTaskManager.GetByCondition(func(task *DownloadTask) bool {
		return isActiveDownloadTaskState(task.GetState()) &&
			strings.TrimSpace(task.Url) == url &&
			task.DstDirPath == dstDirPath &&
			task.Toolname == toolName &&
			task.DeletePolicy == deletePolicy &&
			sameTaskCreator(task.Creator, creator)
	}) {
		return existing
	}
	return nil
}

func AddURL(ctx context.Context, args *AddURLArgs) (task.TaskInfoWithCreator, error) {
	// get tool
	tool, err := Tools.Get(args.Tool)
	if err != nil {
		return nil, errors.Wrapf(err, "failed get tool")
	}
	// check tool is ready
	if !tool.IsReady() {
		// try to init tool
		if _, err := tool.Init(); err != nil {
			return nil, errors.Wrapf(err, "failed init tool %s", args.Tool)
		}
	}
	// check storage
	storage, dstDirActualPath, err := op.GetStorageAndActualPath(args.DstDirPath)
	if err != nil {
		return nil, errors.WithMessage(err, "failed get storage")
	}
	// check is it could upload
	if storage.Config().NoUpload {
		return nil, errors.WithStack(errs.UploadNotSupported)
	}
	// check path is valid
	obj, err := op.Get(ctx, storage, dstDirActualPath)
	if err != nil {
		if !errs.IsObjectNotFound(err) {
			return nil, errors.WithMessage(err, "failed get object")
		}
	} else {
		if !obj.IsDir() {
			// can't add to a file
			return nil, errors.WithStack(errs.NotFolder)
		}
	}

	uid := uuid.NewString()
	toolName := tool.Name()
	tempDir := filepath.Join(conf.Conf.TempDir, toolName, uid)
	deletePolicy := args.DeletePolicy

	switch toolName {
	case "115 Cloud":
		tempDir = args.DstDirPath
		// 防止将下载好的文件删除
		deletePolicy = DeleteNever
	case "pikpak":
		tempDir = args.DstDirPath
		// 防止将下载好的文件删除
		deletePolicy = DeleteNever
	case "Thunder", "ThunderBrowser", "ThunderX":
		tempDir = args.DstDirPath
		// 防止将下载好的文件删除
		deletePolicy = DeleteNever
	}

	taskCreator, _ := ctx.Value("user").(*model.User) // taskCreator is nil when convert failed

	// The AList task manager is process-local, so serialize the lookup and add
	// operation. This makes retries of a non-idempotent add request return the
	// existing in-flight task instead of creating another remote cloud task.
	addURLMu.Lock()
	defer addURLMu.Unlock()
	if existing := findInFlightDownloadTask(args.URL, args.DstDirPath, toolName, deletePolicy, taskCreator); existing != nil {
		return existing, nil
	}

	t := &DownloadTask{
		TaskWithCreator: task.TaskWithCreator{
			Creator: taskCreator,
		},
		Url:          strings.TrimSpace(args.URL),
		DstDirPath:   args.DstDirPath,
		TempDir:      tempDir,
		DeletePolicy: deletePolicy,
		Toolname:     toolName,
		tool:         tool,
	}
	DownloadTaskManager.Add(t)
	return t, nil
}
