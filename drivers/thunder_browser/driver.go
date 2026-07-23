package thunder_browser

import (
	"context"
	"errors"
	"fmt"
	"github.com/alist-org/alist/v3/drivers/base"
	"github.com/alist-org/alist/v3/internal/driver"
	"github.com/alist-org/alist/v3/internal/errs"
	"github.com/alist-org/alist/v3/internal/model"
	"github.com/alist-org/alist/v3/internal/op"
	"github.com/alist-org/alist/v3/pkg/singleflight"
	"github.com/alist-org/alist/v3/pkg/utils"
	hash_extend "github.com/alist-org/alist/v3/pkg/utils/hash"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/go-resty/resty/v2"
	log "github.com/sirupsen/logrus"
	"io"
	"net/http"
	"net/url"
	stdpath "path"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ThunderBrowser struct {
	*XunLeiBrowserCommon
	model.Storage
	Addition

	identity string
}

func (x *ThunderBrowser) Config() driver.Config {
	return config
}

func (x *ThunderBrowser) GetAddition() driver.Additional {
	return &x.Addition
}

func (x *ThunderBrowser) Init(ctx context.Context) (err error) {

	spaceTokenFunc := func() error {
		// 如果用户未设置 "超级保险柜" 密码 则直接返回
		if x.SafePassword == "" {
			return nil
		}
		// 通过 GetSafeAccessToken 获取
		token, err := x.GetSafeAccessToken(x.SafePassword)
		x.SetSpaceTokenResp(token)
		return err
	}

	// 初始化所需参数
	if x.XunLeiBrowserCommon == nil {
		x.XunLeiBrowserCommon = &XunLeiBrowserCommon{
			Common: &Common{
				client:            base.NewRestyClient(),
				Algorithms:        Algorithms,
				DeviceID:          utils.GetMD5EncodeStr(x.Username + x.Password),
				ClientID:          ClientID,
				ClientSecret:      ClientSecret,
				ClientVersion:     ClientVersion,
				PackageName:       PackageName,
				UserAgent:         BuildCustomUserAgent(utils.GetMD5EncodeStr(x.Username+x.Password), PackageName, SdkVersion, ClientVersion, PackageName),
				DownloadUserAgent: DownloadUserAgent,
				UseVideoUrl:       x.UseVideoUrl,
				RemoveWay:         x.Addition.RemoveWay,
				refreshCTokenCk: func(token string) {
					x.CaptchaToken = token
					op.MustSaveDriverStorage(x)
				},
			},
			refreshTokenFunc: func() error {
				// 通过RefreshToken刷新
				token, err := x.RefreshToken(x.TokenResp.RefreshToken)
				if err != nil {
					// 重新登录
					token, err = x.Login(x.Username, x.Password)
					if err != nil {
						x.GetStorage().SetStatus(fmt.Sprintf("%+v", err.Error()))
						op.MustSaveDriverStorage(x)
					}
				}
				x.SetTokenResp(token)
				return err
			},
		}
	}

	// 自定义验证码token
	ctoekn := strings.TrimSpace(x.CaptchaToken)
	if ctoekn != "" {
		x.SetCaptchaToken(ctoekn)
	}
	if x.DeviceID == "" {
		x.SetDeviceID(utils.GetMD5EncodeStr(x.Username + x.Password))
	}
	x.XunLeiBrowserCommon.UseVideoUrl = x.UseVideoUrl
	x.Addition.RootFolderID = x.RootFolderID
	// 防止重复登录
	identity := x.GetIdentity()
	if x.identity != identity || !x.IsLogin() {
		x.identity = identity
		// 登录
		token, err := x.Login(x.Username, x.Password)
		if err != nil {
			return err
		}
		x.SetTokenResp(token)
	}

	// 获取 spaceToken
	err = spaceTokenFunc()
	if err != nil {
		return err
	}

	return nil
}

func (x *ThunderBrowser) Drop(ctx context.Context) error {
	if x.XunLeiBrowserCommon != nil && x.XunLeiBrowserCommon.Common != nil {
		x.XunLeiBrowserCommon.Common.Close()
	}
	return nil
}

type ThunderBrowserExpert struct {
	*XunLeiBrowserCommon
	model.Storage
	ExpertAddition

	identity string
}

func (x *ThunderBrowserExpert) Config() driver.Config {
	return configExpert
}

func (x *ThunderBrowserExpert) GetAddition() driver.Additional {
	return &x.ExpertAddition
}

func (x *ThunderBrowserExpert) Init(ctx context.Context) (err error) {

	spaceTokenFunc := func() error {
		// 如果用户未设置 "超级保险柜" 密码 则直接返回
		if x.SafePassword == "" {
			return nil
		}
		// 通过 GetSafeAccessToken 获取
		token, err := x.GetSafeAccessToken(x.SafePassword)
		x.SetSpaceTokenResp(token)
		return err
	}

	// 防止重复登录
	identity := x.GetIdentity()
	if identity != x.identity || !x.IsLogin() {
		x.identity = identity
		x.XunLeiBrowserCommon = &XunLeiBrowserCommon{
			Common: &Common{
				client: base.NewRestyClient(),
				DeviceID: func() string {
					if len(x.DeviceID) != 32 {
						if x.LoginType == "user" {
							return utils.GetMD5EncodeStr(x.Username + x.Password)
						}
						return utils.GetMD5EncodeStr(x.ExpertAddition.RefreshToken)
					}
					return x.DeviceID
				}(),
				ClientID:      x.ClientID,
				ClientSecret:  x.ClientSecret,
				ClientVersion: x.ClientVersion,
				PackageName:   x.PackageName,
				UserAgent: func() string {
					if x.ExpertAddition.UserAgent != "" {
						return x.ExpertAddition.UserAgent
					}
					if x.LoginType == "user" {
						return BuildCustomUserAgent(utils.GetMD5EncodeStr(x.Username+x.Password), x.PackageName, SdkVersion, x.ClientVersion, x.PackageName)
					}
					return BuildCustomUserAgent(utils.GetMD5EncodeStr(x.ExpertAddition.RefreshToken), x.PackageName, SdkVersion, x.ClientVersion, x.PackageName)
				}(),
				DownloadUserAgent: func() string {
					if x.ExpertAddition.DownloadUserAgent != "" {
						return x.ExpertAddition.DownloadUserAgent
					}
					return DownloadUserAgent
				}(),
				UseVideoUrl: x.UseVideoUrl,
				RemoveWay:   x.ExpertAddition.RemoveWay,
				refreshCTokenCk: func(token string) {
					x.CaptchaToken = token
					op.MustSaveDriverStorage(x)
				},
			},
		}

		if x.ExpertAddition.CaptchaToken != "" {
			x.SetCaptchaToken(x.ExpertAddition.CaptchaToken)
			op.MustSaveDriverStorage(x)
		}
		if x.Common.DeviceID != "" {
			x.ExpertAddition.DeviceID = x.Common.DeviceID
			op.MustSaveDriverStorage(x)
		}
		if x.Common.UserAgent != "" {
			x.ExpertAddition.UserAgent = x.Common.UserAgent
			op.MustSaveDriverStorage(x)
		}
		if x.Common.DownloadUserAgent != "" {
			x.ExpertAddition.DownloadUserAgent = x.Common.DownloadUserAgent
			op.MustSaveDriverStorage(x)
		}
		x.XunLeiBrowserCommon.UseVideoUrl = x.UseVideoUrl
		x.ExpertAddition.RootFolderID = x.RootFolderID
		// 签名方法
		if x.SignType == "captcha_sign" {
			x.Common.Timestamp = x.Timestamp
			x.Common.CaptchaSign = x.CaptchaSign
		} else {
			x.Common.Algorithms = strings.Split(x.Algorithms, ",")
		}

		// 登录方式
		if x.LoginType == "refresh_token" {
			// 通过RefreshToken登录
			token, err := x.XunLeiBrowserCommon.RefreshToken(x.ExpertAddition.RefreshToken)
			if err != nil {
				return err
			}
			x.SetTokenResp(token)

			// 刷新token方法
			x.SetRefreshTokenFunc(func() error {
				token, err := x.XunLeiBrowserCommon.RefreshToken(x.TokenResp.RefreshToken)
				if err != nil {
					x.GetStorage().SetStatus(fmt.Sprintf("%+v", err.Error()))
				}
				x.SetTokenResp(token)
				op.MustSaveDriverStorage(x)
				return err
			})

			err = spaceTokenFunc()
			if err != nil {
				return err
			}

		} else {
			// 通过用户密码登录
			token, err := x.Login(x.Username, x.Password)
			if err != nil {
				return err
			}
			x.SetTokenResp(token)
			x.SetRefreshTokenFunc(func() error {
				token, err := x.XunLeiBrowserCommon.RefreshToken(x.TokenResp.RefreshToken)
				if err != nil {
					token, err = x.Login(x.Username, x.Password)
					if err != nil {
						x.GetStorage().SetStatus(fmt.Sprintf("%+v", err.Error()))
					}
				}
				x.SetTokenResp(token)
				op.MustSaveDriverStorage(x)
				return err
			})

			err = spaceTokenFunc()
			if err != nil {
				return err
			}
		}
	} else {
		// 仅修改验证码token
		if x.CaptchaToken != "" {
			x.SetCaptchaToken(x.CaptchaToken)
		}

		err = spaceTokenFunc()
		if err != nil {
			return err
		}

		x.XunLeiBrowserCommon.UserAgent = x.UserAgent
		x.XunLeiBrowserCommon.DownloadUserAgent = x.DownloadUserAgent
		x.XunLeiBrowserCommon.UseVideoUrl = x.UseVideoUrl
		x.ExpertAddition.RootFolderID = x.RootFolderID
	}

	return nil
}

func (x *ThunderBrowserExpert) Drop(ctx context.Context) error {
	if x.XunLeiBrowserCommon != nil && x.XunLeiBrowserCommon.Common != nil {
		x.XunLeiBrowserCommon.Common.Close()
	}
	return nil
}

func (x *ThunderBrowserExpert) SetTokenResp(token *TokenResp) {
	x.XunLeiBrowserCommon.SetTokenResp(token)
	if token != nil {
		x.ExpertAddition.RefreshToken = token.RefreshToken
	}
}

type XunLeiBrowserCommon struct {
	*Common
	*TokenResp // 登录信息

	refreshTokenFunc func() error
	pathRefCache     sync.Map // cleaned full path -> pathRef
	getObjCache      sync.Map // cleaned full path -> cachedObj
	getObjG          singleflight.Group[model.Obj]
	dirFilesCache    sync.Map // space:id -> cachedDirFiles
	dirFilesG        singleflight.Group[[]model.Obj]
}

type pathRef struct {
	ID    string
	Space string
	IsDir bool
}

type cachedObj struct {
	obj            model.Obj
	expiresAt      int64
	staleExpiresAt int64
}

type cachedDirFiles struct {
	files     []model.Obj
	expiresAt int64
}

const (
	getObjCacheTTL              = 15 * time.Second
	staleGetObjCacheTTL         = 5 * time.Minute
	dirFilesCacheTTL            = 2 * time.Second
	transientResolveRetryCount  = 2
	transientResolveRetryDelay  = 250 * time.Millisecond
	directLinkCacheSafetyMargin = 30 * time.Second
)

func (xc *XunLeiBrowserCommon) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	files, err := xc.getFiles(ctx, dir, args.Refresh)
	if err != nil {
		return nil, err
	}
	reqPath := utils.FixAndCleanPath(args.ReqPath)
	if reqPath != "" {
		parentSpace := ThunderBrowserDriveSpace
		if f, ok := dir.(*Files); ok {
			parentSpace = f.GetSpace()
		}
		xc.cachePathRef(reqPath, pathRef{ID: dir.GetID(), Space: parentSpace, IsDir: true})
		for _, obj := range files {
			ref := pathRef{ID: obj.GetID(), IsDir: obj.IsDir()}
			if f, ok := obj.(*Files); ok {
				ref.Space = f.GetSpace()
			}
			childPath := stdpath.Join(reqPath, obj.GetName())
			xc.cachePathRef(childPath, ref)
			xc.storeCachedObj(childPath, obj)
		}
	}
	return files, nil
}

