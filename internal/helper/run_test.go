package helper

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/settings/settingstest"
)

// checkAndInstallNamedOverlays resolves a bare, distro-unprefixed name
// against what's already installed via catalog.SolveName, with no build and
// no "condatainer create" subprocess when a local overlay already satisfies
// it.
func TestCheckAndInstallNamedOverlaysReusesInstalled(t *testing.T) {
	recipesRoot := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(recipesRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("recipes/ubuntu24/rstudio-server.def",
		"#DESC:RStudio Server {version}\n#TARGET:ubuntu24/rstudio-server/{version}\n#PH:version:2025.05.0-160,2026.09.0-174\n")

	imagesDir := t.TempDir()
	installedName := "ubuntu24--rstudio-server--2026.09.0-174.sqf"
	if err := os.WriteFile(filepath.Join(imagesDir, installedName), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	prevSources := config.Global.Sources
	prevPaths := config.GlobalDataPaths
	config.Global.Sources = []catalog.Spec{{Name: "test", Base: recipesRoot}}
	settingstest.Override(t, "default_distro", "ubuntu24")
	config.GlobalDataPaths.ImagesDirs = []string{imagesDir}
	config.ResetCatalog()
	t.Cleanup(func() {
		config.Global.Sources = prevSources
		config.GlobalDataPaths = prevPaths
		config.ResetCatalog()
	})

	paths, err := checkAndInstallNamedOverlays(context.Background(), []string{"rstudio-server"}, "ubuntu24")
	if err != nil {
		t.Fatalf("checkAndInstallNamedOverlays: %v", err)
	}
	want := filepath.Join(imagesDir, installedName)
	if len(paths) != 1 || paths[0] != want {
		t.Errorf("paths = %v, want [%s] — should reuse the installed overlay with no build", paths, want)
	}
}

// A TERM sent to the wrapper while the container command runs is forwarded to
// it, and the wrapper waits for it to exit before recording done.
func TestWrapperForwardsTermAndWaits(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "events")
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("condatainer", "#!/bin/bash\ncase $1 in\n_pick_port) echo 12345;;\n_server_done) echo \"done $*\" >> "+log+";;\nesac\n")
	child := write("child.sh", "#!/bin/bash\ntrap 'echo got-term >> "+log+"; sleep 1; echo exited >> "+log+"; exit 0' TERM\necho started >> "+log+"\nwhile :; do sleep 0.1; done\n")

	stateDir := filepath.Join(dir, "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := buildHelperCommandBody("t-1", "t", dir, dir, stateDir, time.Hour, nil, nil, child, false)
	wrapper := write("wrapper.sh", "#!/bin/bash\n"+body)

	cmd := exec.Command("bash", wrapper)
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "TMPDIR="+dir)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor := func(want string) {
		t.Helper()
		for i := 0; i < 100; i++ {
			if data, _ := os.ReadFile(log); strings.Contains(string(data), want) {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		data, _ := os.ReadFile(log)
		t.Fatalf("never saw %q; events:\n%s", want, data)
	}
	waitFor("started")
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()

	data, _ := os.ReadFile(log)
	got := string(data)
	for _, want := range []string{"got-term", "exited", "done _server_done --exit-code 130"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q; events:\n%s", want, got)
		}
	}
	if strings.Index(got, "exited") > strings.Index(got, "done _server_done") {
		t.Fatalf("done was recorded before the container exited:\n%s", got)
	}
}

func TestHelperBodyRecordsTheNodeBeforeSettingUp(t *testing.T) {
	body := buildHelperCommandBody("t-1", "t", "/w", "/w", "/w/state", time.Hour, nil, nil, "true", false)
	started, port := strings.Index(body, "condatainer _server_started\n"), strings.Index(body, "_pick_port")
	if started < 0 || started > port {
		t.Errorf("_server_started should come before _pick_port:\n%s", body)
	}
}

// A headless helper exports the resource variables of its spec, as a scheduler job script does.
func TestGenerateWrapperHeadlessExportsResources(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	spec := &scheduler.ResourceSpec{Nodes: 1, Ntasks: 1, TasksPerNode: 1, CpusPerTask: 4, MemPerNodeMB: 8192, Time: time.Hour}
	path, err := generateWrapper("demo-1", "demo", t.TempDir(), t.TempDir(), stateDir, "", "", nil, time.Hour, nil, spec, nil, "true")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"export NCPU=4\n", "export CNT_NCPU=4\n", "export MEM=8192\n", "export OMP_NUM_THREADS=4\n"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("job.sh lacks %q:\n%s", want, data)
		}
	}
}
