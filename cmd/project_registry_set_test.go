package cmd

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/project/lock"
)

func TestProjectRegistrySetRecordsTheCodeURL(t *testing.T) {
	root := newProject(t)
	cmd := newProjectRegistrySetCmd()
	if cmd.Flags().Lookup("source") != nil {
		t.Error("project registry set still has --source")
	}
	if err := cmd.Flags().Set("code-url", "https://github.com/my-lab/rnaseq"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, []string{"ghcr.io/my-lab/rnaseq/cnt"}); err != nil {
		t.Fatal(err)
	}
	l, err := lock.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if l.Source != "https://github.com/my-lab/rnaseq" || l.OCI.Push != "ghcr.io/my-lab/rnaseq/cnt" {
		t.Errorf("lock source %q, push %q", l.Source, l.OCI.Push)
	}
}

func TestProjectRegistrySetSaysWhenTheSourceCameFromOrigin(t *testing.T) {
	root := newProject(t)
	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", "git@github.com:my-lab/rnaseq.git"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v: %s", err, out)
		}
	}
	out, err := captureStderr(t, "project", "registry", "set", "ghcr.io/my-lab/rnaseq/cnt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "https://github.com/my-lab/rnaseq") || !strings.Contains(out, "taken from the origin remote") {
		t.Errorf("output does not say the source came from origin:\n%s", out)
	}

	out, err = captureStderr(t, "project", "registry", "set", "ghcr.io/my-lab/rnaseq/cnt", "--code-url", "https://example.org/x/y")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "origin remote") || !strings.Contains(out, "https://example.org/x/y") {
		t.Errorf("an explicit --code-url was reported as from origin:\n%s", out)
	}
}

func TestProjectRegistrySetNotesAnOriginWithAPort(t *testing.T) {
	root := newProject(t)
	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", "https://git.lab.example:8443/team/p.git"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v: %s", err, out)
		}
	}
	out, err := captureStderr(t, "project", "registry", "set", "ghcr.io/my-lab/rnaseq/cnt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "names a port") || !strings.Contains(out, "--code-url") || strings.Contains(out, "  source ") {
		t.Errorf("output does not note the port:\n%s", out)
	}
}

// captureStderr executes the project command tree with args and returns what it printed to stderr.
func captureStderr(t *testing.T, args ...string) (string, error) {
	t.Helper()
	stderr := os.Stderr
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = write
	_, runErr := run(t, args...)
	write.Close()
	os.Stderr = stderr
	var out strings.Builder
	buf := make([]byte, 4096)
	for {
		n, readErr := read.Read(buf)
		out.Write(buf[:n])
		if readErr != nil {
			break
		}
	}
	read.Close()
	return out.String(), runErr
}
