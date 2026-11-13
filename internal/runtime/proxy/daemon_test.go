package proxy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/scheduler"
)

// A failed login is reported as soon as ssh exits, with what ssh printed,
// instead of after the listener timeout.
func TestStartSocksSSHFailsFastWhenSSHExits(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\necho 'Permission denied (publickey)' >&2\nexit 255\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))

	sock := filepath.Join(t.TempDir(), "s.sock")
	start := time.Now()
	_, _, _, err := startSocksSSH(sock, "unix", sock, []string{"-N", "node"}, func() {})
	if err == nil || !strings.Contains(err.Error(), "Permission denied") {
		t.Fatalf("err = %v, want ssh's message", err)
	}
	if elapsed := time.Since(start); elapsed > tunnelStartTimeout/2 {
		t.Fatalf("took %s, should not wait out the timeout", elapsed)
	}
}

func TestResolveViaHost(t *testing.T) {
	t.Setenv("SLURM_JOB_ID", "1")
	for _, tc := range []struct {
		name, flag, recorded, want string
		wantErr                    bool
	}{
		{name: "the flag wins", flag: "flag-node", recorded: "login01", want: "flag-node"},
		{name: "the recorded login node", recorded: "login01", want: "login01"},
		{name: "neither is known", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(scheduler.LoginNodeVar, tc.recorded)
			if tc.recorded == "" {
				os.Unsetenv(scheduler.LoginNodeVar)
			}
			got, err := ResolveViaHost(tc.flag)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Errorf("ResolveViaHost = %q, %v; want %q, error %v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}
