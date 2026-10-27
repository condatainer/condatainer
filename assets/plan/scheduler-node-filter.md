# Plan: exclude nodes for submitted jobs

Status: the `scheduler.exclude_nodes` setting, its rendering, the script directive and the
`--exclude-nodes` flag on `run` and `helper` are built. The helper front ends are next.
Listing nodes and allowing only named nodes are deferred.

## Goal

A user keeps submitted jobs off named nodes, from config or the command line. The same
setting works on every scheduler that can express it.

## Built

- `scheduler.exclude_nodes` is a list key. `config append` adds a name, and the environment
  variable takes a comma-separated list.
- Every config layer's list is merged, so a site's list and a user's both apply.
- A name must be a plain host name. It is written into a job script, so shell and submit-file
  syntax is refused.
- Each scheduler's script writer renders it, so run, build, restore and helper jobs all get it.

| Scheduler | Exclude |
|---|---|
| SLURM | `--exclude` |
| LSF | `-m "all ~a"` |
| HTCondor | `requirements` mismatch on `Machine` |
| PBS Pro / OpenPBS | refused |

- A scheduler that cannot express the setting fails the submit before anything is queued. The
  error names the setting. It is never silently ignored.
- Priority is the config list, then the script's own directive, then the job's list. Each
  replaces the one before it, as for cpus and mem.
  - Only SLURM `--exclude` and an LSF `-m` are read from a script. An LSF `-m` is a host list
    that cannot be merged, so it stands over the config list.
  - A translated script does not carry the list, so a node name never crosses clusters.
- `run` and `helper` take `--exclude-nodes`, long name only. Their short letters are taken.
  - It replaces the list for that job. An empty value clears it.
  - Build and restore jobs have no flag and use the config key.
- A submit file's own `requirements` is not a recognised key, so the HTCondor line stands
  alone.
- Only SLURM is tested on a real cluster. The LSF and HTCondor rows are written from each
  scheduler's manual and need a check before they are documented as working.

## Next

### Helper front ends

- The dashboard's helper launch form gets an "Exclude nodes" field beside account and
  partition.
  - The start request carries `exclude_nodes`, and the server passes it on like `partition`.
  - The form's placeholder is the config value, served with the other defaults.
  - The field takes typed names.
- The interactive prompt for a helper gets an `x` key beside `a` and `p`. `-` clears it.
- A value set in the form or the prompt is saved in the helper's own config, like account and
  partition. An empty value falls back to the config key.
- A scheduler that refuses the setting shows its error in the form or the prompt, before any
  job is queued.

### Docs and tests

- `config` help for the key, and the `run` and `helper` help for the flag.
- A user-docs page on keeping jobs off a node.
  - It says where to find a node's name: `sinfo -N` on SLURM, `pbsnodes -a` on PBS, `bhosts`
    on LSF, `condor_status` on HTCondor.
  - It says SLURM wants the name as registered. A short name is refused when the node is
    registered by its full name.
- One test for the start request carrying `exclude_nodes` through to the job.

## Deferred

### Listing nodes

- `condatainer scheduler --nodes` prints one row per node: name, state, CPUs, memory and
  partitions.
- It is the way to look up the names the setting takes. The user docs then point to it
  instead of each scheduler's own command.
- `--nodes` combines with `--cpu` and `--gpu` to filter by partition type.
- Sources: `sinfo -N` for SLURM, `pbsnodes -a` for PBS, `bhosts -w` for LSF, `condor_status`
  for HTCondor.
- Each scheduler already queries the cluster for limits, so a node query has a place to live.
- A scheduler that cannot list nodes says so and exits nonzero.
- A node endpoint beside `/api/partitions` returns the same rows and fills a picker in the
  dashboard's launch form. The field keeps taking typed names, so a failed query does not
  block a launch.

### Allow only named nodes

- A second setting, `scheduler.nodes`, with a `--nodes` flag on `run` and `helper`.
- Rendering: SLURM `--nodelist`, LSF `-m "a b"`, HTCondor a `requirements` match, PBS `host=`.
- SLURM `--nodelist` forces the job onto every listed node. More names than the job has nodes
  would be refused, not widened.
- When a name is in both settings, exclude wins, and a list emptied that way is an error.

## Decisions

- Names are not checked against the cluster. A query on every submit costs more than it
  saves. A name that matches nothing excludes nothing.
- No script header carries a node list. A node name means nothing outside one cluster.
- A script's own exclude directive passes through. On SLURM the later `--exclude` wins, and
  the config's line comes after the script's.
