package _115

import (
	"context"
	stdpath "path"
	"strings"
	"sync"
	"time"

	driver115 "github.com/SheltonZhu/115driver/pkg/driver"
	"github.com/alist-org/alist/v3/internal/driver"
	ierrs "github.com/alist-org/alist/v3/internal/errs"
	"github.com/alist-org/alist/v3/internal/model"
	"github.com/alist-org/alist/v3/pkg/http_range"
	"github.com/alist-org/alist/v3/pkg/singleflight"
	"github.com/alist-org/alist/v3/pkg/utils"
	"github.com/pkg/errors"
	"golang.org/x/time/rate"
)

type Pan115 struct {
	model.Storage
	Addition
	client      *driver115.Pan115Client
	limiter     *rate.Limiter
	appVerOnce  sync.Once
	pathIDCache sync.Map // cleaned full path -> file id
	getObjCache sync.Map // cleaned full path -> cached115Obj
	getObjG     singleflight.Group[model.Obj]
}

type cached115Obj struct {
	obj       model.Obj
	expiresAt int64
}

const getObjCacheTTL115 = 15 * time.Second

func (d *Pan115) Config() driver.Config {
	return config
}

func (d *Pan115) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *Pan115) Init(ctx context.Context) error {
	d.appVerOnce.Do(d.initAppVer)
	if d.LimitRate > 0 {
		d.limiter = rate.NewLimiter(rate.Limit(d.LimitRate), 1)
	}
	return d.login()
}

func (d *Pan115) WaitLimit(ctx context.Context) error {
	if d.limiter != nil {
		return d.limiter.Wait(ctx)
	}
	return nil
}

func (d *Pan115) Drop(ctx context.Context) error {
	return nil
}

func (d *Pan115) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	if err := d.WaitLimit(ctx); err != nil {
		return nil, err
	}
	files, err := d.getFiles(dir.GetID())
	if err != nil && !errors.Is(err, driver115.ErrNotExist) {
		return nil, err
	}
	objs, convErr := utils.SliceConvert(files, func(src FileObj) (model.Obj, error) {
		return &src, nil
	})
	if convErr != nil {
		return nil, convErr
	}
	reqPath := utils.FixAndCleanPath(args.ReqPath)
	if reqPath != "" {
		d.cachePathID(reqPath, dir.GetID())
		for _, obj := range objs {
			d.cachePathID(stdpath.Join(reqPath, obj.GetName()), obj.GetID())
		}
	}
	return objs, nil
}

func (d *Pan115) Get(ctx context.Context, path string) (model.Obj, error) {
	cleanPath := utils.FixAndCleanPath(path)
	if cleanPath == "/" {
		rootID := d.RootFolderID
		if rootID == "" {
			rootID = "0"
		}
		return &FileObj{File: driver115.File{
			IsDirectory: true,
			FileID:      rootID,
			ParentID:    "",
			Name:        "root",
			CreateTime:  time.Time{},
			UpdateTime:  time.Time{},
		}}, nil
	}

	if cached, ok := d.loadCachedObj(cleanPath); ok {
		return cached, nil
	}

	obj, err, _ := d.getObjG.Do(cleanPath, func() (model.Obj, error) {
		if cached, ok := d.loadCachedObj(cleanPath); ok {
			return cached, nil
		}
		resolved, resolveErr := d.getNoSingleflight(ctx, cleanPath)
		if resolveErr == nil {
			d.storeCachedObj(cleanPath, resolved)
		}
		return resolved, resolveErr
	})
	return obj, err
}

func (d *Pan115) getNoSingleflight(ctx context.Context, cleanPath string) (model.Obj, error) {
	if fileID, ok := d.loadPathID(cleanPath); ok && fileID != "" {
		if err := d.WaitLimit(ctx); err != nil {
			return nil, err
		}
		if f, err := d.getNewFile(fileID); err == nil && f != nil {
			d.storeCachedObj(cleanPath, f)
			return f, nil
		}
	}

	parentPath, base := stdpath.Split(cleanPath)
	parentPath = utils.FixAndCleanPath(parentPath)
	parentID, err := d.resolveDirIDByPath(ctx, parentPath)
	if err != nil {
		return nil, err
	}

	if err := d.WaitLimit(ctx); err != nil {
		return nil, err
	}
	children, err := d.getFiles(parentID)
	if err != nil {
		return nil, err
	}
	for i := range children {
		child := &children[i]
		if child.GetName() != base {
			continue
		}
		d.cachePathID(cleanPath, child.GetID())
		d.storeCachedObj(cleanPath, child)
		return child, nil
	}
	return nil, ierrs.ObjectNotFound
}