func (xc *XunLeiBrowserCommon) Get(ctx context.Context, path string) (model.Obj, error) {
	cleanPath := utils.FixAndCleanPath(path)
	if cleanPath == "/" {
		return &model.Object{
			Name:     "root",
			Size:     0,
			Modified: time.Time{},
			IsFolder: true,
		}, nil
	}
	if hasRepeatedThunderRoot(cleanPath) {
		return nil, errs.ObjectNotFound
	}

	if cached, ok := xc.loadCachedObj(cleanPath); ok {
		log.Debugf("[thunder_browser.get] obj-cache-hit path=%s", cleanPath)
		return cached, nil
	}

	obj, err, shared := xc.getObjG.Do(cleanPath, func() (model.Obj, error) {
		if cached, ok := xc.loadCachedObj(cleanPath); ok {
			return cached, nil
		}
		resolved, resolveErr := xc.getNoSingleflight(ctx, cleanPath)
		if resolveErr == nil {
			xc.storeCachedObj(cleanPath, resolved)
			xc.cachePathRefFromObj(cleanPath, resolved)
			return resolved, nil
		}
		if errs.IsObjectNotFound(resolveErr) {
			if cached, ok := xc.loadStaleCachedObj(cleanPath); ok {
				log.Warnf("[thunder_browser.get] stale-cache fallback path=%s err=%v", cleanPath, resolveErr)
				return cached, nil
			}
		}
		return nil, resolveErr
	})
	if shared {
		log.Debugf("[thunder_browser.get] singleflight-shared path=%s", cleanPath)
	}
	return obj, err
}

