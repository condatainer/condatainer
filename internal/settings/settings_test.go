package settings

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
)

type fakeLayer struct {
	name  string
	texts map[string]string
	lists map[string][]string
}

func (f fakeLayer) Name() string { return f.name }
func (f fakeLayer) Text(k string) (string, bool) {
	v, ok := f.texts[k]
	return v, ok
}
func (f fakeLayer) List(k string) ([]string, bool) {
	v, ok := f.lists[k]
	return v, ok
}

// use installs layers for one test and clears the flag state when it ends.
func use(t *testing.T, ls ...Layer) {
	t.Helper()
	SetLayers(ls)
	t.Cleanup(func() {
		SetLayers(nil)
		stateMu.Lock()
		flags = map[string]flagValue{}
		warned = map[string]bool{}
		stateMu.Unlock()
	})
}

var (
	tBool   = Bool("test.bool", Default(true), Help("a bool"))
	tInt    = Int("test.int", Default(4), Min(1), Max(64), Help("an int"))
	tDays   = Days("test.days", Default(30), Min(0), Help("days"))
	tMem    = MemoryMB("test.mem", Default("12g"), Help("memory"))
	tTime   = Walltime("test.time", Default("2h"), Help("time"))
	tEnum   = Enum("test.enum", Values("auto", "ssh", "direct"), Default("auto"), Help("an enum"))
	tPath   = Path("test.path", Default("/tmp/x"), Help("a path"))
	tStr    = String("test.str", Default("none"), Help("a string"))
	tEmpty  = String("test.empty", Default("web"), AllowEmpty(), Help("may be empty"))
	tList   = List("test.list", DefaultList("a", "b"), Help("a list"))
	tNormal = String("test.normal", Default("x"), Normalize(strings.ToUpper), Help("normalized"))
	_       = Bool("test.group.one", Help("one"))
	_       = Bool("test.group.two", Help("two"))
)

func TestDefaultsBeforeAnyLoad(t *testing.T) {
	if !tBool.Get() || tInt.Get() != 4 || tDays.Get() != 30*24*time.Hour || tMem.Get() != 12288 ||
		tTime.Get() != 2*time.Hour || tEnum.Get() != "auto" || tStr.Get() != "none" || tNormal.Get() != "X" {
		t.Error("a default is not what its declaration says")
	}
	if !slices.Equal(tList.Get(), []string{"a", "b"}) {
		t.Errorf("list default = %v", tList.Get())
	}
}

func TestEveryDefaultRoundTrips(t *testing.T) {
	for _, k := range Keys() {
		if k.Default == "" {
			continue
		}
		again, err := k.Parse(k.Default)
		if err != nil || again != k.Default {
			t.Errorf("%s: default %q parses to %q, %v", k.Name, k.Default, again, err)
		}
	}
}

func TestKindParse(t *testing.T) {
	cases := []struct {
		key, in, want string
		bad           bool
	}{
		{"test.bool", "1", "true", false},
		{"test.bool", "F", "false", false},
		{"test.bool", "yes", "", true},
		{"test.int", "8", "8", false},
		{"test.int", "0", "", true},
		{"test.int", "65", "", true},
		{"test.int", "x", "", true},
		{"test.days", "0", "0", false},
		{"test.days", "-1", "", true},
		{"test.mem", "8g", "8g", false},
		{"test.mem", "8192", "8192", false},
		{"test.mem", "lots", "", true},
		{"test.time", "01:30:00", "01:30:00", false},
		{"test.time", "soon", "", true},
		{"test.enum", "SSH", "ssh", false},
		{"test.enum", "pigeon", "", true},
		{"test.path", "$HOME/x", "$HOME/x", false},
		{"test.path", "rel/x", "", true},
		{"test.str", "", "", true},
		{"test.empty", "", "", false},
		{"test.normal", "abc", "ABC", false},
	}
	for _, c := range cases {
		k, _ := Lookup(c.key)
		got, err := k.Parse(c.in)
		if (err != nil) != c.bad || (!c.bad && got != c.want) {
			t.Errorf("%s %q = %q, %v; want %q (bad=%v)", c.key, c.in, got, err, c.want, c.bad)
		}
	}
}

func TestResolveOrder(t *testing.T) {
	user := fakeLayer{name: "user", texts: map[string]string{"test.int": "8"}}
	site := fakeLayer{name: "app-root", texts: map[string]string{"test.int": "16"}}
	use(t, user, site)

	res, _ := Resolve("test.int")
	if res.Value != "8" || res.Source != SourceLayer || res.Layer != "user" ||
		len(res.Overridden) != 1 || res.Overridden[0] != (Entry{"app-root", "16"}) {
		t.Errorf("layers: %+v", res)
	}
	t.Setenv(EnvName("test.int"), "32")
	// The cache is keyed on a generation that only a layer or flag change bumps.
	SetLayers([]Layer{user, site})
	res, _ = Resolve("test.int")
	if res.Value != "32" || res.Source != SourceEnv || res.Env != "CNT_CONFIG_TEST_INT" || len(res.Overridden) != 2 {
		t.Errorf("env: %+v", res)
	}
	if err := setFlag("test.int", "ncpus", "2"); err != nil {
		t.Fatal(err)
	}
	if res, _ = Resolve("test.int"); res.Value != "2" || res.Source != SourceFlag || res.Flag != "ncpus" {
		t.Errorf("flag: %+v", res)
	}
	if tInt.Get() != 2 {
		t.Error("the handle reads the flag")
	}
}

