package testenv

import (
	"os"
	"testing"
)

var jobSample = map[string]string{
	"SLURM_JOB_ID": "1", "SLURM_NTASKS": "8", "PBS_JOBID": "1.srv", "NCPUS": "4", "LSB_JOBID": "2",
	"_CONDOR_JOB_AD": "/ad", "CUDA_VISIBLE_DEVICES": "0,1", "OMP_NUM_THREADS": "16",
	"NCPU": "4", "MEM": "1024", "NRANK": "8", "CNT_NCPU": "4", "CNT_NRANK": "8",
}

// Variables that are not about a job stay.
var keepSample = map[string]string{"CNT_ROOT": "/root", "HOME": "/home/x", "SLURMISH": "yes"}

func setSample(t *testing.T) {
	t.Helper()
	for k, v := range jobSample {
		t.Setenv(k, v)
	}
	for k, v := range keepSample {
		t.Setenv(k, v)
	}
}

func checkCleared(t *testing.T) {
	t.Helper()
	for k := range jobSample {
		if v, ok := os.LookupEnv(k); ok {
			t.Errorf("%s = %q is still set", k, v)
		}
	}
	for k, want := range keepSample {
		if got := os.Getenv(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestClearJobEnv(t *testing.T) {
	setSample(t)
	ClearJobEnv()
	checkCleared(t)
}

func TestClearJobEnvForRestores(t *testing.T) {
	setSample(t)
	t.Run("one test", func(t *testing.T) {
		ClearJobEnvFor(t)
		checkCleared(t)
	})
	for k, want := range jobSample {
		if got := os.Getenv(k); got != want {
			t.Errorf("%s = %q after the test, want %q", k, got, want)
		}
	}
}
