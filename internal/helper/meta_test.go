package helper

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseHelperParamLineKeys(t *testing.T) {
	cases := []struct {
		key string
		ok  bool
	}{
		{"PASSWORD", true},
		{"POSIT_R", true},
		{"_X1", true},
		{"password", false},
		{"1ABC", false},
		{"A-B", false},
		{"CNT_HELPER_PORT", false},
		{"NCPU", false},
		{"MEM_GB", false},
		{"OMP_NUM_THREADS", false},
		{"PATH", false},
		{"HOME", false},
		{"TMPDIR", false},
	}
	for _, c := range cases {
		_, err := parseHelperParamLine(c.key + `=x --opt "A param"`)
		if (err == nil) != c.ok {
			t.Errorf("key %q: err = %v, want ok = %v", c.key, err, c.ok)
		}
	}
}

func writeHelper(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "demo")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseHelperScriptMetaReadsHeadersLikeARecipe(t *testing.T) {
	path := writeHelper(t, `#!/bin/bash
  #NCPU: 4 ## cores for the service
#MEM: 8G
#TIME: 2
#BIND: /data#1:/ref
#ENV_POST_INSTALL: echo "a#b"
#PARAM: COLOR=#ff0000 --color "A colour" ## shown in help
`)
	meta, err := ParseHelperScriptMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if meta.NCPU != 4 || meta.MemMB != 8*1024 || meta.Walltime != 2*time.Hour {
		t.Errorf("resources = %d cpu, %d MB, %v", meta.NCPU, meta.MemMB, meta.Walltime)
	}
	if len(meta.Binds) != 1 || meta.Binds[0] != "/data#1:/ref" || meta.EnvPostInstall != `echo "a#b"` {
		t.Errorf("a single # was cut from a value: binds %q, post-install %q", meta.Binds, meta.EnvPostInstall)
	}
	params, err := ParseHelperParams(path)
	if err != nil || len(params) != 1 || params[0].Default != "#ff0000" || params[0].Description != "A colour" {
		t.Errorf("params = %+v, err = %v", params, err)
	}
}

func TestParseHelperScriptMetaNamesAnUnparsableValue(t *testing.T) {
	path := writeHelper(t, "#NCPU: 4\n#MEM: lots\n#TIME: soon\n")
	meta, err := ParseHelperScriptMeta(path)
	if err == nil || !strings.Contains(err.Error(), "#MEM on line 2") || !strings.Contains(err.Error(), "#TIME on line 3") {
		t.Fatalf("error = %v, want both bad lines named", err)
	}
	if meta.NCPU != 4 {
		t.Errorf("NCPU = %d; the value that parsed is still returned", meta.NCPU)
	}
}

func TestPlanRunRefusesAnUnparsableHeader(t *testing.T) {
	path := writeHelper(t, "#MEM: lots\n")
	_, err := PlanRun(t.Context(), RunOptions{ScriptPath: path, ScriptName: "demo"})
	if err == nil || !strings.Contains(err.Error(), "helper demo") || !strings.Contains(err.Error(), "#MEM on line 1") {
		t.Fatalf("error = %v, want the helper and the bad line named", err)
	}
}

func TestHelperMetaReadsSharedHeaders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "svc")
	write := func(text string) (HelperScriptMeta, error) {
		if err := os.WriteFile(path, []byte(text), 0o755); err != nil {
			t.Fatal(err)
		}
		return ParseHelperScriptMeta(path)
	}

	meta, err := write("#!/bin/bash\n#NCPU: 4\n#GPU: a100\n#SINGLETON:\n")
	if err != nil || meta.NCPU != 4 || meta.GPU == nil || meta.GPU.Type != "a100" || !meta.Singleton {
		t.Fatalf("meta = %+v, %v", meta, err)
	}

	meta, err = write("#!/bin/bash\n#NCPU: 4\n#SINGLETON: yes\n")
	if err == nil || !strings.Contains(err.Error(), "#SINGLETON on line 3") || meta.NCPU != 4 {
		t.Fatalf("a bad #SINGLETON: meta = %+v, err = %v", meta, err)
	}

	_, err = write("#!/bin/bash\n#GPU: a100:0\n#NAME: job\n#WRITABLE:\n")
	if err == nil || !strings.Contains(err.Error(), "#GPU on line 2") || strings.Contains(err.Error(), "#NAME") || strings.Contains(err.Error(), "#WRITABLE") {
		t.Fatalf("a bad #GPU: with headers the launch decides: err = %v", err)
	}
}

func TestHelperMetaReadsDepsAndEnvHeaders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "svc")
	text := "#!/bin/bash\n#DEP: base\n#DEP: r/{POSIT_R}\n#DEP: base\n#ENV_PACKAGES:\n#ENV_POST_INSTALL: mm pin add r-base\n"
	if err := os.WriteFile(path, []byte(text), 0o755); err != nil {
		t.Fatal(err)
	}
	meta, err := ParseHelperScriptMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(meta.Deps, []string{"base", "r/{POSIT_R}"}) {
		t.Errorf("Deps = %v, want the lines in order, once each, with the token kept", meta.Deps)
	}
	if !meta.EnvRequired || meta.EnvPackages != "" || meta.EnvPostInstall != "mm pin add r-base" {
		t.Errorf("env = %v %q %q, want the overlay required with no packages", meta.EnvRequired, meta.EnvPackages, meta.EnvPostInstall)
	}
	if got := fillDeps(meta.Deps, map[string]string{"POSIT_R": "4.4.3"}); !slices.Equal(got, []string{"base", "r/4.4.3"}) {
		t.Errorf("fillDeps = %v", got)
	}
}