func TestEmptyEnvCountsAsUnset(t *testing.T) {
	use(t, fakeLayer{name: "user", texts: map[string]string{"test.int": "8"}})
	t.Setenv(EnvName("test.int"), "")
	SetLayers([]Layer{layers[0]})
	if res, _ := Resolve("test.int"); res.Source != SourceLayer {
		t.Errorf("%+v", res)
	}
}

func TestAllowEmpty(t *testing.T) {
	use(t, fakeLayer{name: "user", texts: map[string]string{"test.empty": "", "test.str": ""}})
	if res, _ := Resolve("test.empty"); res.Source != SourceLayer || res.Value != "" {
		t.Errorf("an empty value of an AllowEmpty key is set: %+v", res)
	}
	if res, _ := Resolve("test.str"); res.Source != SourceDefault {
		t.Errorf("an empty value of another key is unset: %+v", res)
	}
}

func TestBadValueIsSkippedAndNextSourceApplies(t *testing.T) {
	use(t,
		fakeLayer{name: "user", texts: map[string]string{"test.int": "abc"}},
		fakeLayer{name: "app-root", texts: map[string]string{"test.int": "9"}})
	res, _ := Resolve("test.int")
	if res.Value != "9" || res.Layer != "app-root" {
		t.Errorf("%+v", res)
	}
	if !warned["bad|test.int|user"] {
		t.Error("the rejected value is reported")
	}
}

func TestListResolution(t *testing.T) {
	use(t, fakeLayer{name: "user", lists: map[string][]string{"test.list": {"x", "y"}}})
	if got := tList.Get(); !slices.Equal(got, []string{"x", "y"}) {
		t.Errorf("list = %v", got)
	}
}

func TestSection(t *testing.T) {
	var names []string
	for _, k := range Section("test.group") {
		names = append(names, k.Name)
	}
	if !slices.Equal(names, []string{"test.group.one", "test.group.two"}) {
		t.Errorf("section = %v", names)
	}
}

func TestOverrideRestores(t *testing.T) {
	restore, err := OverrideValue("test.int", "5")
	if err != nil || tInt.Get() != 5 {
		t.Fatalf("%v %d", err, tInt.Get())
	}
	restore()
	if tInt.Get() != 4 {
		t.Errorf("after restore = %d", tInt.Get())
	}
	if _, err := OverrideValue("test.int", "999"); err == nil {
		t.Error("an override the kind rejects is refused")
	}
	var unknown *UnknownKeyError
	if _, err := OverrideValue("no.such", "1"); !errors.As(err, &unknown) {
		t.Errorf("err = %v", err)
	}
}

func TestRegistrationRefusesBadDeclarations(t *testing.T) {
	for name, fn := range map[string]func(){
		"duplicate":   func() { Bool("test.bool", Help("x")) },
		"no help":     func() { Bool("test.nohelp") },
		"upper case":  func() { Bool("Test.Upper", Help("x")) },
		"env clash":   func() { Bool("test_bool", Help("x")) },
		"leaf+parent": func() { Bool("test.group", Help("x")) },
		"bad default": func() { Int("test.bad_default", Default("abc"), Help("x")) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", name)
				}
			}()
			fn()
		}()
	}
}

func TestFlagsWinAndRejectBadValues(t *testing.T) {
	use(t, fakeLayer{name: "user", texts: map[string]string{"test.int": "8"}})
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	AddFlag(fs, "test.int", "ncpus", Short("n"))
	AddFlag(fs, "test.bool", "flag-bool")
	AddSwitch(fs, "test.enum", "use-ssh", "ssh")
	AddSwitch(fs, "test.enum", "use-direct", "direct")

	if tInt.Get() != 8 {
		t.Fatal("an unset flag changes nothing")
	}
	fs.SetOutput(&strings.Builder{})
	if err := fs.Parse([]string{"-n", "0"}); err == nil {
		t.Error("a bad value fails at parse")
	}
	if err := fs.Parse([]string{"-n", "3", "--flag-bool=false", "--use-ssh"}); err != nil {
		t.Fatal(err)
	}
	if tInt.Get() != 3 || tBool.Get() || tEnum.Get() != "ssh" {
		t.Errorf("flags: %d %v %s", tInt.Get(), tBool.Get(), tEnum.Get())
	}
	err := fs.Parse([]string{"--use-direct"})
	var conflict *FlagConflictError
	if !errors.As(err, &conflict) {
		t.Errorf("two switches for one key: %v", err)
	}
}

