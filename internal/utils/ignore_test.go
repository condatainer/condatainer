package utils

import (
	"reflect"
	"testing"
)

func TestIgnoreMatch(t *testing.T) {
	type probe struct {
		path  string
		isDir bool
		want  IgnoreMatch
	}
	for name, tc := range map[string]struct {
		patterns string
		probes   []probe
	}{
		"a bare name matches at any depth": {"venv", []probe{
			{"venv", true, IgnoreYes}, {"src/venv", true, IgnoreYes}, {"src/venv.py", false, IgnoreNone}}},
		"a trailing slash matches directories only": {"build/", []probe{
			{"build", true, IgnoreYes}, {"build", false, IgnoreNone}, {"a/build", true, IgnoreYes}}},
		"a leading slash anchors at the root": {"/tmp", []probe{
			{"tmp", true, IgnoreYes}, {"src/tmp", true, IgnoreNone}}},
		"a slash inside anchors at the root": {"src/tmp", []probe{
			{"src/tmp", false, IgnoreYes}, {"a/src/tmp", false, IgnoreNone}}},
		"star stays inside a component": {"*.py", []probe{
			{"a.py", false, IgnoreYes}, {"x/y/a.py", false, IgnoreYes}, {"a.pyc", false, IgnoreNone}}},
		"star in an anchored pattern": {"src/*.py", []probe{
			{"src/a.py", false, IgnoreYes}, {"src/x/a.py", false, IgnoreNone}}},
		"question mark is one character": {"a?.sh", []probe{
			{"ab.sh", false, IgnoreYes}, {"a.sh", false, IgnoreNone}, {"a/.sh", false, IgnoreNone}}},
		"a class and a range": {"[a-c]1.[!x]", []probe{
			{"b1.y", false, IgnoreYes}, {"d1.y", false, IgnoreNone}, {"b1.x", false, IgnoreNone}}},
		"a named class": {"[[:digit:]].sh", []probe{
			{"7.sh", false, IgnoreYes}, {"a.sh", false, IgnoreNone}}},
		"a bracket with no closing is literal": {"a[b", []probe{
			{"a[b", false, IgnoreYes}, {"ab", false, IgnoreNone}}},
		"a leading double star": {"**/logs", []probe{
			{"logs", true, IgnoreYes}, {"a/b/logs", true, IgnoreYes}}},
		"an inner double star matches zero or more directories": {"a/**/z", []probe{
			{"a/z", false, IgnoreYes}, {"a/b/c/z", false, IgnoreYes}, {"b/a/z", false, IgnoreNone}}},
		"a trailing double star matches what is inside": {"out/**", []probe{
			{"out/a", false, IgnoreYes}, {"out/a/b", false, IgnoreYes}, {"out", true, IgnoreNone}}},
		"a double star beside text reads as a star": {"a**b", []probe{
			{"axxb", false, IgnoreYes}, {"a/b", false, IgnoreNone}}},
		"a later pattern wins": {"*.py\n!keep.py\n", []probe{
			{"a.py", false, IgnoreYes}, {"keep.py", false, IgnoreNo}}},
		"the last of several matches wins": {"!keep.py\n*.py\n", []probe{
			{"keep.py", false, IgnoreYes}}},
		"comments and blanks are skipped": {"# venv\n\n   \nreal\n", []probe{
			{"venv", false, IgnoreNone}, {"real", false, IgnoreYes}}},
		"backslash escapes a leading hash and a bang": {"\\#a\n\\!b\n", []probe{
			{"#a", false, IgnoreYes}, {"!b", false, IgnoreYes}}},
		"trailing spaces are dropped unless escaped": {"a  \nb\\ \n", []probe{
			{"a", false, IgnoreYes}, {"b ", false, IgnoreYes}, {"b", false, IgnoreNone}}},
		"carriage returns are dropped": {"a\r\n", []probe{{"a", false, IgnoreYes}}},
		"a range that runs backwards matches nothing": {"[z-a]", []probe{
			{"a", false, IgnoreNone}, {"z", false, IgnoreNone}}},
	} {
		ig := ParseIgnore([]byte(tc.patterns))
		for _, p := range tc.probes {
			if got := ig.Match(p.path, p.isDir); got != p.want {
				t.Errorf("%s: %q dir=%v: got %v, want %v", name, p.path, p.isDir, got, p.want)
			}
		}
	}
}

func TestIgnoreNegations(t *testing.T) {
	ig := ParseIgnore([]byte("a\n# c\n!b\n\n!d\n"))
	if got, want := ig.Negations(), []int{3, 5}; !reflect.DeepEqual(got, want) {
		t.Errorf("negations %v, want %v", got, want)
	}
}
