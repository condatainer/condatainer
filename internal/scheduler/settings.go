package scheduler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/condatainer/condatainer/internal/settings"
	"github.com/condatainer/condatainer/internal/utils"
)

var (
	keySubmitJob = settings.Bool("scheduler.submit_job",
		settings.Order(1),
		settings.Default(true),
		settings.Help("Submit jobs to the scheduler. Turn off to build and run on this node."))

	keyBin = settings.String("scheduler.bin",
		settings.Order(2),
		settings.AllowEmpty(),
		settings.Help("Scheduler binary, such as sbatch. Found on PATH when empty."),
		settings.Detect(DetectBin))

	keyTimeout = settings.Int("scheduler.timeout",
		settings.Order(3),
		settings.Default(0), settings.Min(0),
		settings.Help("Seconds to wait for a scheduler command. 0 waits without limit."))

	keyAccount = settings.String("scheduler.account",
		settings.Order(4),
		settings.AllowEmpty(),
		settings.Help("Account to submit under. Empty uses the scheduler's own default."),
		settings.Show(schedulerDefault))

	keyPartition = settings.String("scheduler.partition",
		settings.Order(5),
		settings.AllowEmpty(),
		settings.Help("Partition or queue to submit to. Empty uses the scheduler's own default."),
		settings.Show(schedulerDefault))

	keyNcpu = settings.Int("scheduler.ncpu",
		settings.Order(6),
		settings.Default(1), settings.Min(1),
		settings.Help("CPUs for a job that has no scheduler directives of its own."))

	keyMem = settings.MemoryMB("scheduler.mem",
		settings.Order(7),
		settings.Default(2048),
		settings.Help("Memory for a job that has no scheduler directives of its own."),
		settings.Suggest("2g", "4g", "8g", "16g"))

	keyTime = settings.Walltime("scheduler.time",
		settings.Order(8),
		settings.Default("2h"),
		settings.Help("Time limit for a job that has no scheduler directives of its own."),
		settings.Suggest("1h", "2h", "4h", "8h"))

	keyExcludeNodes = settings.List("scheduler.exclude_nodes",
		settings.Order(9), settings.MergeLists(),
		settings.Validate(validateNodeName),
		settings.Help("Nodes jobs must not run on, as the scheduler names them. Every layer's list is merged. PBS refuses it."))

	keyProxyPerJob = settings.List("scheduler.proxy_per_job",
		settings.Order(10), settings.MergeLists(),
		settings.Validate(validateProxyJobKind),
		settings.Accepts("a list of create, helper, run or all"),
		settings.Suggest(ProxyAll, ProxyCreate, ProxyHelper, ProxyRun),
		settings.Help("Job kinds that get a proxy to the login node: create (includes restore), helper, run or all."))
)

func init() {
	keySubmitJob.SetShow(func(stored string) string {
		if stored == "true" && !Enabled() {
			return "true (disabled: scheduler not accessible)"
		}
		return stored
	})
	keyBin.SetShow(func(string) string {
		bin := Bin()
		if t := TypeFromBin(bin); bin != "" && t != "" {
			return bin + " (" + string(t) + ")"
		}
		return bin
	})
	keyTimeout.SetShow(func(stored string) string {
		if stored == "0" {
			return "0 (disabled)"
		}
		return utils.FormatDuration(CommandTimeout())
	})
}

func schedulerDefault(stored string) string {
	if stored == "" {
		return "(scheduler default)"
	}
	return stored
}

var (
	detectOnce sync.Once
	detected   string
)

// DetectBin returns the first of sbatch, qsub and bsub on PATH, or "".
func DetectBin() string {
	for _, name := range []string{"sbatch", "qsub", "bsub"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

// TypeFromBin names the scheduler a binary belongs to, or "" when it is not recognized.
func TypeFromBin(bin string) SchedulerType {
	switch filepath.Base(bin) {
	case "sbatch", "srun", "salloc", "scancel", "squeue":
		return SchedulerSLURM
	case "qsub", "qdel", "qstat":
		if _, ok := os.LookupEnv("SGE_ROOT"); ok {
			return "SGE"
		}
		return SchedulerPBS
	case "bsub", "bjobs", "bkill":
		return SchedulerLSF
	}
	return SchedulerUnknown
}

// Bin returns the configured scheduler binary. When none is set and submission is on, it is the one found on PATH.
func Bin() string {
	if bin := keyBin.Get(); bin != "" {
		return bin
	}
	if !keySubmitJob.Get() {
		return ""
	}
	detectOnce.Do(func() { detected = DetectBin() })
	return detected
}

// Enabled reports whether jobs are submitted: scheduler.submit_job is on and the binary can be run.
func Enabled() bool {
	if !keySubmitJob.Get() {
		return false
	}
	bin := Bin()
	return bin != "" && utils.ValidateBinary(bin)
}

// Account is the account a job is submitted under when none is given, "" for the scheduler's default.
func Account() string { return keyAccount.Get() }

// ExcludeNodes is the nodes every submitted job is kept off, in the scheduler's own names.
func ExcludeNodes() []string { return keyExcludeNodes.Get() }

// nodeNamePattern is a host name: letters, digits, dots, dashes and underscores. The
// name is written into a job script, so nothing the shell or a submit file reads is allowed.
var nodeNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func validateNodeName(name string) error {
	if !nodeNamePattern.MatchString(name) {
		return fmt.Errorf("%q is not a node name", name)
	}
	return nil
}

// ValidateNodeNames returns an error for the first name that is not a plain host name.
func ValidateNodeNames(names []string) error {
	for _, n := range names {
		if err := validateNodeName(n); err != nil {
			return err
		}
	}
	return nil
}

// Partition is the partition a job is submitted to when none is given, "" for the scheduler's default.
func Partition() string { return keyPartition.Get() }

// CommandTimeout is how long a scheduler command may take, 0 for no limit.
func CommandTimeout() time.Duration { return time.Duration(keyTimeout.Get()) * time.Second }

// The job kinds scheduler.proxy_per_job names. ProxyAll stands for every kind.
const (
	ProxyCreate = "create"
	ProxyHelper = "helper"
	ProxyRun    = "run"
	ProxyAll    = "all"
)

func validateProxyJobKind(kind string) error {
	switch kind {
	case ProxyCreate, ProxyHelper, ProxyRun, ProxyAll:
		return nil
	}
	return fmt.Errorf("%q is not a job kind: use create, helper, run or all", kind)
}

// ProxyPerJob reports whether each submitted job of the given kind starts its own proxy.
func ProxyPerJob(kind string) bool {
	return slices.ContainsFunc(keyProxyPerJob.Get(), func(k string) bool { return k == kind || k == ProxyAll })
}

// DefaultSpec is the resource request for a job that has no scheduler directives of its own.
func DefaultSpec() ResourceSpec {
	return ResourceSpec{CpusPerTask: keyNcpu.Get(), MemPerNodeMB: keyMem.Get(), Time: keyTime.Get()}
}
