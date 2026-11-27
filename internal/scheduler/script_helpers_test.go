package scheduler

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/condatainer/condatainer/internal/utils/units"
)

func TestResourceEnvVars(t *testing.T) {
	tests := []struct {
		name     string
		rs       *ResourceSpec
		wantVars map[string]string
	}{
		{
			name: "OpenMP: 1 task, 8 CPUs, 32GB per node",
			rs: &ResourceSpec{
				Nodes:        1,
				Ntasks:       1,
				TasksPerNode: 1,
				CpusPerTask:  8,
				MemPerNodeMB: 32768,
			},
			wantVars: map[string]string{
				"NNODE":          "1",
				"NRANK":          "1",
				"NCPU":           "8",
				"NRANK_PER_NODE": "1",
				"MEM":            "32768", // 32768 / 1 task
				"MEM_GB":         "32",
			},
		},
		{
			name: "MPI: 2 nodes, 16 tasks, 8 tasks/node, 1 CPU/task, 4GB per CPU",
			rs: &ResourceSpec{
				Nodes:        2,
				Ntasks:       16,
				TasksPerNode: 8,
				CpusPerTask:  1,
				MemPerCpuMB:  4096,
			},
			wantVars: map[string]string{
				"NNODE":          "2",
				"NRANK":          "16",
				"NCPU":           "1",
				"NRANK_PER_NODE": "8",
				"MEM":            "4096", // 4096 * 1 CPU/task
				"MEM_GB":         "4",
			},
		},
		{
			name: "Hybrid: 2 nodes, 8 tasks, 4 tasks/node, 4 CPUs/task, 64GB per node",
			rs: &ResourceSpec{
				Nodes:        2,
				Ntasks:       8,
				TasksPerNode: 4,
				CpusPerTask:  4,
				MemPerNodeMB: 65536,
			},
			wantVars: map[string]string{
				"NNODE":          "2",
				"NRANK":          "8",
				"NCPU":           "4",
				"NRANK_PER_NODE": "4",
				"MEM":            "16384", // 65536 / 4 tasks/node
				"MEM_GB":         "16",
			},
		},
		{
			name: "Manually-constructed spec with Nodes but no TasksPerNode",
			rs: &ResourceSpec{
				Nodes:        2, // Nodes specified
				Ntasks:       7,
				TasksPerNode: 0, // Free distribution (manual edge case; parsing would set this to 4)
				CpusPerTask:  1,
				MemPerCpuMB:  8192,
			},
			wantVars: map[string]string{
				"NNODE":  "2",
				"NRANK":  "7",
				"NCPU":   "1",
				"MEM":    "8192", // 8192 * 1 CPU/task
				"MEM_GB": "8",
			},
		},
		{
			name: "Free-Distribution MPI without Nodes: 7 tasks only, no node info, 1 CPU/task, 8GB per CPU",
			rs: &ResourceSpec{
				Nodes:        0, // Not set - no node info
				Ntasks:       7,
				TasksPerNode: 0, // Free distribution
				CpusPerTask:  1,
				MemPerCpuMB:  8192,
			},
			wantVars: map[string]string{
				"NNODE":  "1", // Defaults to 1
				"NRANK":  "7",
				"NCPU":   "1",
				"MEM":    "8192", // 8192 * 1 CPU/task
				"MEM_GB": "8",
			},
		},
		{
			name: "OpenMP with MemPerCpuMB: Nodes=0, 1 task, 16 CPUs, 2GB per CPU",
			rs: &ResourceSpec{
				Nodes:        0, // Not set - single node implied
				Ntasks:       1,
				TasksPerNode: 1,
				CpusPerTask:  16,
				MemPerCpuMB:  2048,
			},
			wantVars: map[string]string{
				"NNODE":          "1", // Defaults to 1
				"NRANK":          "1",
				"NCPU":           "16",
				"NRANK_PER_NODE": "1",
				"MEM":            "32768", // 2048 * 16 CPUs/task
				"MEM_GB":         "32",
			},
		},
		{
			name: "No memory specified",
			rs: &ResourceSpec{
				Nodes:        2,
				Ntasks:       8,
				TasksPerNode: 4,
				CpusPerTask:  2,
			},
			wantVars: map[string]string{
				"NNODE":          "2",
				"NRANK":          "8",
				"NCPU":           "2",
				"NRANK_PER_NODE": "4",
			},
		},
		{
			name: "Minimal defaults: nil ResourceSpec",
			rs:   nil,
			wantVars: map[string]string{
				"NNODE": "1",
				"NRANK": "1",
				"NCPU":  "1",
			},
		},
		{
			name: "Ntasks from topology: 2 nodes, 4 tasks/node (Ntasks not set)",
			rs: &ResourceSpec{
				Nodes:        2,
				TasksPerNode: 4,
				CpusPerTask:  2,
			},
			wantVars: map[string]string{
				"NNODE":          "2",
				"NRANK":          "8", // 2 * 4
				"NCPU":           "2",
				"NRANK_PER_NODE": "4",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envVars := ResourceEnvVars(tt.rs)

			// Convert slice to map for easier checking
			gotMap := make(map[string]string)
			for _, env := range envVars {
				parts := strings.SplitN(env, "=", 2)
				if len(parts) == 2 {
					gotMap[parts[0]] = parts[1]
				}
			}

			// Check expected variables exist with correct values, and that each has a CNT_ copy
			// with the same value.
			if v, exists := gotMap["OMP_NUM_THREADS"]; exists {
				t.Errorf("OMP_NUM_THREADS = %q is set; only a run sets it", v)
			}
			for key, wantVal := range tt.wantVars {
				names := []string{key, "CNT_" + key}
				for _, name := range names {
					gotVal, exists := gotMap[name]
					if !exists {
						t.Errorf("Expected variable %s not found in output", name)
						continue
					}
					if gotVal != wantVal {
						t.Errorf("Variable %s = %q; want %q", name, gotVal, wantVal)
					}
				}
			}

			// Variables the spec cannot fill are left out, not defaulted.
			for _, key := range []string{"NRANK_PER_NODE", "MEM", "MEM_GB"} {
				if _, want := tt.wantVars[key]; !want {
					for _, name := range []string{key, "CNT_" + key} {
						if v, exists := gotMap[name]; exists {
							t.Errorf("Variable %s = %q is set; want it absent", name, v)
						}
					}
				}
			}

		})
	}
}

