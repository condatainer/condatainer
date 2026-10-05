package scheduler

import (
	"bytes"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/utils"
)

func TestWriteJobHeaderReplacesHome(t *testing.T) {
	t.Cleanup(func(prev string) func() { return func() { JobHome = prev } }(JobHome))

	JobHome = ""
	var plain bytes.Buffer
	writeJobHeader(&plain, "$ID", nil, nil, nil)
	if strings.Contains(plain.String(), "HOME") {
		t.Errorf("header mentions HOME with no home_override:\n%s", plain.String())
	}

	JobHome = "/scratch/u/home"
	var out bytes.Buffer
	writeJobHeader(&out, "$ID", nil, nil, nil)
	for _, want := range []string{
		`export CNT_REAL_HOME="${CNT_REAL_HOME:-${HOME:-}}"`,
		`export HOME='/scratch/u/home'`,
		`mkdir -p "$HOME"`,
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("header lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Index(out.String(), "export HOME=") > strings.Index(out.String(), "_START_TIME") {
		t.Error("HOME is replaced after the job body starts")
	}
}

func TestCommandEnvRestoresRealHome(t *testing.T) {
	t.Setenv(utils.EnvRealHome, "")
	if env := commandEnv(); env != nil {
		t.Errorf("commandEnv without a replaced HOME = %d entries, want nil", len(env))
	}

	t.Setenv("HOME", "/scratch/u/home")
	t.Setenv(utils.EnvRealHome, "/home/u")
	var homes []string
	for _, entry := range commandEnv() {
		if strings.HasPrefix(entry, "HOME=") {
			homes = append(homes, entry)
		}
	}
	if len(homes) != 1 || homes[0] != "HOME=/home/u" {
		t.Errorf("HOME entries = %v, want exactly HOME=/home/u", homes)
	}
}