// hasRepeatedThunderRoot prevents Xunlei's virtual root entry from being
// interpreted as a child of itself. Such a path is never a real cloud path and
// can otherwise be revived by the stale-object fallback.
func hasRepeatedThunderRoot(cleanPath string) bool {
	parts := strings.Split(strings.Trim(utils.FixAndCleanPath(cleanPath), "/"), "/")
	for i := 1; i < len(parts); i++ {
		if parts[i-1] == "迅雷云盘" && parts[i] == "迅雷云盘" {
			return true
		}
	}
	return false
}

func (xc *XunLeiBrowserCommon) getNoSingleflight(ctx context.Context, cleanPath string) (model.Obj, error) {

	if ref, ok := xc.loadPathRef(cleanPath); ok && ref.ID != "" {
		log.Debugf("[thunder_browser.get] cache-hit path=%s id=%s space=%s", cleanPath, ref.ID, ref.Space)
		if obj, ok := xc.loadStaleCachedObj(cleanPath); ok && xc.pathObjLooksValid(cleanPath, obj, ref) {
			// A preceding directory listing already supplied this object's ID and
			// metadata. Xunlei's by-ID response is occasionally empty for valid
			// objects; reusing the short-lived stale entry prevents a costly full
			// parent-directory scan in that case. A refreshed listing overwrites it.
			log.Debugf("[thunder_browser.get] stale-cache-hit path=%s", cleanPath)
			return obj, nil
		}
		if obj, err := xc.getByID(ctx, ref.ID, ref.Space); err == nil {
			if xc.pathObjLooksValid(cleanPath, obj, ref) {
				log.Debugf("[thunder_browser.get] by-id success path=%s", cleanPath)
				xc.storeCachedObj(cleanPath, obj)
				xc.cachePathRefFromObj(cleanPath, obj)
				return obj, nil
			}
			log.Warnf("[thunder_browser.get] by-id returned mismatched obj, fallback list path=%s id=%s space=%s name=%s is_dir=%v", cleanPath, ref.ID, ref.Space, obj.GetName(), obj.IsDir())
		}
		log.Debugf("[thunder_browser.get] by-id failed, fallback list path=%s", cleanPath)
	}

	parentPath, base := stdpath.Split(cleanPath)
	parentPath = utils.FixAndCleanPath(parentPath)
	parentRef, err := xc.resolveDirRefByPath(ctx, parentPath)
	if err != nil {
		return nil, err
	}

	parentDir := &Files{
		ID:    parentRef.ID,
		Space: parentRef.Space,
		Kind:  FOLDER,
	}
	for attempt := 0; attempt <= transientResolveRetryCount; attempt++ {
		children, err := xc.getFiles(ctx, parentDir, false)
		if err != nil {
			return nil, err
		}
		log.Debugf("[thunder_browser.get] fallback-list parent=%s attempt=%d", parentPath, attempt+1)
		for _, child := range children {
			if child.GetName() != base {
				continue
			}
			xc.cachePathRefFromObj(cleanPath, child)
			xc.storeCachedObj(cleanPath, child)
			return child, nil
		}
		if attempt < transientResolveRetryCount {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(transientResolveRetryDelay):
			}
		}
	}
	return nil, errs.ObjectNotFound
}

