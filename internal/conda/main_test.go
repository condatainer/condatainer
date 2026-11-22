package conda

import (
	"os"
	"testing"

	"github.com/condatainer/condatainer/internal/testenv"
)

func TestMain(m *testing.M) {
	testenv.LibexecOnPath()
	os.Exit(m.Run())
}
