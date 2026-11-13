package scheduler

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/condatainer/condatainer/internal/settings"
	"github.com/condatainer/condatainer/internal/utils"
)

var emitMem = settings.Bool("scheduler.slurm.emit_mem",
	settings.Default(true), settings.Order(9),
	settings.Help("Write --mem or --mem-per-cpu in generated Slurm scripts. Turn off where memory is not a scheduled resource."))

// slurmBlacklistedFlags lists SLURM directives that describe CPU topology / task
// distribution that cannot be reliably translated. Any match leaves the resource directives unparsed.
var slurmBlacklistedFlags = []string{
	"--gpus-per-socket",
	"--sockets-per-node",
	"--cores-per-socket",
	"--threads-per-core",
	"--ntasks-per-socket",
	"--ntasks-per-core",
	"--distribution",
}

// SlurmScheduler implements the Scheduler interface for SLURM
type SlurmScheduler struct {
	sbatchBin         string
	sinfoCommand      string
	srunCommand       string
	directiveRe       *regexp.Regexp
	jobIDRe           *regexp.Regexp
	cachedClusterInfo *ClusterInfo
}

// NewSlurmScheduler creates a new SLURM scheduler instance using sbatch from PATH
func NewSlurmScheduler() (*SlurmScheduler, error) {
	return newSlurmSchedulerWithBinary("")
}

// NewSlurmSchedulerWithBinary creates a SLURM scheduler using an explicit sbatch path
func NewSlurmSchedulerWithBinary(sbatchBin string) (*SlurmScheduler, error) {
	return newSlurmSchedulerWithBinary(sbatchBin)
}

func newSlurmSchedulerWithBinary(sbatchBin string) (*SlurmScheduler, error) {
	binPath := sbatchBin
	if binPath == "" {
		var err error
		binPath, err = exec.LookPath("sbatch")
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrSchedulerNotFound, err)
		}
	} else {
		if absPath, err := filepath.Abs(binPath); err == nil {
			binPath = absPath
		}
		info, err := os.Stat(binPath)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrSchedulerNotFound, err)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("%w: %s is a directory", ErrSchedulerNotFound, binPath)
		}
	}

	sinfoCmd := siblingBin(binPath, "sinfo")

	return &SlurmScheduler{
		sbatchBin:    binPath,
		sinfoCommand: sinfoCmd,
		srunCommand:  siblingBin(binPath, "srun"),
		directiveRe:  regexp.MustCompile(`^\s*#SBATCH\s+(.+)$`),
		jobIDRe:      regexp.MustCompile(`Submitted batch job (\d+)`),
	}, nil
}

// JobIDVar returns the name of the SLURM job ID environment variable.
func (s *SlurmScheduler) JobIDVar() string { return "SLURM_JOB_ID" }

// GetCurrentJobID returns the SLURM job ID of the currently running job, or "".
func (s *SlurmScheduler) GetCurrentJobID() string { return os.Getenv(s.JobIDVar()) }
func (s *SlurmScheduler) JobIDEnvExpr() string    { return "${" + s.JobIDVar() + "}" }

// NodeNameEnvExpr is the name the node is registered under.
func (s *SlurmScheduler) NodeNameEnvExpr() string { return "${SLURMD_NODENAME}" }

// TmpDirVar returns the name of the SLURM per-job node-local tmpdir env var.
func (s *SlurmScheduler) TmpDirVar() string { return "SLURM_TMPDIR" }

// GetTmpDir returns the SLURM node-local tmp directory for the current job, or "".
func (s *SlurmScheduler) GetTmpDir() string { return os.Getenv(s.TmpDirVar()) }

// JobExecCommand returns the srun prefix that starts a step inside a running job.
func (s *SlurmScheduler) JobExecCommand(jobID string) ([]string, error) {
	if s.srunCommand == "" {
		return nil, fmt.Errorf("%w: srun not found", ErrExecUnsupported)
	}
	return []string{s.srunCommand, "--jobid", jobID, "--overlap", "--nodes=1", "--ntasks=1"}, nil
}

// IsAvailable checks if the SLURM binary is present on this system.
func (s *SlurmScheduler) IsAvailable() bool {
	return s.sbatchBin != ""
}

// IsInsideJob returns true if the current process is running inside a SLURM job.
func (s *SlurmScheduler) IsInsideJob() bool {
	_, ok := os.LookupEnv("SLURM_JOB_ID")
	return ok
}

func (s *SlurmScheduler) GetType() SchedulerType { return SchedulerSLURM }
func (s *SlurmScheduler) GetBinary() string      { return s.sbatchBin }

// GetVersion returns the SLURM version string, or "" on failure.
func (s *SlurmScheduler) GetVersion(ctx context.Context) string {
	if s.sbatchBin == "" {
		return ""
	}
	version, err := s.getSlurmVersion(ctx)
	if err != nil {
		return ""
	}
	return version
}

// getSlurmVersion attempts to get the SLURM version
func (s *SlurmScheduler) getSlurmVersion(ctx context.Context) (string, error) {
	output, err := runCommand(ctx, "SLURM", "get-version", s.sbatchBin, "--version")
	if err != nil {
		return "", err
	}

	// Parse version from output like "slurm 23.02.6"
	versionStr := strings.TrimSpace(string(output))
	parts := strings.Fields(versionStr)
	if len(parts) >= 2 {
		return parts[1], nil
	}

	return versionStr, nil
}

// ReadScriptSpecs parses #SBATCH directives from a build script
func (s *SlurmScheduler) ReadScriptSpecs(scriptPath string) (*ScriptSpecs, error) {
	lines, err := readFileLines(scriptPath)
	if err != nil {
		return nil, err
	}
	lines = headerLines(scriptPath, lines, s.extractDirectives)
	return parseScript(scriptPath, lines, s.extractDirectives, s.parseRuntimeConfig, s.parseResourceSpec)
}

// extractDirectives extracts raw directive strings from script lines (strips the #SBATCH prefix).
func (s *SlurmScheduler) extractDirectives(lines []string) []string {
	var out []string
	for _, line := range lines {
		if m := s.directiveRe.FindStringSubmatch(line); m != nil {
			out = append(out, utils.StripInlineComment(m[1]))
		}
	}
	return out
}

