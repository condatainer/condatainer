package scheduler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNodeNameEnvExprExpandsToTheJobsNode(t *testing.T) {
	dir := t.TempDir()
	nodeFile := filepath.Join(dir, "nodes")
	machineAd := filepath.Join(dir, "ad")
	if err := os.WriteFile(nodeFile, []byte("n1.example\nn2.example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(machineAd, []byte("Cpus = 4\nMachine = \"n1.example\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(),
		"SLURMD_NODENAME=n1.example", "LSB_HOSTS=n1.example n1.example n2.example",
		"PBS_NODEFILE="+nodeFile, "_CONDOR_MACHINE_AD="+machineAd)

	for name, sched := range map[string]Scheduler{
		"slurm": newTestSlurmScheduler(), "pbs": newTestPbsScheduler(),
		"lsf": newTestLsfScheduler(), "htcondor": newTestHTCondorScheduler(),
	} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", `printf %s "`+sched.NodeNameEnvExpr()+`"`)
			cmd.Env = env
			out, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(string(out)); got != "n1.example" {
				t.Errorf("expression %q gave %q, want n1.example", sched.NodeNameEnvExpr(), got)
			}
		})
	}
}
