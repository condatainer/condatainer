package tool

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestMain doubles as the helper: with CNT_BOUND_HELPER set the test binary
// runs a bound "sleep" and reports its pid, so the parent can kill it.
func TestMain(m *testing.M) {
	if os.Getenv("CNT_BOUND_HELPER") != "" {
		RunCommandBound(context.Background(), "test", "", "sleep", "60")
		return
	}
	os.Exit(m.Run())
}

func TestRunCommandBoundDiesWithParent(t *testing.T) {
	helper := exec.Command(os.Args[0])
	helper.Env = append(os.Environ(), "CNT_BOUND_HELPER=1")
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}

	var child string
	for i := 0; i < 50 && child == ""; i++ {
		time.Sleep(50 * time.Millisecond)
		out, _ := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", helper.Process.Pid, helper.Process.Pid))
		child = strings.TrimSpace(string(out))
	}
	if child == "" {
		t.Skip("cannot see the helper's child")
	}
	pid, _ := strconv.Atoi(strings.Fields(child)[0])

	helper.Process.Kill()
	helper.Wait()
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); os.IsNotExist(err) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("child %d outlived its parent", pid)
}
