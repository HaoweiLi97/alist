package pikpak

import (
	"testing"

	"github.com/alist-org/alist/v3/pkg/utils"
)

func TestSplitFileAtNineGiB(t *testing.T) {
	chunks, err := SplitFile(9 * utils.GB)
	if err != nil {
		t.Fatalf("SplitFile() error = %v", err)
	}
	if len(chunks) == 0 || len(chunks) > 10000 {
		t.Fatalf("SplitFile() chunks = %d, want 1..10000", len(chunks))
	}
}
