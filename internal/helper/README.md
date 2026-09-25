# internal/helper

Runs the lifecycle of a helper service job: resolve params, check overlays, submit, watch, stop.

## Submitting

- Headless or scheduler is chosen automatically, and `RunOptions.NoSubmit` forces headless for one run.
- `NoSubmit` is per run because the server is one long-lived process serving concurrent requests. Global config cannot be flipped for one caller.
- A failed submit never falls back to headless. Running a compute-node job on a shared login node needs consent.
- The error names the override instead.
- Every run records `Runner`: `local`, or the scheduler type.
  - `JobID` and the early status only say which path it took until the run is `running`. After that nothing else does.
- Every run records `Connect`, the `helper.connect` value at submission.
  - The bind address is fixed at submission. Only `direct` binds all interfaces.
  - The dashboard that connects later may be a restarted process, so it reads the run, not current config.
- Account and partition resolve the same way on every entry point: flag or dashboard field, then config.
  - They are applied once, in `buildHelperScriptSpecs`.
  - There is no script-header tier. `#NCPUS:` and `#MEM:` describe what the workload needs. Account and partition describe the user's own cluster access, which a script cannot know.

## Overlays

- `#REQUIRED_OVERLAYS:` order is the overlay order. The last name is topmost and wins file conflicts.
- Standing in a project, a name resolves as `exec -o` does, and a helper never adds a pin.
- A name nothing installed answers is built as it is outside a project.
- A pinned artifact absent on this machine is refused with a pointer to `project restore`.
- A named overlay is a fixed requirement. It is built without a prompt.
- Names resolve against what is installed before anything is built, so a satisfied name costs no network call.
- `#IMG_PACKAGES:` is two requirements: the packages are installed, and a writable form exists to install into.
  - A bare `.sqf` snapshot can satisfy the first. It cannot satisfy the second, so the plan refuses it and guided creation runs.
  - Guided creation still mounts the paired snapshot during the install. Only the difference lands in the new `.img`.
- The package check reads the `.img` and its paired snapshot together.
  - A thin `.img` on a frozen snapshot holds only the newest delta. Checking it alone would block every helper on a good environment.
- `CheckEnv` reports size and snapshot through the paired lookups for the same reason.

## Stopping

- A stop is a SIGTERM to the wrapper. The scheduler's cancel signals only the batch shell, and headless runs signal the process group.
- The wrapper runs the container in the background and `wait`s, since bash defers a trap while a foreground command runs.
- Its trap forwards TERM to `condatainer exec` and keeps waiting, so images are unmounted before `done` is recorded (exit code 130).
- `--stop-grace` is `stopGraceSecs`, kept inside the scheduler's TERM-to-KILL delay.
  - Killing a writable image's `fuse2fs` mount leaves the image needing recovery.
  - Nothing in the wrapper kills the container before that delay expires.
- Before the container is launched, an earlier trap records `done` and cleans up directly.
- The forwarding trap is armed just before the launch. A stop that lands before the container has a pid is forwarded once it has one.

## Waiting

- `condatainer helper <name>` returns once the job is submitted.
- A hidden `_helper_watch` process keeps printing messages and the access URL until `ready`.
- It writes straight to a terminal the shell may be waiting on, so each batch starts on a new line.
- Without a terminal, or with `--wait`, the command waits itself. Ctrl+C there detaches and never stops the helper.
- Later state, including `done`, is the dashboard server's to record.

## State files

- The compute node writes `ready`, `done` and `messages`. The login node reads them.
- They sit on shared storage because the two never share a host.
- `_server_ready` waits for the port to accept connections before writing `ready`, so `ready` means listening.
  - The wait has a timeout so a service that never binds cannot hang the script. It reports ready with a warning.
  - Port `0` with an external URL needs no local port and no tunnel.
- `StateDir` trusts `CNT_HELPER_STATE_DIR` inside a run, so path resolution inside the container cannot send messages elsewhere.

## Headless liveness

- A headless wrapper has no scheduler to ask.
- Several login nodes can share one state directory, so a pid means nothing off its own host.
- The wrapper starts `_helper_hold`, which takes an `fcntl` lock on `{state dir}/lock` and records the owner.
  - Locks are visible from every host, and the kernel drops one when its holder dies, however it dies.
  - The holder ignores TERM, so a stop's group signal cannot release the lock before `done` is recorded.
- `HeadlessLiveness` has three answers.
  - Contended: alive.
  - Free with an owner recorded: gone.
  - Free and never written, or no file: unknown. It claims nothing, and `done`, ready and walltime remain the backstops.
- `KillHeadlessProcess` signals only on the recorded host. Elsewhere it returns `ErrOtherHost`, and neither stop path closes out a run it could not signal.

## Shared history

- It records which overlay combinations a project's helpers ran with, so the next person does not rediscover them.
- It lives in the project's `cnt-lock/.helper-history/`, one file per combination.
- It is used only inside an existing project. It never runs `project init` to make a place for itself.
  - A project's presence already changes overlay resolution, so creating one could break the launch.
- Dedup is by content and recency is the file mtime. A repeat bumps the mtime.
  - The bump uses `UTIME_NOW`, not `os.Chtimes`, so any member of a group-writable `cnt-lock/` can do it.
- What is offered back is what the user chose: the `-o` overlays and the params behind `#REQUIRED_OVERLAYS:`.
  - The params are not stored. They are recovered by matching the recorded names against the helper's current template.
  - `r/4.4.3` against `r/{POSIT_R}` gives `POSIT_R=4.4.3`, so the same version is restored.
  - `#IMG_PACKAGES:` params are never recorded or recovered. They control the env overlay's content.
  - A bare required name such as `rstudio-server` is recorded as declared, so it still follows the latest.
- A run with no added overlays and no recoverable param has nothing to offer, so it is skipped.
- Reuse is offered, never applied silently. The CLI offers the newest on a fresh start. The web UI has an "Overlay history" button, enabled only when there is something to reuse, that opens a list to choose from.
- The same records feed the project's "used but not pinned" signals.
- It is a separate package, `internal/helperhistory`, because `internal/project` reads it and `internal/helper` already imports `internal/project`.