// parseRuntimeConfig consumes job control directives (name, I/O, email) from the directive list.
// Returns the populated RuntimeConfig, unconsumed directives, and any critical error.
func (s *SlurmScheduler) parseRuntimeConfig(directives []string) (RuntimeConfig, []string, error) {
	var rc RuntimeConfig
	var unconsumed []string

	for _, flag := range directives {
		consumed := true
		switch {
		case flagMatches(flag, "--job-name", "-J"):
			rc.JobName, _ = flagValue(flag, "--job-name", "-J")
		case flagMatches(flag, "--chdir", "-D"):
			v, _ := flagValue(flag, "--chdir", "-D")
			rc.WorkDir = absPath(v)
		case flagMatches(flag, "--output", "-o"):
			rc.Stdout, _ = flagValue(flag, "--output", "-o")
		case flagMatches(flag, "--error", "-e"):
			rc.Stderr, _ = flagValue(flag, "--error", "-e")
		case flagMatches(flag, "--partition", "-p"):
			rc.Partition, _ = flagValue(flag, "--partition", "-p")
		case flagMatches(flag, "--account", "-A"):
			rc.Account, _ = flagValue(flag, "--account", "-A")
		case flagMatches(flag, "--exclude", "-x"):
			v, _ := flagValue(flag, "--exclude", "-x")
			rc.ExcludeNodes = splitSlurmNodeList(v)
		case flagMatches(flag, "--mail-user"):
			rc.MailUser, _ = flagValue(flag, "--mail-user")
		case flagMatches(flag, "--mail-type"):
			rawMailType, _ := flagValue(flag, "--mail-type")
			mailType := strings.ToUpper(rawMailType)
			if mailType == "NONE" {
				rc.EmailOnBegin = false
				rc.EmailOnEnd = false
				rc.EmailOnFail = false
			} else {
				for _, t := range strings.Split(mailType, ",") {
					switch strings.TrimSpace(t) {
					case "ALL":
						rc.EmailOnBegin = true
						rc.EmailOnEnd = true
						rc.EmailOnFail = true
					case "BEGIN":
						rc.EmailOnBegin = true
					case "END":
						rc.EmailOnEnd = true
					case "FAIL", "REQUEUE", "INVALID_DEPEND":
						rc.EmailOnFail = true
					}
				}
			}
		default:
			consumed = false
		}
		if !consumed {
			unconsumed = append(unconsumed, flag)
		}
	}
	return rc, unconsumed, nil
}

