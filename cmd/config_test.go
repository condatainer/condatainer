package cmd

import (
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/condatainer/condatainer/internal/build"
	"github.com/condatainer/condatainer/internal/settings"
)

func TestConfigValueCompletion(t *testing.T) {
	opts := configValueCompletion("build.compress_args")
	expected := build.CompressNames()
	for _, e := range expected {
		found := false
		for _, o := range opts {
			if o == e {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected completion option %q not present", e)
		}
	}
}

func TestGetConfigEnvVars(t *testing.T) {
	vars := getConfigEnvVars()
	expected := make([]string, 0)
	for key := range knownConfigKeys() {
		env := settings.EnvName(key)
		expected = append(expected, env)
	}
	sort.Strings(expected)

	if len(vars) != len(expected) {
		t.Fatalf("got %d vars, expected %d", len(vars), len(expected))
	}
	for i, v := range vars {
		if v != expected[i] {
			t.Errorf("env var[%d] = %q, want %q", i, v, expected[i])
		}
	}
}

// Dropping an owner's import or declaration shows up here.
func TestRegisteredKeyNames(t *testing.T) {
	var got []string
	for _, k := range settings.Keys() {
		got = append(got, k.Name+" "+settings.EnvName(k.Name))
	}
	want := []string{
		"autoload_gpu CNT_CONFIG_AUTOLOAD_GPU",
		"bind CNT_CONFIG_BIND",
		"build.account CNT_CONFIG_BUILD_ACCOUNT",
		"build.always_submit CNT_CONFIG_BUILD_ALWAYS_SUBMIT",
		"build.block_size CNT_CONFIG_BUILD_BLOCK_SIZE",
		"build.compress_args CNT_CONFIG_BUILD_COMPRESS_ARGS",
		"build.data_block_size CNT_CONFIG_BUILD_DATA_BLOCK_SIZE",
		"build.logs_dir CNT_CONFIG_BUILD_LOGS_DIR",
		"build.mem CNT_CONFIG_BUILD_MEM",
		"build.ncpu CNT_CONFIG_BUILD_NCPU",
		"build.partition CNT_CONFIG_BUILD_PARTITION",
		"build.time CNT_CONFIG_BUILD_TIME",
		"channels CNT_CONFIG_CHANNELS",
		"default_distro CNT_CONFIG_DEFAULT_DISTRO",
		"helper.connect CNT_CONFIG_HELPER_CONNECT",
		"helper.history_limit CNT_CONFIG_HELPER_HISTORY_LIMIT",
		"helper.notification CNT_CONFIG_HELPER_NOTIFICATION",
		"home_override CNT_CONFIG_HOME_OVERRIDE",
		"host_apptainer CNT_CONFIG_HOST_APPTAINER",
		"host_run.launch CNT_CONFIG_HOST_RUN_LAUNCH",
		"host_run.on CNT_CONFIG_HOST_RUN_ON",
		"metadata_cache_ttl CNT_CONFIG_METADATA_CACHE_TTL",
		"record_usage CNT_CONFIG_RECORD_USAGE",
		"scheduler.account CNT_CONFIG_SCHEDULER_ACCOUNT",
		"scheduler.bin CNT_CONFIG_SCHEDULER_BIN",
		"scheduler.exclude_nodes CNT_CONFIG_SCHEDULER_EXCLUDE_NODES",
		"scheduler.mem CNT_CONFIG_SCHEDULER_MEM",
		"scheduler.ncpu CNT_CONFIG_SCHEDULER_NCPU",
		"scheduler.partition CNT_CONFIG_SCHEDULER_PARTITION",
		"scheduler.proxy_per_job CNT_CONFIG_SCHEDULER_PROXY_PER_JOB",
		"scheduler.slurm.emit_mem CNT_CONFIG_SCHEDULER_SLURM_EMIT_MEM",
		"scheduler.submit_job CNT_CONFIG_SCHEDULER_SUBMIT_JOB",
		"scheduler.time CNT_CONFIG_SCHEDULER_TIME",
		"scheduler.timeout CNT_CONFIG_SCHEDULER_TIMEOUT",
		"server.download_warn_files CNT_CONFIG_SERVER_DOWNLOAD_WARN_FILES",
		"server.download_warn_size CNT_CONFIG_SERVER_DOWNLOAD_WARN_SIZE",
		"server.require_token CNT_CONFIG_SERVER_REQUIRE_TOKEN",
		"server.token_days CNT_CONFIG_SERVER_TOKEN_DAYS",
		"store_gc_grace CNT_CONFIG_STORE_GC_GRACE",
	}
	if !slices.Equal(got, want) {
		t.Errorf("registered keys = %v, want %v", got, want)
	}
}

// The registry says how a list key's layers combine, so no help text states it by hand.
func TestNoHelpStatesHowLayersCombine(t *testing.T) {
	for _, k := range settings.Keys() {
		help := strings.ToLower(k.Help)
		if strings.Contains(help, "layer's list") || strings.Contains(help, "highest-priority file") {
			t.Errorf("%s: the help states the layer rule, which the key's LayerRule prints: %q", k.Name, k.Help)
		}
	}
}

// The reference and the key's help print the layer rule of a list key, and nothing for another key.
func TestReferenceAndHelpPrintTheLayerRule(t *testing.T) {
	for _, tc := range []struct {
		key, want string
	}{
		{"bind", "- **Layers:** merged"},
		{"host_run.launch", "- **Layers:** merged"},
		{"channels", "- **Layers:** replaced"},
		{"build.ncpu", ""},
	} {
		k, ok := settings.Lookup(tc.key)
		if !ok {
			t.Fatalf("%s is not registered", tc.key)
		}
		var b strings.Builder
		writeReferenceKey(&b, k)
		if got := strings.Contains(b.String(), "**Layers:**"); got != (tc.want != "") {
			t.Errorf("%s: reference has a Layers line = %v\n%s", tc.key, got, b.String())
		}
		if tc.want != "" && !strings.Contains(b.String(), tc.want) {
			t.Errorf("%s: reference lacks %q\n%s", tc.key, tc.want, b.String())
		}
	}
}