func (xc *XunLeiBrowserCommon) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	var lFile Files

	params := map[string]string{
		"_magic":         "2021",
		"space":          file.(*Files).GetSpace(),
		"thumbnail_size": "SIZE_LARGE",
		"with":           "url",
	}

	_, err := xc.Request(FILE_API_URL+"/{fileID}", http.MethodGet, func(r *resty.Request) {
		r.SetContext(ctx)
		r.SetPathParam("fileID", file.GetID())
		r.SetQueryParams(params)
		//r.SetQueryParam("space", "")
	}, &lFile)
	if err != nil {
		return nil, err
	}
	link := &model.Link{
		URL: lFile.WebContentLink,
		Header: http.Header{
			"User-Agent": {xc.DownloadUserAgent},
		},
	}
	var expiresAt time.Time

	if xc.UseVideoUrl {
		for _, media := range lFile.Medias {
			if media.Link.URL != "" {
				link.URL = media.Link.URL
				expiresAt = media.Link.Expire
				break
			}
		}
	}
	if expiration, ok := directLinkExpiration(link.URL, expiresAt, time.Now()); ok {
		link.Expiration = &expiration
	}
	return link, nil
}

// directLinkExpiration returns a safe cache lifetime for a Xunlei direct link.
// Download links normally expose their Unix expiry timestamp as the `e` query
// parameter; video links may instead return an explicit expiry in the payload.
func directLinkExpiration(rawURL string, expiresAt, now time.Time) (time.Duration, bool) {
	if expiresAt.IsZero() {
		u, err := url.Parse(rawURL)
		if err != nil {
			return 0, false
		}
		timestamp, err := strconv.ParseInt(u.Query().Get("e"), 10, 64)
		if err != nil || timestamp <= 0 {
			return 0, false
		}
		expiresAt = time.Unix(timestamp, 0)
	}

	expiration := expiresAt.Sub(now) - directLinkCacheSafetyMargin
	if expiration <= 0 {
		return 0, false
	}
	return expiration, true
}

func (xc *XunLeiBrowserCommon) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) error {
	js := base.Json{
		"kind":      FOLDER,
		"name":      dirName,
		"parent_id": parentDir.GetID(),
		"space":     parentDir.(*Files).GetSpace(),
	}

	_, err := xc.Request(FILE_API_URL, http.MethodPost, func(r *resty.Request) {
		r.SetContext(ctx)
		r.SetBody(&js)
	}, nil)
	return err
}

func (xc *XunLeiBrowserCommon) Move(ctx context.Context, srcObj, dstDir model.Obj) error {

	params := map[string]string{
		"_from": srcObj.(*Files).GetSpace(),
	}
	js := base.Json{
		"to":    base.Json{"parent_id": dstDir.GetID(), "space": dstDir.(*Files).GetSpace()},
		"space": srcObj.(*Files).GetSpace(),
		"ids":   []string{srcObj.GetID()},
	}

	_, err := xc.Request(FILE_API_URL+":batchMove", http.MethodPost, func(r *resty.Request) {
		r.SetContext(ctx)
		r.SetBody(&js)
		r.SetQueryParams(params)
	}, nil)
	return err
}

func (xc *XunLeiBrowserCommon) BatchMoveByIDs(ctx context.Context, ids []string, srcSpace, dstParentID, dstSpace string) error {
	params := map[string]string{
		"_from": srcSpace,
	}
	body := base.Json{
		"to":    base.Json{"parent_id": dstParentID, "space": dstSpace},
		"space": srcSpace,
		"ids":   ids,
	}
	_, err := xc.Request(FILE_API_URL+":batchMove", http.MethodPost, func(r *resty.Request) {
		r.SetContext(ctx)
		r.SetBody(&body)
		r.SetQueryParams(params)
	}, nil)
	return err
}

