package helper

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/utils"
)

// HelperParam represents a single #PARAM: declaration from a helper script.
// Format: #PARAM: KEY=default --long-flag,-s "Description"
// Use KEY=? to mark the param as optional (passes through empty; script handles it).
type HelperParam struct {
	Key         string `json:"key"`
	Default     string `json:"default,omitempty"`
	Optional    bool   `json:"optional,omitempty"` // true when declared as KEY=?
	LongFlag    string `json:"long_flag,omitempty"`
	ShortFlag   string `json:"short_flag,omitempty"`
	Description string `json:"description,omitempty"`
}

// HelperScriptMeta holds overlay and singleton metadata extracted from a helper script.
type HelperScriptMeta struct {
	// EnvRequired is true when the #ENV_PACKAGES: header is present, meaning the
	// helper requires a writable overlay (.img) even if no packages are listed.
	EnvRequired bool
	// EnvPackages is a template of conda packages for guided overlay creation and
	// pre-submission package verification. {KEY} tokens are substituted from
	// resolved #PARAM: values before use. Empty when #ENV_PACKAGES: has no value.
	EnvPackages string
	// Deps are the overlays to load before the helper runs, one per #DEP: line, in the order written.
	// {KEY} tokens are substituted from resolved #PARAM: values (e.g. "r/{POSIT_R}").
	Deps []string
	// EnvPostInstall is run inside the container after micromamba installs EnvPackages.
	EnvPostInstall string
	// Singleton is true when at most one running instance of this helper is allowed.
	Singleton bool
	// Binds is a list of extra bind-mount specs from #BIND: headers, in "src:dest" form.
	// {KEY} tokens are substituted from resolved #PARAM: values before use.
	// $VAR references (e.g. $CNT_HELPER_STATE_DIR, $HOME) are passed verbatim and
	// expanded by the shell when the wrapper runs on the compute node.
	Binds []string
	// ParamValues holds the valid-value lists parsed from #VALUE: headers.
	// Keys are param names (e.g. "POSIT_R", "CONDA_PYTHON"); values are the parsed list.
	ParamValues map[string][]string
	// NCPU is the default CPU count from #NCPU:. Zero means not set.
	NCPU int
	// MemMB is the default memory in MB from #MEM:. Zero means not set.
	MemMB int64
	// Walltime is the default walltime from #TIME:. Zero means not set.
	Walltime time.Duration
	// GPU is the default GPU request from #GPU:. Nil means not set.
	GPU *scheduler.GpuSpec
}

// paramKeyPattern is the shape of a #PARAM: key. The key is exported into the helper's environment.
var paramKeyPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// reservedParamKeys are variables a #PARAM: key may not name: the job's resources and the process
// environment every tool reads.
var reservedParamKeys = map[string]bool{
	"NCPU": true, "MEM": true, "MEM_GB": true, "NNODE": true, "NRANK": true,
	"NRANK_PER_NODE": true, "OMP_NUM_THREADS": true, "PATH": true, "HOME": true, "TMPDIR": true,
}

// validateParamKey reports why key cannot name a #PARAM:, or nil.
//   - A key is upper case letters, digits and underscores, and does not start with a digit.
//   - CNT_ is CondaTainer's prefix, and the resource and process variables are reserved.
func validateParamKey(key string) error {
	switch {
	case !paramKeyPattern.MatchString(key):
		return fmt.Errorf("KEY %q must be upper case letters, digits and underscores, not starting with a digit", key)
	case strings.HasPrefix(key, "CNT_"):
		return fmt.Errorf("KEY %q starts with CNT_, which CondaTainer uses", key)
	case reservedParamKeys[key]:
		return fmt.Errorf("KEY %q is a reserved variable", key)
	}
	return nil
}

// ParseHelperParams extracts #PARAM: declarations from a helper script.
// Format per line: #PARAM: KEY=default --long-flag,-s "Description"
// - KEY=default: variable name and default value (empty after = means required)
// - --long-flag,-s: optional flag spec (comma-separated; either part may be omitted)
// - "Description": quoted description string
func ParseHelperParams(scriptPath string) ([]HelperParam, error) {
	data, err := readHelperScript(scriptPath)
	if err != nil {
		return nil, err
	}
	var params []HelperParam
	for _, a := range utils.SelectAnnotations(utils.ScanAnnotations(data), "#PARAM") {
		if a.Value == "" {
			continue
		}
		p, err := parseHelperParamLine(a.Value)
		if err != nil {
			return nil, fmt.Errorf("invalid #PARAM: on line %d: %w", a.Line, err)
		}
		params = append(params, p)
	}
	return params, nil
}

// readHelperScript reads a helper script, or says why it cannot.
func readHelperScript(scriptPath string) ([]byte, error) {
	if !utils.FileExists(scriptPath) {
		return nil, fmt.Errorf("helper script not found at %s", scriptPath)
	}
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read script: %w", err)
	}
	return data, nil
}

