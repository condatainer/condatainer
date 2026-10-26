package cmd

import (
	"strings"
	"testing"

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
	want := ` run --bind='/a b:/c' --env='K=it'\''s' --future='x' --writable='true' /s.sh`
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
		{true, true, "No scheduler: running locally, -o/-e/-m/-t/-g/-A/-p ignored"},
		{false, true, "-o/-e/-m/-t/-g/-A/-p are only used for submitted jobs and are ignored when running locally"},
	} {
		if got := localRunNote(tc.noScheduler, tc.jobFlags); got != tc.want {
			t.Errorf("noScheduler=%v jobFlags=%v: %q, want %q", tc.noScheduler, tc.jobFlags, got, tc.want)
		}
	}
}
