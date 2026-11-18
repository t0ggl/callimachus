package converter

import (
	"github.com/t0ggl/callimachus/pkg/epub"
	"os/exec"
)

// Standard tests never consult or invoke the host Calibre installation.
func init() {
	epub.SetMockCalibreRunner(func(string) (string, error) { return "", exec.ErrNotFound }, func(string, ...string) ([]byte, error) { panic("unmocked Calibre invocation") })
}
