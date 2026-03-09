package handles

import (
	"fmt"
	stdpath "path"

	"github.com/alist-org/alist/v3/drivers/thunder_browser"
	"github.com/alist-org/alist/v3/internal/errs"
	"github.com/alist-org/alist/v3/internal/model"
	"github.com/alist-org/alist/v3/internal/op"
	"github.com/alist-org/alist/v3/server/common"
	"github.com/gin-gonic/gin"
)

type ThunderBatchMoveReq struct {
	SrcDir string   `json:"src_dir"`
	DstDir string   `json:"dst_dir"`
	Names  []string `json:"names"`
}

func FsMoveThunderBatch(c *gin.Context) {
	var req ThunderBatchMoveReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if len(req.Names) == 0 {
		common.ErrorStrResp(c, "Empty file names", 400)
		return
	}

	user := c.MustGet("user").(*model.User)
	if !user.CanMove() {
		common.ErrorResp(c, errs.PermissionDenied, 403)
		return
	}

	srcDir, err := user.JoinPath(req.SrcDir)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	dstDir, err := user.JoinPath(req.DstDir)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}

	srcStorage, srcActual, err := op.GetStorageAndActualPath(srcDir)
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	dstStorage, dstActual, err := op.GetStorageAndActualPath(dstDir)
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	if srcStorage.GetStorage().MountPath != dstStorage.GetStorage().MountPath {
		common.ErrorStrResp(c, "cross-storage move is not supported", 400)
		return
	}

	var xc *thunder_browser.XunLeiBrowserCommon
	switch s := srcStorage.(type) {
	case *thunder_browser.ThunderBrowser:
		xc = s.XunLeiBrowserCommon
	case *thunder_browser.ThunderBrowserExpert:
		xc = s.XunLeiBrowserCommon
	default:
		common.ErrorStrResp(c, "storage is not thunder_browser", 400)
		return
	}
	if xc == nil {
		common.ErrorStrResp(c, "thunder_browser is not initialized", 500)
		return
	}

	srcList, err := op.List(c, srcStorage, srcActual, model.ListArgs{})
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	nameToFile := make(map[string]*thunder_browser.Files, len(srcList))
	for _, obj := range srcList {
		if f, ok := model.UnwrapObj(obj).(*thunder_browser.Files); ok {
			nameToFile[f.GetName()] = f
		}
	}

	dstDirObj, err := op.GetUnwrap(c, dstStorage, dstActual)
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}

	dstParentID := ""
	dstSpace := ""
	if f, ok := dstDirObj.(*thunder_browser.Files); ok {
		dstParentID = f.GetID()
		dstSpace = f.GetSpace()
	}

	idsBySpace := make(map[string][]string)
	moved := 0
	for _, name := range req.Names {
		cleanName := stdpath.Base(name)
		f, ok := nameToFile[cleanName]
		if !ok {
			common.ErrorStrResp(c, fmt.Sprintf("file not found: %s", cleanName), 404)
			return
		}
		space := f.GetSpace()
		idsBySpace[space] = append(idsBySpace[space], f.GetID())
		moved += 1
	}

	for space, ids := range idsBySpace {
		if err := xc.BatchMoveByIDs(c, ids, space, dstParentID, dstSpace); err != nil {
			common.ErrorResp(c, err, 500)
			return
		}
	}

	op.ClearCache(srcStorage, srcActual)
	op.ClearCache(dstStorage, dstActual)
	common.SuccessResp(c, gin.H{
		"moved": moved,
	})
}
