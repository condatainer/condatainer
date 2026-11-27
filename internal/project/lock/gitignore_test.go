package lock

import (
	"reflect"
	"testing"
)

func TestIgnoreMatch(t *testing.T) {
	type probe struct {
		path  string
		isDir bool
		want  ignoreMatch
	}
	for name, tc := range map[string]struct {
		patterns string
		probes   []probe
	}{
		"a bare name matches at any depth": {"venv", []probe{
			{"venv", true, ignoreYes}, {"src/venv", true, ignoreYes}, {"src/venv.py", false, ignoreNone}}},
		"a trailing slash matches directories only": {"build/", []probe{
			{"build", true, ignoreYes}, {"build", false, ignoreNone}, {"a/build", true, ignoreYes}}},
		"a leading slash anchors at the root": {"/tmp", []probe{
			{"tmp", true, ignoreYes}, {"src/tmp", true, ignoreNone}}},
		"a slash inside anchors at the root": {"src/tmp", []probe{
			{"src/tmp", false, ignoreYes}, {"a/src/tmp", false, ignoreNone}}},
		"star stays inside a component": {"*.py", []probe{
			{"a.py", false, ignoreYes}, {"x/y/a.py", false, ignoreYes}, {"a.pyc", false, ignoreNone}}},
		"star in an anchored pattern": {"src/*.py", []probe{
			{"src/a.py", false, ignoreYes}, {"src/x/a.py", false, ignoreNone}}},
		"question mark is one character": {"a?.sh", []probe{
			{"ab.sh", false, ignoreYes}, {"a.sh", false, ignoreNone}, {"a/.sh", false, ignoreNone}}},
		"a class and a range": {"[a-c]1.[!x]", []probe{
			{"b1.y", false, ignoreYes}, {"d1.y", false, ignoreNone}, {"b1.x", false, ignoreNone}}},
		"a named class": {"[[:digit:]].sh", []probe{
			{"7.sh", false, ignoreYes}, {"a.sh", false, ignoreNone}}},
		"a bracket with no closing is literal": {"a[b", []probe{
			{"a[b", false, ignoreYes}, {"ab", false, ignoreNone}}},
		"a leading double star": {"**/logs", []probe{
			{"logs", true, ignoreYes}, {"a/b/logs", true, ignoreYes}}},
		"an inner double star matches zero or more directories": {"a/**/z", []probe{
			{"a/z", false, ignoreYes}, {"a/b/c/z", false, ignoreYes}, {"b/a/z", false, ignoreNone}}},
		"a trailing double star matches what is inside": {"out/**", []probe{
			{"out/a", false, ignoreYes}, {"out/a/b", false, ignoreYes}, {"out", true, ignoreNone}}},
		"a double star beside text reads as a star": {"a**b", []probe{
			{"axxb", false, ignoreYes}, {"a/b", false, ignoreNone}}},
		"a later pattern wins": {"*.py\n!keep.py\n", []probe{
			{"a.py", false, ignoreYes}, {"keep.py", false, ignoreNo}}},
		"the last of several matches wins": {"!keep.py\n*.py\n", []probe{
			{"keep.py", false, ignoreYes}}},
		"comments and blanks are skipped": {"# venv\n\n   \nreal\n", []probe{
			{"venv", false, ignoreNone}, {"real", false, ignoreYes}}},
		"backslash escapes a leading hash and a bang": {"\\#a\n\\!b\n", []probe{
			{"#a", false, ignoreYes}, {"!b", false, ignoreYes}}},
		"trailing spaces are dropped unless escaped": {"a  \nb\\ \n", []probe{
			{"a", false, ignoreYes}, {"b ", false, ignoreYes}, {"b", false, ignoreNone}}},
		"carriage returns are dropped": {"a\r\n", []probe{{"a", false, ignoreYes}}},
		"a range that runs backwards matches nothing": {"[z-a]", []probe{
			{"a", false, ignoreNone}, {"z", false, ignoreNone}}},
	} {
		ig := parseIgnore([]byte(tc.patterns))
		for _, p := range tc.probes {
			if got := ig.Match(p.path, p.isDir); got != p.want {
				t.Errorf("%s: %q dir=%v: got %v, want %v", name, p.path, p.isDir, got, p.want)
			}
		}
	}
}

func TestIgnoreNegations(t *testing.T) {
	ig := parseIgnore([]byte("a\n# c\n!b\n\n!d\n"))
	if got, want := ig.Negations(), []int{3, 5}; !reflect.DeepEqual(got, want) {
		t.Errorf("negations %v, want %v", got, want)
	}
}