func (xc *XunLeiBrowserCommon) Rename(ctx context.Context, srcObj model.Obj, newName string) error {

	params := map[string]string{
		"space": srcObj.(*Files).GetSpace(),
	}

	_, err := xc.Request(FILE_API_URL+"/{fileID}", http.MethodPatch, func(r *resty.Request) {
		r.SetContext(ctx)
		r.SetPathParam("fileID", srcObj.GetID())
		r.SetBody(&base.Json{"name": newName})
		r.SetQueryParams(params)
	}, nil)
	return err
}

func (xc *XunLeiBrowserCommon) Copy(ctx context.Context, srcObj, dstDir model.Obj) error {

	params := map[string]string{
		"_from": srcObj.(*Files).GetSpace(),
	}
	js := base.Json{
		"to":    base.Json{"parent_id": dstDir.GetID(), "space": dstDir.(*Files).GetSpace()},
		"space": srcObj.(*Files).GetSpace(),
		"ids":   []string{srcObj.GetID()},
	}

	_, err := xc.Request(FILE_API_URL+":batchCopy", http.MethodPost, func(r *resty.Request) {
		r.SetContext(ctx)
		r.SetBody(&js)
		r.SetQueryParams(params)
	}, nil)
	return err
}

func (xc *XunLeiBrowserCommon) Remove(ctx context.Context, obj model.Obj) error {

	js := base.Json{
		"ids":   []string{obj.GetID()},
		"space": obj.(*Files).GetSpace(),
	}
	// 先判断是否是特殊情况
	if obj.(*Files).GetSpace() == ThunderDriveSpace {
		_, err := xc.Request(FILE_API_URL+"/{fileID}/trash", http.MethodPatch, func(r *resty.Request) {
			r.SetContext(ctx)
			r.SetPathParam("fileID", obj.GetID())
			r.SetBody("{}")
		}, nil)
		return err
	} else if obj.(*Files).GetSpace() == ThunderBrowserDriveSafeSpace || obj.(*Files).GetSpace() == ThunderDriveSafeSpace {
		_, err := xc.Request(FILE_API_URL+":batchDelete", http.MethodPost, func(r *resty.Request) {
			r.SetContext(ctx)
			r.SetBody(&js)
		}, nil)
		return err
	}

	// 根据用户选择的删除方式进行删除
	if xc.RemoveWay == "delete" {
		_, err := xc.Request(FILE_API_URL+":batchDelete", http.MethodPost, func(r *resty.Request) {
			r.SetContext(ctx)
			r.SetBody(&js)
		}, nil)
		return err
	} else {
		_, err := xc.Request(FILE_API_URL+":batchTrash", http.MethodPost, func(r *resty.Request) {
			r.SetContext(ctx)
			r.SetBody(&js)
		}, nil)
		return err
	}
}

func (xc *XunLeiBrowserCommon) Put(ctx context.Context, dstDir model.Obj, stream model.FileStreamer, up driver.UpdateProgress) error {
	hi := stream.GetHash()
	gcid := hi.GetHash(hash_extend.GCID)
	if len(gcid) < hash_extend.GCID.Width {
		tFile, err := stream.CacheFullInTempFile()
		if err != nil {
			return err
		}

		gcid, err = utils.HashFile(hash_extend.GCID, tFile, stream.GetSize())
		if err != nil {
			return err
		}
	}

	js := base.Json{
		"kind":        FILE,
		"parent_id":   dstDir.GetID(),
		"name":        stream.GetName(),
		"size":        stream.GetSize(),
		"hash":        gcid,
		"upload_type": UPLOAD_TYPE_RESUMABLE,
		"space":       dstDir.(*Files).GetSpace(),
	}

	var resp UploadTaskResponse
	_, err := xc.Request(FILE_API_URL, http.MethodPost, func(r *resty.Request) {
		r.SetContext(ctx)
		r.SetBody(&js)
	}, &resp)
	if err != nil {
		return err
	}

	param := resp.Resumable.Params
	if resp.UploadType == UPLOAD_TYPE_RESUMABLE {
		param.Endpoint = strings.TrimPrefix(param.Endpoint, param.Bucket+".")
		s, err := session.NewSession(&aws.Config{
			Credentials: credentials.NewStaticCredentials(param.AccessKeyID, param.AccessKeySecret, param.SecurityToken),
			Region:      aws.String("xunlei"),
			Endpoint:    aws.String(param.Endpoint),
		})
		if err != nil {
			return err
		}
		uploader := s3manager.NewUploader(s)
		if stream.GetSize() > s3manager.MaxUploadParts*s3manager.DefaultUploadPartSize {
			uploader.PartSize = stream.GetSize() / (s3manager.MaxUploadParts - 1)
		}
		_, err = uploader.UploadWithContext(ctx, &s3manager.UploadInput{
			Bucket:  aws.String(param.Bucket),
			Key:     aws.String(param.Key),
			Expires: aws.Time(param.Expiration),
			Body:    io.TeeReader(stream, driver.NewProgress(stream.GetSize(), up)),
		})
		return err
	}
	return nil
}