// parseHelperParamLine parses the content after "#PARAM: " into a HelperParam.
// Expected: KEY=default [--long-flag[,-s]] "Description"
func parseHelperParamLine(s string) (HelperParam, error) {
	// Extract trailing quoted description first.
	desc, rest, err := extractQuotedSuffix(s)
	if err != nil {
		return HelperParam{}, fmt.Errorf("missing quoted description: %w", err)
	}

	// Split remaining into whitespace tokens.
	tokens := strings.Fields(rest)
	if len(tokens) == 0 {
		return HelperParam{}, fmt.Errorf("missing KEY=default")
	}

	// First token must be KEY=value (no spaces allowed).
	keyVal := tokens[0]
	eqIdx := strings.IndexByte(keyVal, '=')
	if eqIdx < 0 {
		return HelperParam{}, fmt.Errorf("KEY=default token missing '=': %q", keyVal)
	}
	key := keyVal[:eqIdx]
	def := keyVal[eqIdx+1:]
	optional := false
	if def == "?" {
		def = ""
		optional = true
	}
	if key == "" {
		return HelperParam{}, fmt.Errorf("empty KEY in: %q", keyVal)
	}
	if err := validateParamKey(key); err != nil {
		return HelperParam{}, err
	}

	// Remaining tokens are optional flag specs: "--long,-s" or "--long" or "-s"
	var longFlag, shortFlag string
	for _, tok := range tokens[1:] {
		parts := strings.SplitN(tok, ",", 2)
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if strings.HasPrefix(p, "--") {
				longFlag = p
			} else if strings.HasPrefix(p, "-") && len(p) == 2 {
				shortFlag = p
			}
		}
	}

	return HelperParam{
		Key:         key,
		Default:     def,
		Optional:    optional,
		LongFlag:    longFlag,
		ShortFlag:   shortFlag,
		Description: desc,
	}, nil
}

// extractQuotedSuffix finds and removes the last double-quoted string from s.
// Returns (quoted content, remainder, error).
func extractQuotedSuffix(s string) (string, string, error) {
	s = strings.TrimSpace(s)
	end := len(s) - 1
	if end < 1 || s[end] != '"' {
		return "", "", fmt.Errorf("no closing quote found")
	}
	start := strings.LastIndex(s[:end], "\"")
	if start < 0 {
		return "", "", fmt.Errorf("no opening quote found")
	}
	quoted := s[start+1 : end]
	rest := strings.TrimSpace(s[:start])
	return quoted, rest, nil
}

// helperKeys are the headers a helper reads. One after the header block is reported, not dropped silently.
var helperKeys = []string{"#DESC", "#PARAM", "#VALUE", "#NCPU", "#MEM", "#TIME", "#GPU", "#SINGLETON", "#BIND", "#DEP", "#ENV_PACKAGES", "#ENV_POST_INSTALL"}

// unhonoredHeaders are the shared headers a helper does not read: the launch or the wrapper decides them.
var unhonoredHeaders = []string{"#NAME", "#OUTPUT", "#ERROR", "#PARTITION", "#ACCOUNT", "#WRITABLE", "#FAKEROOT"}

// ParseHelperScriptMeta extracts metadata from a helper script.
// Recognised headers: #NCPU:, #MEM:, #TIME:, #GPU:, #DEP:, #ENV_PACKAGES:,
// #ENV_POST_INSTALL:, #SINGLETON:, #BIND:, #VALUE:.
//   - A resource header or #SINGLETON: value that does not parse is an error naming its line.
//   - A header the launch decides (#NAME:, #OUTPUT:, #ERROR:, #PARTITION:, #ACCOUNT:, #WRITABLE:, #FAKEROOT:) is an error naming its line.
//   - The metadata that did parse is returned with the error, so a caller that ignores the error still gets it.
func ParseHelperScriptMeta(scriptPath string) (HelperScriptMeta, error) {
	data, err := readHelperScript(scriptPath)
	if err != nil {
		return HelperScriptMeta{}, err
	}

	annotations := utils.ScanAnnotations(data)
	utils.WarnStray(scriptPath, utils.StrayAnnotations(data, helperKeys...))
	resources, problem := scheduler.ParseResourceHeaders(annotations)
	problems := []error{problem, utils.Reject(annotations, "a helper script", unhonoredHeaders...)}
	meta := HelperScriptMeta{
		ParamValues: make(map[string][]string),
		Deps:        utils.Dependencies(annotations),
		NCPU:        resources.CpusPerTask,
		MemMB:       resources.MemPerNodeMB,
		Walltime:    resources.Time,
		GPU:         resources.Gpu,
	}
	if meta.Singleton, err = utils.Flag(annotations, "#SINGLETON"); err != nil {
		problems = append(problems, err)
	}
	for _, a := range annotations {
		switch a.Key {
		case "#ENV_PACKAGES":
			meta.EnvRequired = true
			meta.EnvPackages = a.Value
		case "#BIND":
			if a.Value != "" {
				meta.Binds = append(meta.Binds, a.Value)
			}
		case "#ENV_POST_INSTALL":
			meta.EnvPostInstall = a.Value
		case "#VALUE":
			key, spec, ok := strings.Cut(a.Value, "=")
			if ok && strings.TrimSpace(key) != "" {
				// An empty spec yields a nil list: the param simply has no choices.
				meta.ParamValues[strings.TrimSpace(key)] = utils.ParseValueList(spec)
			}
		}
	}
	return meta, errors.Join(problems...)
}

// ScriptDescription returns a script's first #DESC: value, or "".
func ScriptDescription(scriptPath string) string {
	text, err := os.ReadFile(scriptPath)
	if err != nil {
		return ""
	}
	description, _ := utils.FindAnnotation(utils.ScanAnnotations(text), "#DESC")
	return description
}