// parseResourceSpec consumes the compute resource directives and returns the ResourceSpec with the unused directives.
//   - It returns nil on a blacklisted flag or an unresolvable interdependency.
//   - Phase 1 scans every directive into temporaries.
//   - Phase 2 resolves in dependency order: Nodes and Tasks, GPU, CPU, memory, time.
func (s *SlurmScheduler) parseResourceSpec(directives []string) (*ResourceSpec, []string) {
	defaults := GetSpecDefaults()

	// ── Phase 1: Scan ────────────────────────────────────────────────────────
	var (
		nodes            int = defaults.Nodes
		ntasksPerNode    int
		totalNtasks      int
		hasNtasksPerNode bool
		hasTotalNtasks   bool

		cpusPerTask    int = defaults.CpusPerTask
		hasCpusPerTask bool
		cpusPerGpu     int
		hasCpusPerGpu  bool

		// GPU: at most one form expected; last one seen wins in Phase 1.
		// Resolution priority in Phase 2: gres > per-node > total > per-task.
		gpuGresStr      string // --gres=gpu:... (per-node by SLURM definition)
		gpuPerNodeStr   string // --gpus-per-node=...
		gpuTotalStr     string // --gpus=... (total across all nodes)
		gpuPerTaskStr   string // --gpus-per-task=...
		ntasksPerGpu    int
		hasNtasksPerGpu bool

		memStr       string // --mem
		memPerCpuStr string // --mem-per-cpu (resolved after CPU)
		memPerGpuStr string // --mem-per-gpu (resolved after GPU)
		timeStr      string

		exclusive bool
	)

	var unconsumed []string

	for _, flag := range directives {
		// Blacklist: topology flags we cannot translate → unparsed immediately.
		for _, bl := range slurmBlacklistedFlags {
			if flag == bl || flagMatches(flag, bl) {
				logParseWarning("SLURM: unsupported topology directive %q; resource directives not parsed", flag)
				return nil, directives
			}
		}

		consumed := true
		var parseErr error

		switch {
		case flagMatches(flag, "--nodes", "-N"):
			_, parseErr = flagScanInt(flag, &nodes, "--nodes", "-N")
		case flagMatches(flag, "--ntasks-per-node"):
			_, parseErr = flagScanInt(flag, &ntasksPerNode, "--ntasks-per-node")
			hasNtasksPerNode = true
		case flagMatches(flag, "--ntasks", "-n"):
			_, parseErr = flagScanInt(flag, &totalNtasks, "--ntasks", "-n")
			hasTotalNtasks = true
		case flagMatches(flag, "--cpus-per-task", "-c"):
			_, parseErr = flagScanInt(flag, &cpusPerTask, "--cpus-per-task", "-c")
			hasCpusPerTask = true
		case flagMatches(flag, "--cpus-per-gpu"):
			_, parseErr = flagScanInt(flag, &cpusPerGpu, "--cpus-per-gpu")
			hasCpusPerGpu = true
		case strings.HasPrefix(flag, "--gres=gpu:"):
			gpuGresStr = strings.TrimPrefix(flag, "--gres=")
		case flagMatches(flag, "--gpus"):
			gpuTotalStr, _ = flagValue(flag, "--gpus")
		case flagMatches(flag, "--gpus-per-node"):
			gpuPerNodeStr, _ = flagValue(flag, "--gpus-per-node")
		case flagMatches(flag, "--gpus-per-task"):
			gpuPerTaskStr, _ = flagValue(flag, "--gpus-per-task")
		case flagMatches(flag, "--ntasks-per-gpu"):
			_, parseErr = flagScanInt(flag, &ntasksPerGpu, "--ntasks-per-gpu")
			hasNtasksPerGpu = true
		case flagMatches(flag, "--mem"):
			memStr, _ = flagValue(flag, "--mem")
		case flagMatches(flag, "--mem-per-cpu"):
			memPerCpuStr, _ = flagValue(flag, "--mem-per-cpu")
		case flagMatches(flag, "--mem-per-gpu"):
			memPerGpuStr, _ = flagValue(flag, "--mem-per-gpu")
		case flagMatches(flag, "--time", "-t"):
			timeStr, _ = flagValue(flag, "--time", "-t")
		case flag == "--exclusive", strings.HasPrefix(flag, "--exclusive="):
			exclusive = true
		default:
			consumed = false
		}

		if parseErr != nil {
			logParseWarning("SLURM: failed to parse directive %q: %v", flag, parseErr)
			return nil, directives
		}
		if !consumed {
			unconsumed = append(unconsumed, flag)
		}
	}

	// ── Phase 2: Resolve in dependency order ─────────────────────────────────
	rs := &ResourceSpec{
		CpusPerTask:  defaults.CpusPerTask,
		TasksPerNode: defaults.TasksPerNode,
		Nodes:        defaults.Nodes,
		MemPerNodeMB: defaults.MemPerNodeMB,
		Time:         defaults.Time,
		Exclusive:    exclusive,
	}

	// Conflict Check
	if gpuPerTaskStr != "" && hasNtasksPerGpu {
		logParseWarning("SLURM: --gpus-per-task and --ntasks-per-gpu are mutually exclusive; resource directives not parsed")
		return nil, directives
	}

	// 1. Nodes
	rs.Nodes = nodes

	// 2. GPU Resolution
	switch {
	case gpuGresStr != "":
		gpu, err := parseSlurmGpu(gpuGresStr)
		if err != nil {
			logParseWarning("SLURM: failed to parse --gres value %q: %v; resource directives not parsed", gpuGresStr, err)
			return nil, directives
		}
		if rs.Nodes <= 0 {
			rs.Nodes = 1
		}
		rs.Gpu = gpu
	case gpuPerNodeStr != "":
		gpu, err := parseSlurmGpu(gpuPerNodeStr)
		if err != nil {
			logParseWarning("SLURM: failed to parse --gpus-per-node value %q: %v; resource directives not parsed", gpuPerNodeStr, err)
			return nil, directives
		}
		if rs.Nodes <= 0 {
			rs.Nodes = 1
		}
		rs.Gpu = gpu
	case gpuTotalStr != "":
		gpu, err := parseSlurmGpu(gpuTotalStr)
		if err != nil {
			logParseWarning("SLURM: failed to parse --gpus value %q: %v; resource directives not parsed", gpuTotalStr, err)
			return nil, directives
		}
		if rs.Nodes <= 0 {
			// If total gpus provided but no nodes, default to that many nodes with 1 gpu each
			rs.Nodes = gpu.Count
			gpu.Count = 1
		} else {
			if gpu.Count%rs.Nodes != 0 {
				logParseWarning("SLURM: --gpus=%d not divisible by --nodes=%d; resource directives not parsed", gpu.Count, rs.Nodes)
				return nil, directives
			}
			gpu.Count /= rs.Nodes
		}
		rs.Gpu = gpu
	}

	// 3. Tasks
	// We want to calculate explicitly how many Ntasks we have.
	if hasTotalNtasks {
		rs.Ntasks = totalNtasks
		if hasNtasksPerNode {
			rs.TasksPerNode = ntasksPerNode
		} else if rs.Nodes > 0 {
			if totalNtasks%rs.Nodes == 0 {
				rs.TasksPerNode = totalNtasks / rs.Nodes
			} else if rs.Gpu != nil || gpuPerTaskStr != "" {
				logParseWarning("SLURM: --ntasks=%d not evenly divisible by --nodes=%d with GPU specs; resource directives not parsed", totalNtasks, rs.Nodes)
				return nil, directives
			} else {
				// Non-divisible: use ceiling to ensure all tasks fit; this matches memory allocation logic.
				rs.TasksPerNode = (totalNtasks + rs.Nodes - 1) / rs.Nodes
			}
		} else {
			rs.TasksPerNode = 0 // no node constraint: free distribution
		}
	} else if hasNtasksPerGpu {
		if rs.Gpu != nil {
			rs.Ntasks = ntasksPerGpu * (rs.Nodes * rs.Gpu.Count)
		} else {
			logParseWarning("SLURM: --ntasks-per-gpu requires a GPU spec; resource directives not parsed")
			return nil, directives
		}
	} else if hasNtasksPerNode {
		rs.TasksPerNode = ntasksPerNode
	}

	// Back-calculate GPUs if --gpus-per-task was used
	if gpuPerTaskStr != "" {
		gpu, err := parseSlurmGpu(gpuPerTaskStr)
		if err != nil {
			logParseWarning("SLURM: failed to parse --gpus-per-task value %q: %v; resource directives not parsed", gpuPerTaskStr, err)
			return nil, directives
		}
		if rs.Nodes > 0 {
			// e.g. nodes=2, total tasks=8 => tasksPerNode=4.
			// gpusPerNode = gpusPerTask * tasksPerNode
			tasksUsed := rs.TasksPerNode
			if tasksUsed == 0 && rs.Ntasks > 0 {
				tasksUsed = rs.Ntasks / rs.Nodes
			}
			if tasksUsed == 0 {
				tasksUsed = 1 // Safe fallback
			}
			gpu.Count *= tasksUsed
		}
		rs.Gpu = gpu
	}

	// 4. CPU (depends on task distribution or GPUs)
	if hasCpusPerTask {
		rs.CpusPerTask = cpusPerTask
	} else if hasCpusPerGpu {
		if rs.Gpu == nil {
			logParseWarning("SLURM: --cpus-per-gpu requires a GPU spec; resource directives not parsed")
			return nil, directives
		}
		rs.CpusPerTask = cpusPerGpu * rs.Gpu.Count
		// If MPI tasks spread things out, we need to divide CPUs evenly among tasks
		// on that node. If TasksPerNode is defined, we divide.
		tasksUsed := rs.TasksPerNode
		if tasksUsed == 0 && rs.Nodes > 0 && rs.Ntasks > 0 {
			tasksUsed = rs.Ntasks / rs.Nodes
		}
		if tasksUsed > 0 {
			rs.CpusPerTask /= tasksUsed
		}
		if rs.CpusPerTask < 1 {
			rs.CpusPerTask = 1
		}
	} else {
		// New logic mandates fallback to 1 if not set
		if rs.CpusPerTask <= 0 {
			rs.CpusPerTask = 1
		}
	}

	// 5. Memory — three forms, resolved after CPU and GPU.
	switch {
	case memStr != "":
		mem, err := parseMemoryMB(memStr)
		if err != nil {
			logParseWarning("SLURM: invalid --mem value %q: %v; resource directives not parsed", memStr, err)
			return nil, directives
		}
		rs.MemPerNodeMB = mem
	case memPerCpuStr != "":
		memPerCpu, err := parseMemoryMB(memPerCpuStr)
		if err != nil {
			logParseWarning("SLURM: invalid --mem-per-cpu value %q: %v; resource directives not parsed", memPerCpuStr, err)
			return nil, directives
		}
		rs.MemPerCpuMB = memPerCpu
		rs.MemPerNodeMB = 0 // clear default so GetMemPerNodeMB() derives correctly
	case memPerGpuStr != "":
		if rs.Gpu == nil {
			logParseWarning("SLURM: --mem-per-gpu requires a GPU spec; resource directives not parsed")
			return nil, directives
		}
		memPerGpu, err := parseMemoryMB(memPerGpuStr)
		if err != nil {
			logParseWarning("SLURM: invalid --mem-per-gpu value %q: %v; resource directives not parsed", memPerGpuStr, err)
			return nil, directives
		}
		rs.MemPerNodeMB = memPerGpu * int64(rs.Gpu.Count)
	}

	// 6. Time (independent).
	if timeStr != "" {
		t, err := utils.ParseDHMSTime(timeStr)
		if err != nil {
			logParseWarning("SLURM: invalid --time value %q: %v; resource directives not parsed", timeStr, err)
			return nil, directives
		}
		rs.Time = t
	}

	return rs, unconsumed
}