func TestEnvNamesAreUnique(t *testing.T) {
	seen := map[string]string{}
	for _, k := range Keys() {
		if other, ok := seen[EnvName(k.Name)]; ok {
			t.Errorf("%s and %s share %s", other, k.Name, EnvName(k.Name))
		}
		seen[EnvName(k.Name)] = k.Name
	}
}

var (
	tDyn   = String("test.dyn", DefaultFunc(func() any { return os.Getenv("SETTINGS_TEST_DYN") }), Help("dynamic default"))
	tValid = String("test.valid", Default("ok"), Validate(func(s string) error {
		if s == "bad" {
			return errors.New("not allowed")
		}
		return nil
	}), Help("validated"))
	tChan = List("test.chan", DefaultList("x"), Help("channels"))
)

func TestDefaultFuncIsComputedOnRead(t *testing.T) {
	t.Setenv("SETTINGS_TEST_DYN", "first")
	if tDyn.Get() != "first" {
		t.Errorf("dyn = %q", tDyn.Get())
	}
	t.Setenv("SETTINGS_TEST_DYN", "second")
	SetLayers(nil) // the cache is per generation
	if tDyn.Get() != "second" {
		t.Errorf("dyn = %q", tDyn.Get())
	}
}

func TestValidateRejectsAfterTheKind(t *testing.T) {
	k, _ := Lookup("test.valid")
	if _, err := k.Parse("bad"); err == nil {
		t.Error("Validate is not applied")
	}
}

func TestListEnvSplitsOnPipeOrColon(t *testing.T) {
	use(t)
	t.Setenv(EnvName("test.chan"), "a:b")
	if got := tChan.Get(); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("colon: %v", got)
	}
	t.Setenv(EnvName("test.chan"), "a:b|c")
	SetLayers(nil)
	if got := tChan.Get(); !slices.Equal(got, []string{"a:b", "c"}) {
		t.Errorf("pipe wins: %v", got)
	}
}

func TestListFlagReplacesAndChangedFlagsRepeatsIt(t *testing.T) {
	use(t, fakeLayer{name: "user", lists: map[string][]string{"test.chan": {"cfg"}}})
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	AddFlag(fs, "test.chan", "channel", Short("c"))
	AddFlag(fs, "test.int", "ncpus")
	AddSwitch(fs, "test.enum", "use-ssh", "ssh")
	if err := fs.Parse([]string{"-c", "one", "-c", "two", "--ncpus", "3", "--use-ssh"}); err != nil {
		t.Fatal(err)
	}
	if got := tChan.Get(); !slices.Equal(got, []string{"one", "two"}) {
		t.Errorf("list flag = %v", got)
	}
	want := []string{"--channel", "one", "--channel", "two", "--ncpus", "3", "--use-ssh"}
	if got := ChangedFlags(fs, "test.chan", "test.int", "test.enum"); !slices.Equal(got, want) {
		t.Errorf("ChangedFlags = %v, want %v", got, want)
	}
	if got := ChangedFlags(fs, "test.int"); !slices.Equal(got, []string{"--ncpus", "3"}) {
		t.Errorf("only the asked keys: %v", got)
	}
}

var (
	tMerge = List("test.merge", MergeLists(), Split(func(s string) []string { return strings.Split(s, "|") }), Help("merged"))
	tNote  = Enum("test.note", Values("none", "web"), AllowEmpty(), Normalize(func(s string) string {
		if s == "" {
			return "none"
		}
		return s
	}), Default("web"), Help("empty means none"))
	tOmit = String("test.omit", AllowEmpty(), OmitEmpty(), Help("hidden while empty"))
)

func TestMergedListIsTheUnionHighestFirstWithOrigins(t *testing.T) {
	use(t,
		fakeLayer{name: "user", lists: map[string][]string{"test.merge": {"a", "b"}}},
		fakeLayer{name: "app-root", lists: map[string][]string{"test.merge": {"b", "c"}}})
	res, _ := Resolve("test.merge")
	if !slices.Equal(res.List, []string{"a", "b", "c"}) || !slices.Equal(res.Origins, []string{"user", "user", "app-root"}) {
		t.Errorf("%v from %v", res.List, res.Origins)
	}
	if len(res.Overridden) != 0 {
		t.Errorf("a merged list hides nothing: %v", res.Overridden)
	}
	t.Setenv(EnvName("test.merge"), "x|y")
	SetLayers(layers)
	if got := tMerge.Get(); !slices.Equal(got, []string{"x", "y"}) {
		t.Errorf("an environment variable replaces the merge: %v", got)
	}
}

func TestEnumAllowsEmptyAndNormalizesIt(t *testing.T) {
	use(t, fakeLayer{name: "user", texts: map[string]string{"test.note": ""}})
	if res, _ := Resolve("test.note"); res.Value != "none" || res.Source != SourceLayer {
		t.Errorf("%+v", res)
	}
}

func TestOmitEmptyIsReported(t *testing.T) {
	k, _ := Lookup("test.omit")
	if !k.OmitsEmpty() {
		t.Error("OmitEmpty is not recorded")
	}
	if tOmit.Get() != "" {
		t.Error("an unset AllowEmpty string reads empty")
	}
}
