package cmd

import (
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/settings/settingstest"
	"github.com/spf13/pflag"
)

// A submitted job runs `condatainer run` again, so every flag set on the command
// line that is not a job flag must be in the command it runs, a flag added later
// included.
func TestSubmittedJobCarriesTheFlagsGiven(t *testing.T) {
	fs := pflag.NewFlagSet("run", pflag.ContinueOnError)
	fs.Bool("writable", false, "")
	fs.Bool("fakeroot", false, "")
	fs.StringArray("bind", nil, "")
	fs.StringArray("env", nil, "")
	fs.String("future", "", "")
	fs.String("project", "", "")
	fs.Int("cpu", 0, "")

	projectRootFlag = "/p r"
	t.Cleanup(func() { projectRootFlag = "" })
	plain, err := buildMpiRunCommand("/s.sh", forwardedFlagArgs(fs), []string{"a"}, nil, false)
	if err != nil || !strings.HasSuffix(plain, " run /s.sh 'a'") {
		t.Fatalf("no flags: %q, %v", plain, err)
	}

	for _, set := range [][2]string{
		{"writable", "true"}, {"bind", "/a b:/c"}, {"env", "K=it's"}, {"future", "x"}, {"project", "p"}, {"cpu", "4"},
	} {
		if err := fs.Set(set[0], set[1]); err != nil {
			t.Fatal(err)
		}
	}
	got, err := buildMpiRunCommand("/s.sh", forwardedFlagArgs(fs), nil, nil, false)
	want := ` run --bind='/a b:/c' --env='K=it'\''s' --future='x' --project='/p r' --writable='true' /s.sh`
	if err != nil || !strings.HasSuffix(got, want) {
		t.Fatalf("flags:\n got %q\nwant suffix %q (%v)", got, want, err)
	}
}

func TestLocalRunNote(t *testing.T) {
	for _, tc := range []struct {
		noScheduler, jobFlags bool
		want                  string
	}{
		{false, false, ""},
		{true, false, "No scheduler: running locally"},
		{true, true, "No scheduler: running locally, -o/-e/-t/-A/-p ignored"},
		{false, true, "-o/-e/-t/-A/-p ignored: only a submitted job uses them"},
	} {
		if got := localRunNote(tc.noScheduler, tc.jobFlags); got != tc.want {
			t.Errorf("noScheduler=%v jobFlags=%v: %q, want %q", tc.noScheduler, tc.jobFlags, got, tc.want)
		}
	}
}

// A submitted run carries --usage when it was typed or record_usage names run, and once.
func TestSubmittedRunCarriesTheUsageFlag(t *testing.T) {
	fs := pflag.NewFlagSet("run", pflag.ContinueOnError)
	fs.Bool("usage", false, "")
	fs.Bool("quiet", false, "")
	t.Cleanup(func() { runUsage = false })

	if got := submittedFlagArgs(fs); len(got) != 0 {
		t.Errorf("nothing asked for = %q", got)
	}

	settingstest.OverrideList(t, "record_usage", "run")
	if got := submittedFlagArgs(fs); len(got) != 1 || got[0] != "--usage" {
		t.Errorf("record_usage names run = %q, want --usage", got)
	}

	// -q is forwarded and --usage rides with it.
	if err := fs.Set("quiet", "true"); err != nil {
		t.Fatal(err)
	}
	if got := submittedFlagArgs(fs); len(got) != 2 || got[0] != "--quiet='true'" || got[1] != "--usage" {
		t.Errorf("under -q = %q", got)
	}

	// A typed flag is repeated once, not twice.
	settingstest.OverrideList(t, "record_usage", "none")
	if err := fs.Set("usage", "true"); err != nil {
		t.Fatal(err)
	}
	runUsage = true
	got := submittedFlagArgs(fs)
	if count := strings.Count(strings.Join(got, " "), "--usage"); count != 1 {
		t.Errorf("a typed flag appears %d times in %q, want once", count, got)
	}
}
