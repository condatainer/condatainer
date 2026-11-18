package slot

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/utils"
)

func parse(t *testing.T, text string) ([]Decl, error) {
	t.Helper()
	return ParseHelper(utils.ScanAnnotations([]byte(text)))
}

// parseScript reads slots as a script does, where optional= is not an option.
func parseScript(t *testing.T, text string) ([]Decl, error) {
	t.Helper()
	return Parse(utils.ScanAnnotations([]byte(text)))
}

func TestParseReadsEveryOption(t *testing.T) {
	decls, err := parse(t, "#!/bin/bash\n"+
		"#SLOT: genome default=grch38/genome/gencode choices=grch38/genome/gencode,grcm39/genome/gencode env=GENOME_FASTA,GENOME_FAI ## note\n"+
		"#SLOT: trimmer bin=trim_galore optional=true\n"+
		"#SLOT: annotation default=grch38/gtf/gencode/47 choices=grch38/gtf/gencode/47 strict=false\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(decls) != 3 {
		t.Fatalf("decls = %+v", decls)
	}
	g := decls[0]
	if g.Name != "genome" || g.Default != "grch38/genome/gencode" || !g.Strict || g.Line != 2 ||
		!slices.Equal(g.Choices, []string{"grch38/genome/gencode", "grcm39/genome/gencode"}) ||
		!slices.Equal(g.Env, []string{"GENOME_FASTA", "GENOME_FAI"}) {
		t.Errorf("genome = %+v", g)
	}
	if tr := decls[1]; !tr.Optional || !slices.Equal(tr.Bin, []string{"trim_galore"}) || tr.Default != "" {
		t.Errorf("trimmer = %+v", tr)
	}
	if decls[2].Strict {
		t.Errorf("annotation strict = true, want strict=false to turn it off")
	}
}