func (d *Pan115) cachePathID(path, fileID string) {
	p := utils.FixAndCleanPath(path)
	if p == "" || fileID == "" {
		return
	}
	d.pathIDCache.Store(p, fileID)
}

func (d *Pan115) loadPathID(path string) (string, bool) {
	v, ok := d.pathIDCache.Load(utils.FixAndCleanPath(path))
	if !ok {
		return "", false
	}
	id, ok := v.(string)
	return id, ok && id != ""
}

func (d *Pan115) loadCachedObj(path string) (model.Obj, bool) {
	v, ok := d.getObjCache.Load(utils.FixAndCleanPath(path))
	if !ok {
		return nil, false
	}
	entry, ok := v.(cached115Obj)
	if !ok || entry.obj == nil {
		return nil, false
	}
	if time.Now().UnixNano() > entry.expiresAt {
		d.getObjCache.Delete(utils.FixAndCleanPath(path))
		return nil, false
	}
	return entry.obj, true
}

func (d *Pan115) storeCachedObj(path string, obj model.Obj) {
	if obj == nil {
		return
	}
	d.getObjCache.Store(utils.FixAndCleanPath(path), cached115Obj{
		obj:       obj,
		expiresAt: time.Now().Add(getObjCacheTTL115).UnixNano(),
	})
}

func (d *Pan115) resolveDirIDByPath(ctx context.Context, dirPath string) (string, error) {
	cleanPath := utils.FixAndCleanPath(dirPath)
	rootID := d.RootFolderID
	if rootID == "" {
		rootID = "0"
	}
	if cleanPath == "/" {
		return rootID, nil
	}
	if id, ok := d.loadPathID(cleanPath); ok {
		return id, nil
	}

	parts := strings.Split(strings.TrimPrefix(cleanPath, "/"), "/")
	curPath := "/"
	curID := rootID
	for _, part := range parts {
		if part == "" {
			continue
		}
		nextPath := stdpath.Join(curPath, part)
		if cachedID, ok := d.loadPathID(nextPath); ok {
			curPath = nextPath
			curID = cachedID
			continue
		}
		if err := d.WaitLimit(ctx); err != nil {
			return "", err
		}
		items, err := d.getFiles(curID)
		if err != nil {
			return "", err
		}
		found := false
		for i := range items {
			item := &items[i]
			if item.GetName() != part || !item.IsDir() {
				continue
			}
			curID = item.GetID()
			curPath = nextPath
			d.cachePathID(curPath, curID)
			found = true
			break
		}
		if !found {
			return "", ierrs.ObjectNotFound
		}
	}
	return curID, nil
}

func (d *Pan115) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	if err := d.WaitLimit(ctx); err != nil {
		return nil, err
	}
	userAgent := args.Header.Get("User-Agent")
	downloadInfo, err := d.
		DownloadWithUA(file.(*FileObj).PickCode, userAgent)
	if err != nil {
		return nil, err
	}
	link := &model.Link{
		URL:    downloadInfo.Url.Url,
		Header: downloadInfo.Header,
	}
	return link, nil
}

func (d *Pan115) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) (model.Obj, error) {
	if err := d.WaitLimit(ctx); err != nil {
		return nil, err
	}

	result := driver115.MkdirResp{}
	form := map[string]string{
		"pid":   parentDir.GetID(),
		"cname": dirName,
	}
	req := d.client.NewRequest().
		SetFormData(form).
		SetResult(&result).
		ForceContentType("application/json;charset=UTF-8")

	resp, err := req.Post(driver115.ApiDirAdd)

	err = driver115.CheckErr(err, &result, resp)
	if err != nil {
		return nil, err
	}
	f, err := d.getNewFile(result.FileID)
	if err != nil {
		return nil, nil
	}
	return f, nil
}

func (d *Pan115) Move(ctx context.Context, srcObj, dstDir model.Obj) (model.Obj, error) {
	if err := d.WaitLimit(ctx); err != nil {
		return nil, err
	}
	if err := d.client.Move(dstDir.GetID(), srcObj.GetID()); err != nil {
		return nil, err
	}
	f, err := d.getNewFile(srcObj.GetID())
	if err != nil {
		return nil, nil
	}
	return f, nil
}

