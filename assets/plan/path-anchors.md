# Plan: where a relative `#DEP:` path points

Status: implemented.

## Model

Two layouts share one project:

- Root layout: scripts in `src/`, files in `data/`. A script writes `data/x.sqf`.
- Steps layout: `step1/` holds a script and its files. A script writes `x.sqf`.

| Caller | A relative `.sqf` / `.sif` / `.img` means |
|---|---|
| `run`, no project | the work directory: the submit directory, or the one a native directive sets |
| `lock` | the declaring script's folder first, then the project root |
| `run`, in a project | the file `lock` pinned |

- `lock` checks the filesystem. It pins the file it found under a root-relative key.
- `run` reads the pin. It checks the pinned file against the digest and searches nothing.
- The work directory is never an anchor in a project. A pin records a fixed place, and a
  work directory is decided at run time.
- `--project` picks the root. It moves neither the work directory nor the anchors.
- A path that needs the work directory cannot be pinned. The author declares it relative to
  the script or the root, or as an external absolute path, which is a finding.
- A helper declares names only, so it has no path to anchor.

## Gap

1. The order is reversed. `PathCandidates` (`lock/scan.go`) tries the root key first and the
   script's folder second. Two tests assert root first. It matters when the file exists in
   both places.
2. Scripts that declare the same text share one request (`scan.go`, merge by key).
   `pinFirstCandidate` pins the first file that exists, and `MatchPin` then counts every
   declaring script as answered. In the steps layout, `step1/run.sh` and `step2/run.sh` both
   write `x.sqf`, so only `step1/x.sqf` is pinned and `run` on `step2/run.sh` reports "not
   pinned". Read from the code, not yet run. The first test confirms it.

## Change

- `PathCandidates` lists the declaring script's folder first, then the root key.
- Pinning, `Reconcile` and `MatchPin` handle a path once per declaring script, so each script
  gets its own pin and its own "needs pinning" answer. `Request.Scripts` stays for display.
- `run` keeps using only the pin lookup.
- `ParseDeclaration` marks a `../` path external from its text alone, before any script is
  known. `reclassifyEscaped` then rescues it with the first declaring script only. With the
  script's folder first, `../shared/x.sqf` is an ordinary script-relative path, so the kind is
  decided per script and `reclassifyEscaped` goes away or folds into that step.
- `unpinnableReason` says "tried relative to the project root and relative to each script".
  The wording follows the new order.
- The root-anchor section of `internal/project/README.md` states the order and that `run`
  searches nothing.
- `docs/src/content/docs/guides/project.mdx` says paths are "relative to the project folder".
  It says script folder, then project folder, with one example of each layout.

Unchanged, because they read only the root-relative key: `restore/plan.go`, `publish/plan.go`,
`lock/verify.go`, `lock/schema.go`, `cmd/project.go` and `server/api_project.go`.

The helper history in `project/lock_cmd.go` (`classifyOverlay`) joins a recorded `-o` path
to the helper's own work directory. It only suggests pins and creates no finding, so it
stays as it is.

## Tests

- Written first, expected to fail today: `step1/x.sqf` and `step2/x.sqf` both exist, `lock`
  pins both, and `run` resolves each script to its own file.
- The file exists in both places: the script's file is pinned.
- Only the root file exists: it is pinned under the root key.
- `step1/run.sh` declares `../shared/x.sqf`: it pins `shared/x.sqf`, not an external finding.
- Replace the two tests that assert root first, and the `reclassifyEscaped` tests if the
  function goes. Nothing else about path candidates changes.

## Open

- Both candidates exist: the script's file wins with no message. Whether `lock` should say
  which one it chose is not decided.
