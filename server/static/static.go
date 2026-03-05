package static

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strings"

	"github.com/alist-org/alist/v3/cmd/flags"
	"github.com/alist-org/alist/v3/internal/conf"
	"github.com/alist-org/alist/v3/internal/offline_download/tool"
	"github.com/alist-org/alist/v3/internal/op"
	"github.com/alist-org/alist/v3/internal/setting"
	"github.com/alist-org/alist/v3/pkg/utils"
	"github.com/alist-org/alist/v3/public"
	"github.com/gin-gonic/gin"
)

var static fs.FS

func initStatic() {
	if conf.Conf.DistDir == "" {
		dist, err := fs.Sub(public.Public, "dist")
		if err != nil {
			utils.Log.Fatalf("failed to read dist dir")
		}
		static = dist
		return
	}
	static = os.DirFS(conf.Conf.DistDir)
}

func initIndex() {
	indexFile, err := static.Open("index.html")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			utils.Log.Fatalf("index.html not exist, you may forget to put dist of frontend to public/dist")
		}
		utils.Log.Fatalf("failed to read index.html: %v", err)
	}
	defer func() {
		_ = indexFile.Close()
	}()
	index, err := io.ReadAll(indexFile)
	if err != nil {
		utils.Log.Fatalf("failed to read dist/index.html")
	}
	conf.RawIndexHtml = string(index)
	siteConfig := getSiteConfig()
	// Frontend appends "/api" to window.ALIST.api internally.
	// So we should inject base path here instead of ".../api", otherwise
	// requests become "/api/api/*".
	apiPath := strings.TrimSuffix(siteConfig.BasePath, "/")
	if apiPath == "" {
		apiPath = "/"
	}
	replaceMap := map[string]string{
		"cdn: undefined":       fmt.Sprintf("cdn: '%s'", siteConfig.Cdn),
		"base_path: undefined": fmt.Sprintf("base_path: '%s'", siteConfig.BasePath),
		"api: undefined":       fmt.Sprintf("api: '%s'", apiPath),
	}
	for k, v := range replaceMap {
		conf.RawIndexHtml = strings.Replace(conf.RawIndexHtml, k, v, 1)
	}
	UpdateIndex()
}

func UpdateIndex() {
	favicon := setting.GetStr(conf.Favicon)
	title := setting.GetStr(conf.SiteTitle)
	customizeHead := setting.GetStr(conf.CustomizeHead)
	customizeBody := setting.GetStr(conf.CustomizeBody)
	mainColor := setting.GetStr(conf.MainColor)
	conf.ManageHtml = conf.RawIndexHtml
	replaceMap1 := map[string]string{
		"https://jsd.nn.ci/gh/alist-org/logo@main/logo.svg": favicon,
		"Loading...":            title,
		"main_color: undefined": fmt.Sprintf("main_color: '%s'", mainColor),
	}
	for k, v := range replaceMap1 {
		conf.ManageHtml = strings.Replace(conf.ManageHtml, k, v, 1)
	}
	conf.IndexHtml = conf.ManageHtml
	replaceMap2 := map[string]string{
		"<!-- customize head -->": customizeHead,
		"<!-- customize body -->": customizeBody,
	}
	for k, v := range replaceMap2 {
		conf.IndexHtml = strings.Replace(conf.IndexHtml, k, v, 1)
	}
}

func Static(r *gin.RouterGroup, noRoute func(handlers ...gin.HandlerFunc)) {
	initStatic()
	initIndex()
	folders := []string{"assets", "images", "streamer", "static"}
	r.Use(func(c *gin.Context) {
		for i := range folders {
			if strings.HasPrefix(c.Request.RequestURI, fmt.Sprintf("/%s/", folders[i])) {
				// In dev mode, avoid stale cached bundles causing frontend state mismatch.
				if flags.Dev {
					c.Header("Cache-Control", "max-age=0, no-cache, no-store, must-revalidate")
				} else {
					c.Header("Cache-Control", "public, max-age=15552000")
				}
			}
		}
	})
	for i, folder := range folders {
		sub, err := fs.Sub(static, folder)
		if err != nil {
			utils.Log.Fatalf("can't find folder: %s", folder)
		}
		r.StaticFS(fmt.Sprintf("/%s/", folders[i]), http.FS(sub))
	}

	noRoute(func(c *gin.Context) {
		// Compatibility fallback:
		// some frontend bundles may request these public APIs under unexpected prefixes
		// (for example "/@manage/public/settings"). We handle suffix matches here
		// to avoid returning index.html to XHR requests.
		reqPath := strings.TrimSuffix(c.Request.URL.Path, "/")
		if strings.HasSuffix(reqPath, "/public/settings") {
			c.JSON(http.StatusOK, gin.H{
				"code":    200,
				"message": "success",
				"data":    op.GetPublicSettingsMap(),
			})
			return
		}
		if strings.HasSuffix(reqPath, "/public/archive_extensions") {
			c.JSON(http.StatusOK, gin.H{
				"code":    200,
				"message": "success",
				"data":    []string{},
			})
			return
		}
		if strings.HasSuffix(reqPath, "/public/offline_download_tools") {
			c.JSON(http.StatusOK, gin.H{
				"code":    200,
				"message": "success",
				"data":    tool.Tools.Names(),
			})
			return
		}

		// Never cache HTML entry documents. They should always reference
		// the latest hashed assets after backend/frontend updates.
		c.Header("Cache-Control", "max-age=0, no-cache, no-store, must-revalidate")
		c.Header("Pragma", "no-cache")
		c.Header("Expires", "0")
		c.Header("Content-Type", "text/html")
		c.Status(200)
		if strings.HasPrefix(c.Request.URL.Path, "/@manage") {
			_, _ = c.Writer.WriteString(conf.ManageHtml)
		} else {
			_, _ = c.Writer.WriteString(conf.IndexHtml)
		}
		c.Writer.Flush()
		c.Writer.WriteHeaderNow()
	})
}