// A scheduler stops reading directives at the first command, so a directive after it
// is not part of the script's request.
func TestScriptDirectivesAreReadFromTheHeaderOnly(t *testing.T) {
	for name, tc := range map[string]struct {
		parse func(string) (*ScriptSpecs, error)
		line  string
	}{
		"slurm": {TryParseSlurmScript, "#SBATCH --cpus-per-task=%d"},
		"pbs":   {TryParsePbsScript, "#PBS -l select=1:ncpus=%d"},
		"lsf":   {TryParseLsfScript, "#BSUB -n %d"},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "job.sh")
			text := "#!/bin/bash\n" + fmt.Sprintf(tc.line, 4) + "\necho start\n" + fmt.Sprintf(tc.line, 16) + "\n"
			if err := os.WriteFile(path, []byte(text), 0o755); err != nil {
				t.Fatal(err)
			}
			specs, err := tc.parse(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(specs.RawFlags) != 1 || !strings.HasSuffix(specs.RawFlags[0], "4") {
				t.Errorf("RawFlags = %v, want only the header's directive", specs.RawFlags)
			}
		})
	}
}

// A unique job file never replaces one that exists; any other job file does.
func TestCreateScriptFileUnique(t *testing.T) {
	path := filepath.Join(t.TempDir(), "job.sbatch")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := createScriptFile(path, true); !errors.Is(err, os.ErrExist) {
		t.Errorf("unique create of a taken path: err = %v, want os.ErrExist", err)
	}
	f, err := createScriptFile(path, false)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if got, _ := os.ReadFile(path); len(got) != 0 {
		t.Errorf("a create that is not unique keeps %q", got)
	}
}

// The Elapsed line of a job log writes a time as units.FormatTimeDetail does: HH:MM:SS under a day, D-HH:MM:SS from a day.
func TestJobFooterTimeMatchesTimeDetail(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	var header strings.Builder
	writeJobHeader(&header, "$JOB", nil, units.FormatTimeDetail, nil)
	var definition string
	for _, line := range strings.Split(header.String(), "\n") {
		if strings.HasPrefix(line, "_format_time()") {
			definition = line
		}
	}
	if definition == "" {
		t.Fatal("the job header does not define _format_time")
	}
	for _, seconds := range []int{0, 5, 59, 3599, 3600, 86399, 86400, 86401, 90061, 168 * 3600, 30*86400 + 5*3600 + 6} {
		out, err := exec.Command(bash, "-c", definition+"\n_format_time "+strconv.Itoa(seconds)).Output()
		if err != nil {
			t.Fatalf("%d seconds: %v", seconds, err)
		}
		if want := units.FormatTimeDetail(time.Duration(seconds) * time.Second); string(out) != want {
			t.Errorf("_format_time %d = %q, want %q", seconds, out, want)
		}
	}
}

func TestStripInlineComment(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"No comment", "--cpus-per-task=8", "--cpus-per-task=8"},
		{"With comment", "--cpus-per-task=8  # This is a comment", "--cpus-per-task=8"},
		{"Comment only", "# Just a comment", ""},
		{"Multiple hashes", "--mem=16G # First # Second", "--mem=16G"},
		{"Hash in value needs escaping", "foo=bar#baz", "foo=bar"},
		{"Whitespace around comment", "--time=02:00:00   #   Time limit  ", "--time=02:00:00"},
		{"Empty after hash", "value #", "value"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripInlineComment(tt.input)
			if got != tt.want {
				t.Errorf("stripInlineComment(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
