package scheduler

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// scancelKillErrorRe is the line scancel prints for one job it could not cancel.
var scancelKillErrorRe = regexp.MustCompile(`Kill job error on job id (\S+?): (.*)$`)

// CancelJobs cancels the current user's jobs with one scancel --me for every ID.
//   - --me limits the call to the user's own jobs, so a user who may cancel any job still cancels only theirs.
//   - scancel prints nothing for a job it cancelled and an error line for each ID it could not,
//     so an ID is sent unless it has an error line.
//   - The caller passes only IDs it found among the user's jobs. scancel is silent for a job of
//     another user, so its silence says nothing about ownership.
func (s *SlurmScheduler) CancelJobs(ctx context.Context, ids []string) ([]CancelResult, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	scancelBin := siblingBin(s.sbatchBin, "scancel")
	if scancelBin == "" {
		return nil, NewClusterError("SLURM", "cancel jobs", fmt.Errorf("scancel not found"))
	}
	out, err := runCommandEnv(ctx, slurmToolEnv("SCANCEL_", false), "SLURM", "cancel-jobs", scancelBin, append([]string{"--me"}, ids...)...)
	if _, timedOut := err.(*TimeoutError); timedOut {
		return nil, err
	}
	refused, other := parseScancelErrors(string(out))
	if err != nil && (len(refused) == 0 || len(other) > 0) {
		// An error line that names none of the IDs says the call itself failed.
		return nil, NewClusterError("SLURM", "cancel jobs", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out))))
	}

	results := make([]CancelResult, len(ids))
	named := 0
	for i, id := range ids {
		results[i] = CancelResult{ID: id, Outcome: CancelSent}
		if msg, bad := refused[id]; bad {
			results[i] = CancelResult{ID: id, Outcome: CancelError, Message: msg}
			named++
		}
	}
	if named != len(refused) {
		// An error for an ID that was not asked for cannot be tied to a job, so no result is trusted.
		return nil, NewClusterError("SLURM", "cancel jobs", fmt.Errorf("unexpected scancel output: %s", strings.TrimSpace(string(out))))
	}
	return results, nil
}

// parseScancelErrors reads the output of scancel. refused is the message for each job ID that has a
// "Kill job error on job id" line. other is every other line of output.
func parseScancelErrors(output string) (refused map[string]string, other []string) {
	refused = map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if m := scancelKillErrorRe.FindStringSubmatch(line); m != nil {
			refused[m[1]] = strings.TrimSpace(m[2])
			continue
		}
		other = append(other, line)
	}
	return refused, other
}