func (xc *XunLeiBrowserCommon) OfflineDownload(ctx context.Context, fileURL string, parentDir model.Obj, fileName string) (*OfflineTask, error) {
	var resp OfflineDownloadResp
	body := base.Json{
		"kind":        FILE,
		"name":        fileName,
		"parent_id":   parentDir.GetID(),
		"upload_type": UPLOAD_TYPE_URL,
		"url": base.Json{
			"url": fileURL,
		},
	}
	if files, ok := parentDir.(*Files); ok {
		body["space"] = files.GetSpace()
	} else {
		body["space"] = ThunderDriveSpace
	}
	_, err := xc.Request(FILE_API_URL, http.MethodPost, func(r *resty.Request) {
		r.SetContext(ctx)
		r.SetBody(&body)
	}, &resp)
	if err != nil {
		return nil, err
	}
	return &resp.Task, nil
}

func (xc *XunLeiBrowserCommon) OfflineList(ctx context.Context, nextPageToken string) ([]OfflineTask, error) {
	var resp OfflineListResp
	_, err := xc.Request(TASK_API_URL, http.MethodGet, func(req *resty.Request) {
		req.SetContext(ctx).SetQueryParams(map[string]string{
			"type":       "offline",
			"limit":      "10000",
			"page_token": nextPageToken,
			"space":      "default/*",
		})
	}, &resp)
	if err != nil {
		return nil, fmt.Errorf("failed to get offline list: %w", err)
	}
	return resp.Tasks, nil
}

func (xc *XunLeiBrowserCommon) DeleteOfflineTasks(ctx context.Context, taskIDs []string) error {
	_, err := xc.Request(TASK_API_URL, http.MethodDelete, func(req *resty.Request) {
		req.SetContext(ctx).SetQueryParams(map[string]string{
			"task_ids": strings.Join(taskIDs, ","),
			"_t":       strconv.FormatInt(time.Now().UnixMilli(), 10),
		})
	}, nil)
	if err != nil {
		return fmt.Errorf("failed to delete tasks %v: %w", taskIDs, err)
	}
	return nil
}

func (xc *XunLeiBrowserCommon) directoryFilesKey(dir model.Obj) string {
	if files, ok := dir.(*Files); ok {
		// The virtual driver root and the real “迅雷云盘” root both have an
		// empty ID, but live in different spaces. Keep their cache entries apart.
		return "files:" + files.GetSpace() + ":" + files.GetID()
	}
	return "root:" + ThunderBrowserDriveSpace + ":" + dir.GetID()
}

func cloneObjs(objs []model.Obj) []model.Obj {
	return append([]model.Obj(nil), objs...)
}

func (xc *XunLeiBrowserCommon) loadCachedDirFiles(key string) ([]model.Obj, bool) {
	v, ok := xc.dirFilesCache.Load(key)
	if !ok {
		return nil, false
	}
	entry, ok := v.(cachedDirFiles)
	if !ok || time.Now().UnixNano() > entry.expiresAt {
		xc.dirFilesCache.Delete(key)
		return nil, false
	}
	return cloneObjs(entry.files), true
}

func (xc *XunLeiBrowserCommon) getFiles(ctx context.Context, dir model.Obj, refresh bool) ([]model.Obj, error) {
	key := xc.directoryFilesKey(dir)
	if !refresh {
		if files, ok := xc.loadCachedDirFiles(key); ok {
			return files, nil
		}
	}

	files, err, _ := xc.dirFilesG.Do(key, func() ([]model.Obj, error) {
		if !refresh {
			if files, ok := xc.loadCachedDirFiles(key); ok {
				return files, nil
			}
		}
		files, err := xc.getFilesUncached(ctx, dir)
		if err != nil {
			return nil, err
		}
		xc.dirFilesCache.Store(key, cachedDirFiles{
			files:     cloneObjs(files),
			expiresAt: time.Now().Add(dirFilesCacheTTL).UnixNano(),
		})
		return files, nil
	})
	if err != nil {
		return nil, err
	}
	return cloneObjs(files), nil
}

func (xc *XunLeiBrowserCommon) getFilesUncached(ctx context.Context, dir model.Obj) ([]model.Obj, error) {
	files := make([]model.Obj, 0)
	var pageToken string
	_, isNestedDirectory := dir.(*Files)
	for {
		var fileList FileList
		folderSpace := ""
		switch dirF := dir.(type) {
		case *Files:
			folderSpace = dirF.GetSpace()
		default:
			// 处理 根目录的情况
			folderSpace = ThunderBrowserDriveSpace
		}
		params := map[string]string{
			"parent_id":      dir.GetID(),
			"page_token":     pageToken,
			"space":          folderSpace,
			"filters":        `{"trashed":{"eq":false}}`,
			"with":           "url",
			"with_audit":     "true",
			"thumbnail_size": "SIZE_LARGE",
		}

		_, err := xc.Request(FILE_API_URL, http.MethodGet, func(r *resty.Request) {
			r.SetContext(ctx)
			r.SetQueryParams(params)
		}, &fileList)
		if err != nil {
			return nil, err
		}
		for i := range fileList.Files {
			file := &fileList.Files[i]
			isDefaultThunderRoot := file.Name == "迅雷云盘" && file.FolderType == ThunderDriveFolderType && file.ID == "" && file.Space == ""
			// Xunlei returns two “迅雷云盘” entries at the virtual root. Keep
			// only the empty-ID DEFAULT_ROOT entry; the NORMAL one points to an
			// invalid duplicate path. Inside a directory, ignore any echoed
			// DEFAULT_ROOT entry as well.
			if (!isNestedDirectory && file.Name == "迅雷云盘" && !isDefaultThunderRoot) || (isNestedDirectory && file.Name == "迅雷云盘") {
				continue
			}
			files = append(files, file)
		}

		if fileList.NextPageToken == "" {
			break
		}
		pageToken = fileList.NextPageToken
	}
	return files, nil
}

