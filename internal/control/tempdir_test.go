package control

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Windows directory deletion can lag behind completed atomic replacements.
// Retry cleanup of this test's own temporary directory; assertion failures remain failures.
func testTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Cleanup(func() {
			base, e := filepath.Abs(os.TempDir())
			if e != nil {
				t.Error("invalid temporary root")
				return
			}
			target, e := filepath.Abs(dir)
			if e != nil {
				t.Error("invalid temporary path")
				return
			}
			relative, e := filepath.Rel(base, target)
			if e != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
				t.Error("refusing cleanup outside test temporary root")
				return
			}
			for i := 0; i < 10; i++ {
				if os.RemoveAll(target) == nil {
					return
				}
				time.Sleep(100 * time.Millisecond)
			}
			t.Error("temporary directory cleanup remained unavailable")
		})
	}
	return dir
}
