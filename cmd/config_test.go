package cmd

import (
	"slices"
	"sort"
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
		"build.always_submit_data CNT_CONFIG_BUILD_ALWAYS_SUBMIT_DATA",
		"build.block_size CNT_CONFIG_BUILD_BLOCK_SIZE",
		"build.compress_args CNT_CONFIG_BUILD_COMPRESS_ARGS",
		"build.data_block_size CNT_CONFIG_BUILD_DATA_BLOCK_SIZE",
		"build.logs_dir CNT_CONFIG_BUILD_LOGS_DIR",
		"build.mem CNT_CONFIG_BUILD_MEM",
		"build.ncpus CNT_CONFIG_BUILD_NCPUS",
		"build.system_apptainer CNT_CONFIG_BUILD_SYSTEM_APPTAINER",
		"build.time CNT_CONFIG_BUILD_TIME",
		"channels CNT_CONFIG_CHANNELS",
		"default_distro CNT_CONFIG_DEFAULT_DISTRO",
		"helper.connect CNT_CONFIG_HELPER_CONNECT",
		"helper.notification CNT_CONFIG_HELPER_NOTIFICATION",
		"home_override CNT_CONFIG_HOME_OVERRIDE",
		"metadata_cache_ttl CNT_CONFIG_METADATA_CACHE_TTL",
		"nested_run CNT_CONFIG_NESTED_RUN",
		"scheduler.account CNT_CONFIG_SCHEDULER_ACCOUNT",
		"scheduler.bin CNT_CONFIG_SCHEDULER_BIN",
		"scheduler.mem CNT_CONFIG_SCHEDULER_MEM",
		"scheduler.ncpus CNT_CONFIG_SCHEDULER_NCPUS",
		"scheduler.partition CNT_CONFIG_SCHEDULER_PARTITION",
		"scheduler.proxy_perjob CNT_CONFIG_SCHEDULER_PROXY_PERJOB",
		"scheduler.slurm.emit_mem CNT_CONFIG_SCHEDULER_SLURM_EMIT_MEM",
		"scheduler.submit_job CNT_CONFIG_SCHEDULER_SUBMIT_JOB",
		"scheduler.time CNT_CONFIG_SCHEDULER_TIME",
		"scheduler.timeout CNT_CONFIG_SCHEDULER_TIMEOUT",
		"store_gc_grace CNT_CONFIG_STORE_GC_GRACE",
	}
	if !slices.Equal(got, want) {
		t.Errorf("registered keys = %v, want %v", got, want)
	}
}