func (xc *XunLeiBrowserCommon) getByID(ctx context.Context, fileID, space string) (model.Obj, error) {
	var f Files
	params := map[string]string{
		"_magic":         "2021",
		"space":          space,
		"thumbnail_size": "SIZE_LARGE",
	}
	_, err := xc.Request(FILE_API_URL+"/{fileID}", http.MethodGet, func(r *resty.Request) {
		r.SetContext(ctx)
		r.SetPathParam("fileID", fileID)
		r.SetQueryParams(params)
	}, &f)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (xc *XunLeiBrowserCommon) cachePathRef(path string, ref pathRef) {
	p := utils.FixAndCleanPath(path)
	if p == "" {
		return
	}
	xc.pathRefCache.Store(p, ref)
}

func (xc *XunLeiBrowserCommon) cachePathRefFromObj(path string, obj model.Obj) {
	if obj == nil {
		return
	}
	ref := pathRef{ID: obj.GetID(), IsDir: obj.IsDir()}
	if f, ok := obj.(*Files); ok {
		ref.Space = f.GetSpace()
	}
	xc.cachePathRef(path, ref)
}

func (xc *XunLeiBrowserCommon) loadPathRef(path string) (pathRef, bool) {
	v, ok := xc.pathRefCache.Load(utils.FixAndCleanPath(path))
	if !ok {
		return pathRef{}, false
	}
	ref, ok := v.(pathRef)
	return ref, ok
}

func (xc *XunLeiBrowserCommon) loadCachedObj(path string) (model.Obj, bool) {
	v, ok := xc.getObjCache.Load(utils.FixAndCleanPath(path))
	if !ok {
		return nil, false
	}
	entry, ok := v.(cachedObj)
	if !ok || entry.obj == nil {
		return nil, false
	}
	now := time.Now().UnixNano()
	if now > entry.expiresAt {
		if entry.staleExpiresAt > 0 && now <= entry.staleExpiresAt {
			return nil, false
		}
		xc.getObjCache.Delete(utils.FixAndCleanPath(path))
		return nil, false
	}
	return entry.obj, true
}

func (xc *XunLeiBrowserCommon) loadStaleCachedObj(path string) (model.Obj, bool) {
	v, ok := xc.getObjCache.Load(utils.FixAndCleanPath(path))
	if !ok {
		return nil, false
	}
	entry, ok := v.(cachedObj)
	if !ok || entry.obj == nil {
		return nil, false
	}
	now := time.Now().UnixNano()
	if entry.staleExpiresAt <= 0 || now > entry.staleExpiresAt {
		xc.getObjCache.Delete(utils.FixAndCleanPath(path))
		return nil, false
	}
	return entry.obj, true
}

func (xc *XunLeiBrowserCommon) storeCachedObj(path string, obj model.Obj) {
	if obj == nil {
		return
	}
	xc.getObjCache.Store(utils.FixAndCleanPath(path), cachedObj{
		obj:            obj,
		expiresAt:      time.Now().Add(getObjCacheTTL).UnixNano(),
		staleExpiresAt: time.Now().Add(staleGetObjCacheTTL).UnixNano(),
	})
}

func (xc *XunLeiBrowserCommon) pathObjLooksValid(cleanPath string, obj model.Obj, ref pathRef) bool {
	if obj == nil {
		return false
	}
	base := stdpath.Base(cleanPath)
	if base != "." && base != "/" && obj.GetName() != "" && obj.GetName() != base {
		return false
	}
	if obj.IsDir() != ref.IsDir {
		return false
	}
	if f, ok := obj.(*Files); ok && ref.Space != "" && f.GetSpace() != ref.Space {
		return false
	}
	return true
}

func (xc *XunLeiBrowserCommon) resolveDirRefByPath(ctx context.Context, dirPath string) (pathRef, error) {
	cleanPath := utils.FixAndCleanPath(dirPath)
	if cleanPath == "/" {
		return pathRef{ID: "", Space: ThunderBrowserDriveSpace, IsDir: true}, nil
	}
	if ref, ok := xc.loadPathRef(cleanPath); ok && ref.IsDir {
		return ref, nil
	}

	parts := strings.Split(strings.TrimPrefix(cleanPath, "/"), "/")
	curPath := "/"
	curRef := pathRef{ID: "", Space: ThunderBrowserDriveSpace, IsDir: true}
	for _, part := range parts {
		if part == "" {
			continue
		}
		nextPath := stdpath.Join(curPath, part)
		if cached, ok := xc.loadPathRef(nextPath); ok && cached.IsDir {
			curPath = nextPath
			curRef = cached
			continue
		}
		dirObj := &Files{ID: curRef.ID, Space: curRef.Space, Kind: FOLDER}
		found := false
		for attempt := 0; attempt <= transientResolveRetryCount; attempt++ {
			items, err := xc.getFiles(ctx, dirObj, false)
			if err != nil {
				return pathRef{}, err
			}
			for _, item := range items {
				if item.GetName() != part || !item.IsDir() {
					continue
				}
				nextRef := pathRef{
					ID:    item.GetID(),
					IsDir: true,
				}
				if f, ok := item.(*Files); ok {
					nextRef.Space = f.GetSpace()
				}
				xc.cachePathRef(nextPath, nextRef)
				curPath = nextPath
				curRef = nextRef
				found = true
				break
			}
			if found {
				break
			}
			if attempt < transientResolveRetryCount {
				select {
				case <-ctx.Done():
					return pathRef{}, ctx.Err()
				case <-time.After(transientResolveRetryDelay):
				}
			}
		}
		if !found {
			return pathRef{}, errs.ObjectNotFound
		}
	}
	return curRef, nil
}

// SetRefreshTokenFunc 设置刷新Token的方法
func (xc *XunLeiBrowserCommon) SetRefreshTokenFunc(fn func() error) {
	xc.refreshTokenFunc = fn
}

// SetTokenResp 设置Token
func (xc *XunLeiBrowserCommon) SetTokenResp(tr *TokenResp) {
	xc.TokenResp = tr
}

// SetSpaceTokenResp 设置Token
func (xc *XunLeiBrowserCommon) SetSpaceTokenResp(spaceToken string) {
	xc.TokenResp.Token = spaceToken
}

// Request 携带Authorization和CaptchaToken的请求
func (xc *XunLeiBrowserCommon) Request(url string, method string, callback base.ReqCallback, resp interface{}) ([]byte, error) {
	data, err := xc.Common.Request(url, method, func(req *resty.Request) {
		req.SetHeaders(map[string]string{
			"Authorization":         xc.GetToken(),
			"X-Captcha-Token":       xc.GetCaptchaToken(),
			"X-Space-Authorization": xc.GetSpaceToken(),
		})
		if callback != nil {
			callback(req)
		}
	}, resp)

	errResp, ok := err.(*ErrResp)
	if !ok {
		return nil, err
	}

	switch errResp.ErrorCode {
	case 0:
		return data, nil
	case 4122, 4121, 10, 16:
		if xc.refreshTokenFunc != nil {
			if err = xc.refreshTokenFunc(); err == nil {
				break
			}
		}
		return nil, err
	case 9:
		// space_token 获取失败
		if errResp.ErrorMsg == "space_token_invalid" {
			if token, err := xc.GetSafeAccessToken(xc.Token); err != nil {
				return nil, err
			} else {
				xc.SetSpaceTokenResp(token)
			}

		}
		if errResp.ErrorMsg == "captcha_invalid" {
			// 验证码token过期
			if err = xc.RefreshCaptchaTokenAtLogin(GetAction(method, url), xc.UserID); err != nil {
				return nil, err
			}
		}
		return nil, err
	default:
		return nil, err
	}
	return xc.Request(url, method, callback, resp)
}

// RefreshToken 刷新Token
func (xc *XunLeiBrowserCommon) RefreshToken(refreshToken string) (*TokenResp, error) {
	var resp TokenResp
	_, err := xc.Common.Request(XLUSER_API_URL+"/auth/token", http.MethodPost, func(req *resty.Request) {
		req.SetBody(&base.Json{
			"grant_type":    "refresh_token",
			"refresh_token": refreshToken,
			"client_id":     xc.ClientID,
			"client_secret": xc.ClientSecret,
		})
	}, &resp)
	if err != nil {
		return nil, err
	}

	if resp.RefreshToken == "" {
		return nil, errors.New("refresh token is empty")
	}
	return &resp, nil
}

// GetSafeAccessToken 获取 超级保险柜 AccessToken
func (xc *XunLeiBrowserCommon) GetSafeAccessToken(safePassword string) (string, error) {
	var resp TokenResp
	_, err := xc.Request(XLUSER_API_URL+"/password/check", http.MethodPost, func(req *resty.Request) {
		req.SetBody(&base.Json{
			"scene":    "box",
			"password": EncryptPassword(safePassword),
		})
	}, &resp)
	if err != nil {
		return "", err
	}

	if resp.Token == "" {
		return "", errors.New("SafePassword is incorrect ")
	}
	return resp.Token, nil
}

// Login 登录
func (xc *XunLeiBrowserCommon) Login(username, password string) (*TokenResp, error) {
	url := XLUSER_API_URL + "/auth/signin"
	err := xc.RefreshCaptchaTokenInLogin(GetAction(http.MethodPost, url), username)
	if err != nil {
		return nil, err
	}

	var resp TokenResp
	_, err = xc.Common.Request(url, http.MethodPost, func(req *resty.Request) {
		req.SetBody(&SignInRequest{
			CaptchaToken: xc.GetCaptchaToken(),
			ClientID:     xc.ClientID,
			ClientSecret: xc.ClientSecret,
			Username:     username,
			Password:     password,
		})
	}, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (xc *XunLeiBrowserCommon) IsLogin() bool {
	if xc.TokenResp == nil {
		return false
	}
	_, err := xc.Request(XLUSER_API_URL+"/user/me", http.MethodGet, nil, nil)
	return err == nil
}
