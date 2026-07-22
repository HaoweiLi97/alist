package tool

import (
	"testing"

	"github.com/xhofe/tache"
)

func TestFindInFlightDownloadTask(t *testing.T) {
	previousManager := DownloadTaskManager
	DownloadTaskManager = tache.NewManager[*DownloadTask](tache.WithRunning(false))
	defer func() { DownloadTaskManager = previousManager }()

	existing := &DownloadTask{
		Url:          "https://example.com/video.mkv",
		DstDirPath:   "/downloads",
		Toolname:     "ThunderBrowser",
		DeletePolicy: DeleteNever,
	}
	DownloadTaskManager.Add(existing)

	matched := findInFlightDownloadTask(
		" https://example.com/video.mkv ",
		"/downloads",
		"ThunderBrowser",
		DeleteNever,
		nil,
	)
	if matched != existing {
		t.Fatal("expected matching in-flight download task")
	}

	existing.SetState(tache.StateSucceeded)
	if matched := findInFlightDownloadTask(
		"https://example.com/video.mkv",
		"/downloads",
		"ThunderBrowser",
		DeleteNever,
		nil,
	); matched != nil {
		t.Fatal("completed task must not be reused")
	}
}
