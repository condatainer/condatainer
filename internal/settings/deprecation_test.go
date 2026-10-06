package settings

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

var (
	dNew = Bool("test.dep.new", Default(false), Help("renamed"),
		Replaces("test.dep.old", Since("0.5"), RemoveIn("0.7")))
	dMem = MemoryMB("test.dep.mem", Default("1g"), Help("migrated"),
		Replaces("test.dep.mem_mb", Since("0.5"), Migrate(func(old string) (string, error) {
			if old == "bad" {
				return "", errors.New("cannot convert")
			}
			return old + "m", nil
		})))
	dDiscouraged = String("test.dep.discouraged", Default("x"), Help("old way"),
		Deprecated("use test.dep.new instead", Since("0.5")))
	_ = Removed("test.dep.gone", "set test.dep.new instead", Since("0.5"))
)

// warnings collects what a test is told, and restores the printer afterwards.
func warnings(t *testing.T) *[]string {
	t.Helper()
	var got []string
	prev := warnFn
	warnFn = func(m string) { got = append(got, m) }
	t.Cleanup(func() { warnFn = prev })
	return &got
}

func TestOldNameInAFileReadsAsTheNewKeyInItsOwnLayer(t *testing.T) {
	got := warnings(t)
	use(t,
		fakeLayer{name: "user", texts: map[string]string{"test.dep.old": "true"}},
		fakeLayer{name: "app-root", texts: map[string]string{"test.dep.new": "false"}})
	res, _ := Resolve("test.dep.new")
	if res.Value != "true" || res.Layer != "user" || res.Via != "test.dep.old" {
		t.Errorf("%+v", res)
	}
	if len(*got) != 1 || !strings.Contains((*got)[0], "test.dep.old is deprecated since 0.5; use test.dep.new") {
		t.Errorf("warnings = %v", *got)
	}
	dNew.Get()
	if len(*got) != 1 {
		t.Errorf("the warning repeats: %v", *got)
	}
}

func TestNewNameWinsOverTheOldOneInOneLayer(t *testing.T) {
	warnings(t)
	use(t, fakeLayer{name: "user", texts: map[string]string{"test.dep.old": "true", "test.dep.new": "false"}})
	if res, _ := Resolve("test.dep.new"); res.Value != "false" || res.Via != "" {
		t.Errorf("%+v", res)
	}
}

func TestOldVariableReadsAsTheNewKeyAndTheNewVariableWins(t *testing.T) {
	got := warnings(t)
	use(t)
	t.Setenv(EnvName("test.dep.old"), "true")
	SetLayers(nil)
	res, _ := Resolve("test.dep.new")
	if res.Value != "true" || res.Source != SourceEnv || res.Via != "test.dep.old" || res.Env != EnvName("test.dep.old") {
		t.Errorf("%+v", res)
	}
	if len(*got) != 1 || !strings.Contains((*got)[0], "CNT_CONFIG_TEST_DEP_OLD is deprecated since 0.5; use CNT_CONFIG_TEST_DEP_NEW") {
		t.Errorf("warnings = %v", *got)
	}
	t.Setenv(EnvName("test.dep.new"), "false")
	SetLayers(nil)
	if res, _ := Resolve("test.dep.new"); res.Value != "false" || res.Env != EnvName("test.dep.new") {
		t.Errorf("both set: %+v", res)
	}
	if !slices.ContainsFunc(*got, func(m string) bool { return strings.Contains(m, "are both set") }) {
		t.Errorf("both variables set is not reported: %v", *got)
	}
}

func TestMigrateConvertsAnOldValueAndRefusesABadOne(t *testing.T) {
	warnings(t)
	use(t, fakeLayer{name: "user", texts: map[string]string{"test.dep.mem_mb": "512"}})
	if dMem.Get() != 512 {
		t.Errorf("migrated value = %d", dMem.Get())
	}
	use(t, fakeLayer{name: "user", texts: map[string]string{"test.dep.mem_mb": "bad"}})
	if dMem.Get() != 1024 {
		t.Errorf("a value that cannot migrate keeps the default: %d", dMem.Get())
	}
}

func TestRemovedKeyIsReportedAndIgnored(t *testing.T) {
	got := warnings(t)
	use(t, fakeLayer{name: "user", texts: map[string]string{"test.dep.gone": "x"}})
	if len(*got) != 1 || !strings.Contains((*got)[0], "test.dep.gone was removed since 0.5: set test.dep.new instead") {
		t.Errorf("warnings = %v", *got)
	}
	if r, ok := LookupRemoved("test.dep.gone"); !ok || r.Message == "" {
		t.Error("the removed key is not found")
	}
}

func TestDeprecatedKeyWarnsOnceWhenSet(t *testing.T) {
	got := warnings(t)
	use(t)
	dDiscouraged.Get()
	if len(*got) != 0 {
		t.Errorf("an unset deprecated key warned: %v", *got)
	}
	use(t, fakeLayer{name: "user", texts: map[string]string{"test.dep.discouraged": "y"}})
	dDiscouraged.Get()
	if len(*got) != 1 || !strings.Contains((*got)[0], "test.dep.discouraged is deprecated since 0.5: use test.dep.new instead") {
		t.Errorf("warnings = %v", *got)
	}
}

func TestLookupAliasFindsTheNewKey(t *testing.T) {
	k, a, ok := LookupAlias("test.dep.old")
	if !ok || k.Name != "test.dep.new" || a.Since != "0.5" || a.RemoveIn != "0.7" {
		t.Errorf("%v %+v %v", k, a, ok)
	}
	if _, _, ok := LookupAlias("test.dep.new"); ok {
		t.Error("a live key is not an alias")
	}
}

func TestAnAliasNeverCollidesWithALiveKeyOrVariable(t *testing.T) {
	for name, fn := range map[string]func(){
		"alias is a key":       func() { Bool("test.dep.x1", Help("x"), Replaces("test.bool", Since("1"))) },
		"alias env clash":      func() { Bool("test.dep.x2", Help("x"), Replaces("test_dep_old", Since("1"))) },
		"key is an alias":      func() { Bool("test.dep.old", Help("x")) },
		"key is removed":       func() { Bool("test.dep.gone", Help("x")) },
		"alias w/o since":      func() { Bool("test.dep.x3", Help("x"), Replaces("test.dep.x3old")) },
		"deprecated w/o since": func() { Bool("test.dep.x4", Help("x"), Deprecated("no")) },
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