func (d *Pan115) Rename(ctx context.Context, srcObj model.Obj, newName string) (model.Obj, error) {
	if err := d.WaitLimit(ctx); err != nil {
		return nil, err
	}
	if err := d.client.Rename(srcObj.GetID(), newName); err != nil {
		return nil, err
	}
	f, err := d.getNewFile((srcObj.GetID()))
	if err != nil {
		return nil, nil
	}
	return f, nil
}

func (d *Pan115) Copy(ctx context.Context, srcObj, dstDir model.Obj) error {
	if err := d.WaitLimit(ctx); err != nil {
		return err
	}
	return d.client.Copy(dstDir.GetID(), srcObj.GetID())
}

func (d *Pan115) Remove(ctx context.Context, obj model.Obj) error {
	if err := d.WaitLimit(ctx); err != nil {
		return err
	}
	return d.client.Delete(obj.GetID())
}

func (d *Pan115) Put(ctx context.Context, dstDir model.Obj, stream model.FileStreamer, up driver.UpdateProgress) (model.Obj, error) {
	if err := d.WaitLimit(ctx); err != nil {
		return nil, err
	}

	var (
		fastInfo *driver115.UploadInitResp
		dirID    = dstDir.GetID()
	)

	if ok, err := d.client.UploadAvailable(); err != nil || !ok {
		return nil, err
	}
	if stream.GetSize() > d.client.UploadMetaInfo.SizeLimit {
		return nil, driver115.ErrUploadTooLarge
	}
	//if digest, err = d.client.GetDigestResult(stream); err != nil {
	//	return err
	//}

	const PreHashSize int64 = 128 * utils.KB
	hashSize := PreHashSize
	if stream.GetSize() < PreHashSize {
		hashSize = stream.GetSize()
	}
	reader, err := stream.RangeRead(http_range.Range{Start: 0, Length: hashSize})
	if err != nil {
		return nil, err
	}
	preHash, err := utils.HashReader(utils.SHA1, reader)
	if err != nil {
		return nil, err
	}
	preHash = strings.ToUpper(preHash)
	fullHash := stream.GetHash().GetHash(utils.SHA1)
	if len(fullHash) <= 0 {
		tmpF, err := stream.CacheFullInTempFile()
		if err != nil {
			return nil, err
		}
		fullHash, err = utils.HashFile(utils.SHA1, tmpF)
		if err != nil {
			return nil, err
		}
	}
	fullHash = strings.ToUpper(fullHash)

	// rapid-upload
	// note that 115 add timeout for rapid-upload,
	// and "sig invalid" err is thrown even when the hash is correct after timeout.
	if fastInfo, err = d.rapidUpload(stream.GetSize(), stream.GetName(), dirID, preHash, fullHash, stream); err != nil {
		return nil, err
	}
	if matched, err := fastInfo.Ok(); err != nil {
		return nil, err
	} else if matched {
		f, err := d.getNewFileByPickCode(fastInfo.PickCode)
		if err != nil {
			return nil, nil
		}
		return f, nil
	}

	var uploadResult *UploadResult
	// 闪传失败，上传
	if stream.GetSize() <= 10*utils.MB { // 文件大小小于10MB，改用普通模式上传
		if uploadResult, err = d.UploadByOSS(&fastInfo.UploadOSSParams, stream, dirID); err != nil {
			return nil, err
		}
	} else {
		// 分片上传
		if uploadResult, err = d.UploadByMultipart(&fastInfo.UploadOSSParams, stream.GetSize(), stream, dirID); err != nil {
			return nil, err
		}
	}

	file, err := d.getNewFile(uploadResult.Data.FileID)
	if err != nil {
		return nil, nil
	}
	return file, nil
}

func (d *Pan115) OfflineList(ctx context.Context) ([]*driver115.OfflineTask, error) {
	resp, err := d.client.ListOfflineTask(0)
	if err != nil {
		return nil, err
	}
	return resp.Tasks, nil
}

func (d *Pan115) OfflineDownload(ctx context.Context, uris []string, dstDir model.Obj) ([]string, error) {
	return d.client.AddOfflineTaskURIs(uris, dstDir.GetID())
}

func (d *Pan115) DeleteOfflineTasks(ctx context.Context, hashes []string, deleteFiles bool) error {
	return d.client.DeleteOfflineTasks(hashes, deleteFiles)
}

var _ driver.Driver = (*Pan115)(nil)