func TestParseRefuses(t *testing.T) {
	for name, tc := range map[string]struct{ text, want string }{
		"bad name":              {"#SLOT: Genome\n", "not a slot name"},
		"duplicate":             {"#SLOT: g\n#SLOT: g\n", "already declared on line 2"},
		"strict alone":          {"#SLOT: g strict=false\n", "strict= needs choices="},
		"optional with default": {"#SLOT: g optional=true default=a/b\n", "cannot have a default"},
		"default not a choice":  {"#SLOT: g default=a/b choices=c/d\n", "is not one of choices="},
		"constraint":            {"#SLOT: g default=a/b>=1\n", "version constraint"},
		"token":                 {"#SLOT: g default=a/{X}\n", "{KEY} token"},
		"empty entry":           {"#SLOT: g env=A,,B\n", "an entry is empty"},
		"bad variable":          {"#SLOT: g env=1BAD\n", "not a variable name"},
		"bad command":           {"#SLOT: g bin=bin/tool\n", "not a command name"},
		"unknown option":        {"#SLOT: g colour=red\n", "not an option"},
		"bare option":           {"#SLOT: g optional\n", "write optional=true"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parse(t, "#!/bin/bash\n"+tc.text)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestCoversMatchesWholeParts(t *testing.T) {
	choices := []string{"grch38/genome/gencode", "grcm39/genome/gencode"}
	for name, want := range map[string]bool{
		"grch38/genome/gencode":        true,
		"grch38/genome/gencode/49":     true,
		"grcm39/genome/gencode":        true,
		"grch38/genome/gencode2":       false,
		"grch38/genome":                false,
		"grch38_grcm39/genome/gencode": false,
	} {
		if got := Covers(choices, name); got != want {
			t.Errorf("Covers(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestBind(t *testing.T) {
	decls, err := parse(t, "#SLOT: genome default=grch38/genome/gencode choices=grch38/genome/gencode,grcm39/genome/gencode\n"+
		"#SLOT: index\n#SLOT: extra optional=true\n")
	if err != nil {
		t.Fatal(err)
	}

	bound, err := Bind(decls, []string{"genome=grcm39/genome/gencode", "index=star/2.7"})
	if err != nil {
		t.Fatal(err)
	}
	if len(bound) != 3 || bound[0].Requested != "grcm39/genome/gencode" || bound[0].Source != FromFlag ||
		bound[1].Requested != "star/2.7" || bound[2].Bound() {
		t.Errorf("bound = %+v, want the two flags and the optional slot left unbound", bound)
	}

	bound, err = Bind(decls, []string{"index=star/2.7"})
	if err != nil || bound[0].Requested != "grch38/genome/gencode" || bound[0].Source != FromDefault {
		t.Errorf("bound = %+v, err = %v, want the default", bound, err)
	}
}

func TestBindReportsUnboundSlots(t *testing.T) {
	decls, _ := parse(t, "#SLOT: genome\n#SLOT: index\n")
	bound, err := Bind(decls, []string{"genome=a/b"})
	var unbound *UnboundError
	if !errors.As(err, &unbound) || !errors.Is(err, ErrUnbound) || len(unbound.Slots) != 1 || unbound.Slots[0].Name != "index" {
		t.Fatalf("err = %v, want index reported unbound", err)
	}
	if len(bound) != 1 || bound[0].Name != "genome" {
		t.Errorf("bound = %+v, want the slot that did bind", bound)
	}
}

func TestBindRefuses(t *testing.T) {
	decls, _ := parse(t, "#SLOT: genome default=a/g choices=a/g,b/g\n#SLOT: open strict=false choices=a/x default=a/x\n")
	for name, tc := range map[string]struct {
		values []string
		want   string
	}{
		"not NAME=OVERLAY": {[]string{"genome"}, "is not NAME=OVERLAY"},
		"unknown slot":     {[]string{"index=a/b"}, `no slot "index" is declared; the script declares genome, open`},
		"twice":            {[]string{"genome=a/g", "genome=b/g"}, "bound twice"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Bind(decls, tc.values)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
	// strict=false lets any overlay in.
	if bound, err := Bind(decls, []string{"open=zzz/anything"}); err != nil || bound[1].Unlisted {
		t.Errorf("strict=false refused an unlisted overlay: %v", err)
	}
	if _, err := Bind(nil, []string{"x=y"}); err == nil || !strings.Contains(err.Error(), "declares no slots") {
		t.Errorf("a flag with no slots declared: %v", err)
	}
}

func TestShortNamesStandForTheirOverlay(t *testing.T) {
	decls, err := parse(t, "#!/bin/bash\n#SLOT: genome default=human choices=human=grch38/genome/gencode,mouse=grcm39/genome/gencode,grch38_grcm39/genome/gencode\n")
	if err != nil {
		t.Fatal(err)
	}
	d := decls[0]
	if d.Default != "grch38/genome/gencode" || d.Aliases["mouse"] != "grcm39/genome/gencode" ||
		!slices.Equal(d.Choices, []string{"grch38/genome/gencode", "grcm39/genome/gencode", "grch38_grcm39/genome/gencode"}) {
		t.Fatalf("decl = %+v, want overlays in Choices and Default, and the short names kept", d)
	}
	if got := d.ChoiceLabels(); got[0] != "human=grch38/genome/gencode" || got[2] != "grch38_grcm39/genome/gencode" {
		t.Errorf("labels = %v", got)
	}

	bound, err := Bind(decls, []string{"genome=mouse"})
	if err != nil || bound[0].Requested != "grcm39/genome/gencode" || bound[0].Source != FromFlag {
		t.Errorf("--slot genome=mouse: bound = %+v, err = %v", bound, err)
	}
	if bound, err = Bind(decls, nil); err != nil || bound[0].Requested != "grch38/genome/gencode" || bound[0].Source != FromDefault {
		t.Errorf("the default: bound = %+v, err = %v", bound, err)
	}
	// The full name still works, and a short name the slot does not define is refused as any other overlay.
	if bound, err = Bind(decls, []string{"genome=grcm39/genome/gencode"}); err != nil || bound[0].Requested != "grcm39/genome/gencode" {
		t.Errorf("the full name: bound = %+v, err = %v", bound, err)
	}
	if bound, err = Bind(decls, []string{"genome=rat"}); err != nil || !bound[0].Unlisted ||
		!strings.Contains(bound[0].Refusal("").Error(), "mouse=grcm39/genome/gencode") {
		t.Errorf("an unknown short name: bound = %+v, err = %v, want it unlisted and refused with the labels", bound, err)
	}
}

func TestShortNamesRefuse(t *testing.T) {
	for name, tc := range map[string]struct{ text, want string }{
		"bad short name":   {"#SLOT: g choices=Mouse=a/b\n", "not a short name"},
		"written twice":    {"#SLOT: g choices=m=a/b,m=c/d\n", "written twice"},
		"name is overlay":  {"#SLOT: g choices=star,star=a/b\n", "also an overlay in the list"},
		"name=version":     {"#SLOT: g choices=star=2.7.11b=x\n", "write the overlay as name/version"},
		"default= with =":  {"#SLOT: g default=a=b choices=a/b\n", "write the overlay as name/version"},
		"default unlisted": {"#SLOT: g default=rat choices=m=a/b\n", "is not one of choices="},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parse(t, "#!/bin/bash\n"+tc.text)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// A strict slot takes a choice by its full name or its short name. Any other value is checked after it resolves, so a
// partial tool or os name can still reach a choice, and what it resolves to must be exactly one of the choices.
func TestStrictLimitsTheChoicesNotTheCompletion(t *testing.T) {
	decls, err := parse(t, "#!/bin/bash\n#SLOT: genome choices=human=grch38/genome/gencode,grcm39/genome/gencode\n#SLOT: tool choices=samtools/1.24\n")
	if err != nil {
		t.Fatal(err)
	}
	listed := func(value string) bool {
		bound, err := Bind(decls, []string{"genome=" + value, "tool=samtools/1.24"})
		if err != nil {
			t.Fatalf("genome=%s: %v", value, err)
		}
		return !bound[0].Unlisted
	}
	for _, value := range []string{"human", "grch38/genome/gencode", "grcm39/genome/gencode"} {
		if !listed(value) {
			t.Errorf("genome=%s is a choice as written, but was marked unlisted", value)
		}
	}
	for _, value := range []string{"grch38/genome/gencode/49", "grch38", "hg19"} {
		if listed(value) {
			t.Errorf("genome=%s is not a choice as written, but was not marked unlisted", value)
		}
	}

	// A partial tool name resolves to a choice and passes. What resolves to anything else is refused.
	bound, _ := Bind(decls, []string{"genome=human", "tool=samtools/1"})
	if !bound[1].Unlisted || bound[1].CheckResolved("samtools/1.24") != nil {
		t.Errorf("samtools/1 resolved to the choice samtools/1.24 and should pass")
	}
	if err := bound[1].CheckResolved("samtools/1.25"); err == nil || !strings.Contains(err.Error(), "cannot be bound to samtools/1 (samtools/1.25)") {
		t.Errorf("samtools/1 resolved to a version the slot does not list: err = %v", err)
	}
	// A listed value is not checked again.
	if err := bound[0].CheckResolved("anything/else"); err != nil {
		t.Errorf("a listed value was checked again: %v", err)
	}
	// strict=false takes any overlay.
	open, _ := parse(t, "#!/bin/bash\n#SLOT: genome choices=human=grch38/genome/gencode strict=false\n")
	if bound, err := Bind(open, []string{"genome=grch38/genome/gencode/49"}); err != nil || bound[0].Unlisted {
		t.Errorf("strict=false: bound = %+v, err = %v", bound, err)
	}
}

// optional=true is for helpers. A script's slot is always bound, so the option is unknown there.
func TestOptionalIsForHelpersOnly(t *testing.T) {
	text := "#!/bin/bash\n#SLOT: extra optional=true\n"
	_, err := parseScript(t, text)
	if err == nil || !strings.Contains(err.Error(), `"optional" is not an option`) ||
		!strings.Contains(err.Error(), "valid options are bin, choices, default, env, strict") {
		t.Fatalf("a script: err = %v, want optional refused as an unknown option", err)
	}
	decls, err := parse(t, text)
	if err != nil || len(decls) != 1 || !decls[0].Optional {
		t.Errorf("a helper: decls = %+v, err = %v, want an optional slot", decls, err)
	}
}