// CreateScriptWithSpec generates a SLURM batch script
// Returns the script path or any critical error.
func (s *SlurmScheduler) CreateScriptWithSpec(jobSpec *JobSpec, outputDir string) (string, error) {
	specs := jobSpec.Specs

	// Create output directory if specified (scripts still live in outputDir)
	if outputDir != "" {
		if err := utils.MkdirAllShared(outputDir); err != nil {
			return "", NewScriptCreationError(jobSpec.Name, outputDir, err)
		}
	}

	// Set log path based on job name; only override if caller requests it or script has no output set
	// Capture before override: separate output when stderr is explicitly set to a different path
	arraySeparateOutput := jobSpec.Array != nil && specs.Control.Stderr != "" && specs.Control.Stderr != specs.Control.Stdout
	if jobSpec.Array != nil {
		// Array job: silence scheduler output; exec redirect in script body handles per-task logs
		specs.Control.Stdout = "/dev/null"
		specs.Control.Stderr = "/dev/null"
	} else if jobSpec.Name != "" && (jobSpec.OverrideOutput || specs.Control.Stdout == "") {
		specs.Control.Stdout = filepath.Join(outputDir, fmt.Sprintf("%s.log", safeJobName(jobSpec.Name)))
	}
	if specs.Control.Stderr == "" && specs.Control.Stdout != "" {
		specs.Control.Stderr = specs.Control.Stdout
	}

	// Generate script filename
	scriptName := "job.sbatch"
	if jobSpec.Name != "" {
		scriptName = fmt.Sprintf("%s.sbatch", safeJobName(jobSpec.Name))
	}

	scriptPath := filepath.Join(outputDir, scriptName)

	// Create the batch script
	file, err := utils.CreateFileWritable(scriptPath)
	if err != nil {
		return "", NewScriptCreationError(jobSpec.Name, scriptPath, err)
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	defer writer.Flush()

	// Write shebang
	fmt.Fprintln(writer, "#!/bin/bash")

	// Write the directives not consumed by Spec or Control, with
	// the environment directive replaced by one that copies everything.
	kept, export := slurmFullEnv(specs.RemainingFlags)
	for _, flag := range kept {
		fmt.Fprintf(writer, "#SBATCH %s\n", flag)
	}
	fmt.Fprintf(writer, "#SBATCH %s\n", export)

	// Write RuntimeConfig directives
	ctrl := specs.Control
	if ctrl.JobName != "" {
		fmt.Fprintf(writer, "#SBATCH --job-name=%s\n", ctrl.JobName)
	}
	if ctrl.WorkDir != "" {
		fmt.Fprintf(writer, "#SBATCH --chdir=%s\n", ctrl.WorkDir)
	}
	if ctrl.Stdout != "" {
		fmt.Fprintf(writer, "#SBATCH --output=%s\n", ctrl.AbsStdout())
	}
	if ctrl.Stderr != "" {
		fmt.Fprintf(writer, "#SBATCH --error=%s\n", ctrl.AbsStderr())
	}
	if ctrl.EmailOnBegin || ctrl.EmailOnEnd || ctrl.EmailOnFail {
		var mailTypes []string
		if ctrl.EmailOnBegin {
			mailTypes = append(mailTypes, "BEGIN")
		}
		if ctrl.EmailOnEnd {
			mailTypes = append(mailTypes, "END")
		}
		if ctrl.EmailOnFail {
			mailTypes = append(mailTypes, "FAIL")
		}
		fmt.Fprintf(writer, "#SBATCH --mail-type=%s\n", strings.Join(mailTypes, ","))
	}
	if ctrl.MailUser != "" {
		fmt.Fprintf(writer, "#SBATCH --mail-user=%s\n", ctrl.MailUser)
	}
	if ctrl.Partition != "" {
		fmt.Fprintf(writer, "#SBATCH --partition=%s\n", ctrl.Partition)
	}
	if ctrl.Account != "" {
		fmt.Fprintf(writer, "#SBATCH --account=%s\n", ctrl.Account)
	}
	if nodes := ctrl.ExcludedNodes(); len(nodes) > 0 {
		fmt.Fprintf(writer, "#SBATCH --exclude=%s\n", strings.Join(nodes, ","))
	}

	// Write ResourceSpec directives (only when the resources parsed)
	if specs.Spec != nil {
		rs := specs.Spec

		if !rs.IsMPI() {
			// Pure OpenMP: pin to 1 node and 1 task so --mem is unambiguous.
			fmt.Fprintf(writer, "#SBATCH --nodes=1\n")
			fmt.Fprintf(writer, "#SBATCH --ntasks=1\n")
			if rs.CpusPerTask > 0 {
				fmt.Fprintf(writer, "#SBATCH --cpus-per-task=%d\n", rs.CpusPerTask)
			}
		} else {
			fmt.Fprintf(writer, "#SBATCH --ntasks=%d\n", rs.GetNtasks())
			if rs.Nodes > 0 {
				fmt.Fprintf(writer, "#SBATCH --nodes=%d\n", rs.Nodes)
			}
			// Calculate and enforce TasksPerNode when both Nodes and Ntasks are set
			tasksPerNode := rs.TasksPerNode
			if tasksPerNode == 0 && rs.Nodes > 0 && rs.GetNtasks() > 0 {
				// Use ceiling to ensure all tasks fit
				tasksPerNode = (rs.GetNtasks() + rs.Nodes - 1) / rs.Nodes
			}
			if tasksPerNode > 0 {
				fmt.Fprintf(writer, "#SBATCH --ntasks-per-node=%d\n", tasksPerNode)
			}
			if rs.CpusPerTask > 0 {
				fmt.Fprintf(writer, "#SBATCH --cpus-per-task=%d\n", rs.CpusPerTask)
			}
		}

		if emitMem.Get() {
			if rs.MemPerCpuMB > 0 {
				fmt.Fprintf(writer, "#SBATCH --mem-per-cpu=%dmb\n", rs.MemPerCpuMB)
			} else if rs.MemPerNodeMB > 0 {
				fmt.Fprintf(writer, "#SBATCH --mem=%dmb\n", rs.MemPerNodeMB)
			}
		}
		if rs.Time > 0 {
			fmt.Fprintf(writer, "#SBATCH --time=%s\n", formatSlurmTimeSpec(rs.Time))
		}
		if rs.Gpu != nil && rs.Gpu.Count > 0 {
			if rs.Gpu.Type != "" && rs.Gpu.Type != "gpu" {
				fmt.Fprintf(writer, "#SBATCH --gpus-per-node=%s:%d\n", rs.Gpu.Type, rs.Gpu.Count)
			} else {
				fmt.Fprintf(writer, "#SBATCH --gpus-per-node=%d\n", rs.Gpu.Count)
			}
		}
		if rs.Exclusive {
			fmt.Fprintln(writer, "#SBATCH --exclusive")
		}
	}

	// Array directive
	if jobSpec.Array != nil {
		arr := jobSpec.Array
		r := fmt.Sprintf("1-%d", arr.Count)
		if arr.Limit > 0 {
			r += fmt.Sprintf("%%%d", arr.Limit)
		}
		fmt.Fprintf(writer, "#SBATCH --array=%s\n", r)
	}

	fmt.Fprintln(writer, "")

	if specs.Spec != nil {
		writeResourceExports(writer, specs.Spec)
	}

	// Array job: extract input line, set ARRAY_ARGS, and redirect output
	if jobSpec.Array != nil {
		writeArrayBlock(writer, "$SLURM_ARRAY_TASK_ID",
			jobSpec.Array.InputFile, outputDir, safeJobName(jobSpec.Name),
			jobSpec.Array.Count, arraySeparateOutput)
		jobSpec.Metadata["Array Job ID"] = "$SLURM_ARRAY_JOB_ID"
		jobSpec.Metadata["Array Index"] = "$SLURM_ARRAY_TASK_ID"
		jobSpec.Metadata["Array File"] = jobSpec.Array.InputFile
		jobSpec.Metadata["Array Args"] = "$ARRAY_ARGS"
	}

	// Print job information at start
	jobIDVar := "$SLURM_JOB_ID"
	if jobSpec.Array != nil {
		jobIDVar = "$SLURM_ARRAY_JOB_ID"
	}
	writeJobHeader(writer, jobIDVar, specs, formatSlurmTimeSpec, jobSpec.Metadata)
	fmt.Fprintln(writer, "")

	// Write the command and capture exit code
	fmt.Fprintln(writer, jobSpec.Command)
	fmt.Fprintln(writer, "_EXIT_CODE=$?")

	// Print completion info
	fmt.Fprintln(writer, "")
	writeJobFooter(writer, jobIDVar)

	// Self-dispose: remove this script file (unless in debug mode or KeepScript)
	if !utils.DebugMode && !jobSpec.KeepScript {
		fmt.Fprintf(writer, "rm -f %s\n", scriptPath)
	}

	// Exit with command's exit code
	fmt.Fprintln(writer, "exit $_EXIT_CODE")

	// Make executable
	if err := utils.MakeExecutable(scriptPath); err != nil {
		return "", NewScriptCreationError(jobSpec.Name, scriptPath, err)
	}

	return scriptPath, nil
}

// buildSlurmDepFlag returns the --dependency flag string for sbatch, or "" if deps is empty.
// Format: --dependency=afterok:ID1:ID2,afternotok:ID3,afterany:ID4
func buildSlurmDepFlag(deps []Dependency) string {
	var parts []string
	for _, dep := range deps {
		if len(dep.JobIDs) > 0 {
			parts = append(parts, dep.Type+":"+strings.Join(dep.JobIDs, ":"))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "--dependency=" + strings.Join(parts, ",")
}

// buildSlurmSubmitArgs returns the sbatch argument list for the given deps and script path.
// slurmExportFlag is the directive that sets a job's environment.
const slurmExportFlag = "--export"

// slurmFullEnv returns flags without their --export directive, and the --export
// directive the job is submitted with: ALL, plus the assignments the script's
// own carried. A script that limited the environment is told so, and so is a
// shell that sets SBATCH_EXPORT, which the submit command overrides.
func slurmFullEnv(flags []string) (rest []string, export string) {
	rest, value, found := takeFlag(flags, slurmExportFlag)
	full, limited := fullEnvValue(value, "ALL", ",")
	export = slurmExportFlag + "=" + full
	if found && limited {
		noteEnvOverride("Your script sets "+slurmExportFlag+"="+value, export)
	}
	if env := os.Getenv("SBATCH_EXPORT"); env != "" {
		if _, limited := fullEnvValue(env, "ALL", ","); limited {
			noteEnvOverride("This shell sets SBATCH_EXPORT="+env, export)
		}
	}
	return rest, export
}

// slurmExportArg reads the --export directive of a generated script, to repeat
// it on the sbatch command line: there it wins over SBATCH_EXPORT, which a
// directive in the script does not. "" when the script has none.
func slurmExportArg(scriptPath string) string {
	lines, err := readFileLines(scriptPath)
	if err != nil {
		return ""
	}
	for _, line := range lines {
		if rest, ok := strings.CutPrefix(line, "#SBATCH "+slurmExportFlag+"="); ok {
			return slurmExportFlag + "=" + rest
		}
	}
	return ""
}

func buildSlurmSubmitArgs(deps []Dependency, scriptPath string) []string {
	args := []string{scriptPath}
	if export := slurmExportArg(scriptPath); export != "" {
		args = append([]string{export}, args...)
	}
	if flag := buildSlurmDepFlag(deps); flag != "" {
		args = append([]string{flag, "--kill-on-invalid-dep=yes"}, args...)
	}
	return args
}

func (s *SlurmScheduler) Submit(ctx context.Context, scriptPath string, deps []Dependency) (string, error) {
	if err := CheckDependencies(SchedulerSLURM, deps); err != nil {
		return "", err
	}
	args := buildSlurmSubmitArgs(deps, scriptPath)

	// Execute sbatch
	output, err := runCommand(ctx, "SLURM", "submit", s.sbatchBin, args...)
	if err != nil {
		return "", NewSubmissionError("SLURM", filepath.Base(scriptPath), string(output), err)
	}

	// Parse job ID from output
	matches := s.jobIDRe.FindStringSubmatch(string(output))
	if len(matches) < 2 {
		return "", fmt.Errorf("%w: %s", ErrJobIDParseFailed, string(output))
	}

	jobID := matches[1]
	return jobID, nil
}

// GetClusterInfo retrieves SLURM cluster configuration. A failed sinfo call is returned and not cached.
func (s *SlurmScheduler) GetClusterInfo(ctx context.Context) (*ClusterInfo, error) {
	if s.cachedClusterInfo != nil {
		return s.cachedClusterInfo, nil
	}

	info := &ClusterInfo{
		AvailableGpus: make([]GpuInfo, 0),
		Limits:        make([]ResourceLimits, 0),
	}

	if s.sinfoCommand != "" {
		limits, gpus, err := s.getPartitions(ctx)
		if err != nil {
			return nil, err
		}
		info.Limits = limits
		info.AvailableGpus = gpus
	}

	s.cachedClusterInfo = info
	return info, nil
}

// getPartitions queries SLURM for every partition's state, time limits, node shape and GPUs in one sinfo call.
// It never passes -N, which lists a row per node per partition and is far slower on a large cluster.
func (s *SlurmScheduler) getPartitions(ctx context.Context) ([]ResourceLimits, []GpuInfo, error) {
	// %P=partition (* marks the default) %a=state %l=max time %s=job size
	// %c=CPUs/node %m=mem/node(MB) %G=GRES %F=nodes allocated/idle/other/total
	output, err := runCommandEnv(ctx, slurmToolEnv("SINFO_", false), "SLURM", "query-partitions", s.sinfoCommand, "-e", "-o", sinfoPartitionFormat, "--noheader")
	if err != nil {
		return nil, nil, NewClusterError("SLURM", "query partitions", err)
	}
	limits, gpus := parseSinfoPartitions(string(output))
	return limits, gpus, nil
}

// sinfoPartitionFormat has no %T, which would split a partition into a row per node state.
// It is used with -e, so a row holds only nodes that agree on every column.
const sinfoPartitionFormat = "%P|%a|%l|%s|%c|%m|%G|%F"

// parseSinfoPartitions reads the rows of sinfoPartitionFormat, in the order sinfo prints them.
//   - A partition has several rows when its nodes differ in CPUs, memory or GRES. Rows that agree on
//     CPUs, memory and GRES are one node type. Their nodes add up. A socket suffix such as "(S:0-3)" is ignored.
//   - The node column is "allocated/idle/other/total", or a plain total. Nodes that are allocated or idle
//     are up and take jobs. The rest are down, drained or in another state that takes none.
//   - A trailing "+" on CPUs or memory marks the smallest value of a row that groups unequal nodes.
//   - The per-node figures and the node count of a partition are the largest and the sum over its
//     node types.
func parseSinfoPartitions(output string) ([]ResourceLimits, []GpuInfo) {
	var limits []ResourceLimits
	limitIdx := make(map[string]int)
	typeIdx := make(map[string]int) // key = partition, CPUs, memory and GPUs; the index in the node types of the partition

	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		f := strings.Split(line, "|")
		if len(f) < 8 {
			continue
		}
		name := strings.TrimSpace(f[0])
		isDefault := strings.HasSuffix(name, "*")
		name = strings.TrimSuffix(name, "*")

		i, ok := limitIdx[name]
		if !ok {
			limits = append(limits, ResourceLimits{
				Partition:     name,
				State:         strings.ToLower(strings.TrimSpace(f[1])),
				Default:       isDefault,
				MaxTime:       parseSlurmTime(f[2]),
				TimeUnlimited: strings.EqualFold(strings.TrimSpace(f[2]), "infinite"),
				MaxNodes:      parseSlurmMaxNodes(f[3]),
			})
			i = len(limits) - 1
			limitIdx[name] = i
		}
		l := &limits[i]

		cpuText, cpuLower := splitLowerBound(f[4])
		cpus, _ := strconv.Atoi(cpuText)
		memText, memLower := splitLowerBound(f[5])
		memMB, _ := parseMemoryMB(memText)
		nodes, nodesUp := parseSlurmNodeCounts(f[7])
		gpus := parseSlurmGres(f[6])

		key := name + "|" + strconv.Itoa(cpus) + "|" + strconv.FormatInt(memMB, 10) + "|" + gpuKey(gpus)
		j, ok := typeIdx[key]
		if !ok {
			l.NodeTypes = append(l.NodeTypes, NodeType{Cpus: cpus, MemMB: memMB, Gpus: gpus})
			j = len(l.NodeTypes) - 1
			typeIdx[key] = j
		}
		t := &l.NodeTypes[j]
		t.Nodes += nodes
		t.NodesUp = addNodesUp(t.NodesUp, nodesUp)
		t.CpusLowerBound = t.CpusLowerBound || cpuLower
		t.MemLowerBound = t.MemLowerBound || memLower
	}

	var gpus []GpuInfo
	gpuIdx := make(map[string]int) // key = "partition:gpuType"
	for i := range limits {
		l := &limits[i]
		for _, t := range l.NodeTypes {
			l.Nodes += t.Nodes
			l.MaxCpusPerNode = max(l.MaxCpusPerNode, t.Cpus)
			l.MaxMemMBPerNode = max(l.MaxMemMBPerNode, t.MemMB)
			l.CpusLowerBound = l.CpusLowerBound || t.CpusLowerBound
			l.MemLowerBound = l.MemLowerBound || t.MemLowerBound
			for _, g := range t.Gpus {
				key := l.Partition + ":" + g.Type
				k, ok := gpuIdx[key]
				if !ok {
					gpus = append(gpus, GpuInfo{Type: g.Type, Partition: l.Partition})
					k = len(gpus) - 1
					gpuIdx[key] = k
				}
				gpus[k].Total += g.PerNode * t.Nodes
				l.MaxGpus += g.PerNode * t.Nodes
			}
		}
	}
	return limits, gpus
}

// parseSlurmNodeCounts reads the node column of sinfoPartitionFormat. It returns the total, and the
// nodes that are allocated or idle, which is nil when the column is a plain total.
func parseSlurmNodeCounts(s string) (total int, up *int) {
	parts := strings.Split(strings.TrimSpace(s), "/")
	if len(parts) != 4 {
		total, _ = strconv.Atoi(parts[0])
		return total, nil
	}
	allocated, _ := strconv.Atoi(parts[0])
	idle, _ := strconv.Atoi(parts[1])
	total, _ = strconv.Atoi(parts[3])
	n := allocated + idle
	return total, &n
}

// parseSlurmGres reads the GPUs of a sinfo GRES column, such as
// "gpu:nvidia_h100:8(S:0-3),gpu:nvidia_a100:4". A GPU with no type is "gpu", and other GRES are skipped.
func parseSlurmGres(gres string) []NodeGpu {
	var gpus []NodeGpu
	for _, entry := range strings.Split(strings.TrimSpace(gres), ",") {
		if !strings.HasPrefix(entry, "gpu:") {
			continue
		}
		entry = strings.TrimPrefix(entry, "gpu:")
		if idx := strings.Index(entry, "("); idx > 0 {
			entry = entry[:idx]
		}

		gpuType := "gpu"
		count := 0
		if strings.Contains(entry, ":") {
			parts := strings.Split(entry, ":")
			gpuType = parts[0]
			count, _ = strconv.Atoi(parts[len(parts)-1])
		} else {
			count, _ = strconv.Atoi(entry)
		}
		if count > 0 {
			gpus = append(gpus, NodeGpu{Type: gpuType, PerNode: count})
		}
	}
	return gpus
}

// splitLowerBound drops the trailing "+" of a sinfo figure and reports whether it was there.
func splitLowerBound(s string) (string, bool) {
	s = strings.TrimSpace(s)
	trimmed := strings.TrimSuffix(s, "+")
	return trimmed, trimmed != s
}

// parseSlurmMaxNodes reads sinfo's job size, "min-max" or one number, and returns the maximum.
// 0 means "infinite" or unreadable.
func parseSlurmMaxNodes(s string) int {
	s = strings.TrimSpace(s)
	if _, after, found := strings.Cut(s, "-"); found {
		s = after
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// parseSlurmTime reads a SLURM time: "minutes", "minutes:seconds", "hours:minutes:seconds",
// "days-hours", "days-hours:minutes" or "days-hours:minutes:seconds".
// "15:00" is 15 minutes, not 15 hours. "infinite" and anything unreadable give 0.
func parseSlurmTime(s string) time.Duration {
	s = strings.TrimSpace(s)
	var days int64
	hasDays := false
	if before, after, found := strings.Cut(s, "-"); found {
		n, err := strconv.ParseInt(before, 10, 64)
		if err != nil {
			return 0
		}
		days, s, hasDays = n, after, true
	}

	parts := strings.Split(s, ":")
	nums := make([]int64, len(parts))
	for i, p := range parts {
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil || n < 0 {
			return 0
		}
		nums[i] = n
	}

	var h, m, sec int64
	switch {
	case hasDays && len(nums) == 1:
		h = nums[0]
	case hasDays && len(nums) == 2:
		h, m = nums[0], nums[1]
	case len(nums) == 3:
		h, m, sec = nums[0], nums[1], nums[2]
	case !hasDays && len(nums) == 2:
		m, sec = nums[0], nums[1]
	case !hasDays && len(nums) == 1:
		m = nums[0]
	default:
		return 0
	}
	return time.Duration(days*86400+h*3600+m*60+sec) * time.Second
}

// parseSlurmGpu parses a SLURM GPU spec:
//   - "gpu:2" and "a100:1"
//   - "gpu:a100:2"
//   - a MIG profile such as "nvidia_h100_80gb_hbm3_1g.10gb", with an optional ":count"
func parseSlurmGpu(gpuStr string) (*GpuSpec, error) {
	if gpuStr == "" {
		return nil, nil
	}

	spec := &GpuSpec{
		Raw:   gpuStr,
		Count: 1,
	}

	// Check if this is a MIG profile (contains dots and underscores)
	// MIG format: nvidia_h100_80gb_hbm3_1g.10gb or nvidia_h100_80gb_hbm3_1g.10gb:2
	if strings.Contains(gpuStr, ".") && strings.Contains(gpuStr, "_") {
		// This looks like a MIG profile
		parts := strings.Split(gpuStr, ":")
		if len(parts) == 1 {
			// Just the MIG profile name
			spec.Type = parts[0]
			spec.Count = 1
		} else if len(parts) == 2 {
			// MIG profile with count
			spec.Type = parts[0]
			if count, err := strconv.Atoi(parts[1]); err == nil {
				spec.Count = count
			}
		}
		return spec, nil
	}

	parts := strings.Split(gpuStr, ":")

	switch len(parts) {
	case 1:
		if count, err := strconv.Atoi(parts[0]); err == nil {
			spec.Count = count
			spec.Type = "gpu"
		} else {
			spec.Type = parts[0]
			spec.Count = 1
		}

	case 2:
		spec.Type = parts[0]
		if count, err := strconv.Atoi(parts[1]); err == nil {
			spec.Count = count
		}

	case 3:
		spec.Type = parts[1]
		if count, err := strconv.Atoi(parts[2]); err == nil {
			spec.Count = count
		}

	default:
		lastColon := strings.LastIndex(gpuStr, ":")
		if lastColon > 0 {
			spec.Type = gpuStr[:lastColon]
			if count, err := strconv.Atoi(gpuStr[lastColon+1:]); err == nil {
				spec.Count = count
			}
		} else {
			spec.Type = gpuStr
		}
	}

	return spec, nil
}

func formatSlurmTimeSpec(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	days := int64(d.Hours()) / 24
	if days > 0 {
		rem := d - time.Duration(days*24)*time.Hour
		return fmt.Sprintf("%d-%s", days, formatHMSTime(rem))
	}
	return formatHMSTime(d)
}

// GetJobResources reads allocated resources from SLURM environment variables.
// Fields with value 0 were not exposed by SLURM.
func (s *SlurmScheduler) GetJobResources() *ResourceSpec {
	if _, ok := os.LookupEnv("SLURM_JOB_ID"); !ok {
		return nil
	}
	res := &ResourceSpec{}
	if v := getEnvInt("SLURM_JOB_NUM_NODES"); v != nil {
		res.Nodes = *v
	}

	// Prefer MPI library env vars (global across all ranks) over SLURM vars (task-local in MPI jobs).
	if mpiSize := getMpiCommSize(); mpiSize != nil {
		res.Ntasks = *mpiSize
	} else if v := getEnvInt("SLURM_NTASKS"); v != nil {
		res.Ntasks = *v
	}

	// TasksPerNode: prefer MPI local size (actual tasks on this node) over SLURM var
	if mpiLocal := getMpiLocalSize(); mpiLocal != nil {
		res.TasksPerNode = *mpiLocal
	} else if v := getEnvInt("SLURM_NTASKS_PER_NODE"); v != nil {
		res.TasksPerNode = *v
	} else if res.Ntasks > 0 && res.Nodes > 1 && res.Ntasks%res.Nodes == 0 {
		// Derive TasksPerNode when SLURM_NTASKS_PER_NODE is absent but geometry is uniform.
		res.TasksPerNode = res.Ntasks / res.Nodes
	}
	if v := getEnvInt("SLURM_CPUS_PER_TASK"); v != nil {
		res.CpusPerTask = *v
	}
	// SLURM_MEM_PER_NODE is already in MB.
	if v := getEnvInt64("SLURM_MEM_PER_NODE"); v != nil {
		res.MemPerNodeMB = *v
	}
	if n := getCudaDeviceCount(); n != nil && *n > 0 {
		res.Gpu = &GpuSpec{Count: *n}
	}
	return res
}

// GetJobStatus returns the current status of the given SLURM job ID.
// Uses squeue --format=%T; returns JobStatusUnknown conservatively when squeue is unavailable or times out.
// squeue only lists active jobs; empty output means the job has finished or is absent.
func (s *SlurmScheduler) GetJobStatus(ctx context.Context, jobID string) (JobStatus, error) {
	squeueBin := siblingBin(s.sbatchBin, "squeue")
	if squeueBin == "" {
		return JobStatusUnknown, nil // conservative: can't check
	}
	out, err := runCommand(ctx, "SLURM", "job-status", squeueBin, "-j", jobID, "--noheader", "--format=%T")
	if err != nil {
		// squeue exits non-zero when the job ID is no longer known (completed/cancelled).
		// "Invalid job id" covers both old IDs and IDs that never existed.
		if strings.Contains(strings.ToLower(string(out)), "invalid job id") {
			return JobStatusDone, nil
		}
		return JobStatusUnknown, nil // conservative: timeout or unavailable
	}
	state := strings.TrimSpace(string(out))
	if state == "" {
		return JobStatusDone, nil // not in queue → finished or absent
	}
	switch strings.ToUpper(state) {
	case "PENDING", "CONFIGURING", "STAGE_IN":
		return JobStatusPending, nil
	default: // RUNNING, COMPLETING, STAGE_OUT, or any other active state
		return JobStatusRunning, nil
	}
}

// CancelJob cancels the SLURM job with the given ID using scancel.
// Returns nil if the job is already gone.
func (s *SlurmScheduler) CancelJob(ctx context.Context, jobID string) error {
	scancelBin := siblingBin(s.sbatchBin, "scancel")
	if scancelBin == "" {
		return fmt.Errorf("scancel not found")
	}
	_, err := runCommand(ctx, "SLURM", "cancel-job", scancelBin, jobID)
	if err != nil {
		if _, ok := err.(*TimeoutError); ok {
			return err
		}
		return nil // scancel exits non-zero for already-gone jobs
	}
	return nil
}

// TryParseSlurmScript attempts to parse a SLURM script without requiring SLURM binaries.
// This is a static parser that can work in any environment.
func TryParseSlurmScript(scriptPath string) (*ScriptSpecs, error) {
	parser := &SlurmScheduler{
		directiveRe: regexp.MustCompile(`^\s*#SBATCH\s+(.+)$`),
	}
	return parser.ReadScriptSpecs(scriptPath)
}

// splitSlurmNodeList splits a node list on the commas outside brackets, so "n[1,3],m" is two entries.
func splitSlurmNodeList(list string) []string {
	out := []string{}
	depth, start := 0, 0
	for i, r := range list {
		switch r {
		case '[':
			depth++
		case ']':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, list[start:i])
				start = i + 1
			}
		}
	}
	out = append(out, list[start:])
	kept := out[:0]
	for _, n := range out {
		if n = strings.TrimSpace(n); n != "" {
			kept = append(kept, n)
		}
	}
	return kept
}
