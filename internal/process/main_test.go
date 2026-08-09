package process

import (
	"fmt"
	"os"
	"testing"

	fakeruntime "github.com/dsdred/goal/testdata/fake-runtime/testutil"
)

func TestMain(m *testing.M) {
	code := m.Run()
	if err := fakeruntime.Cleanup(); err != nil {
		fmt.Fprintf(os.Stderr, "fake runtime cleanup: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}
