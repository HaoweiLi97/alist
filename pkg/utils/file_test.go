package utils

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/alist-org/alist/v3/internal/conf"
	"github.com/alist-org/alist/v3/internal/errs"
)

func TestCreateTempFileRejectsIncompleteStreamAndCleansUp(t *testing.T) {
	tempDir := t.TempDir()
	oldConf := conf.Conf
	conf.Conf = &conf.Config{TempDir: tempDir}
	t.Cleanup(func() { conf.Conf = oldConf })

	file, err := CreateTempFile(strings.NewReader("short"), 10)
	if file != nil {
		t.Fatalf("CreateTempFile() file = %v, want nil", file)
	}
	if !errors.Is(err, errs.StreamIncomplete) {
		t.Fatalf("CreateTempFile() error = %v, want StreamIncomplete", err)
	}
	entries, readErr := os.ReadDir(tempDir)
	if readErr != nil {
		t.Fatalf("ReadDir() error = %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary files were not cleaned up: %v", entries)
	}
}
