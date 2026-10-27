# First stable release: CLI and schema review

Status: open. Decide each item, then mark it.

Scope: the binary's user-facing surface (flags, config keys, env vars, file schemas).
Checked by building the binary and running it with `CNT_ROOT` and `XDG_*` isolated.
Not checked: `run` and `create` flag tables, `helper-config` and helper-history schemas, dashboard API JSON.

## 1. Server port help says "saved to config"

- Where: `cmd/server.go:292` (`server start -p`), `cmd/server.go:295` (`server restart -p`).
- Fact: the port is written to a state file (`config.SaveServerPort`). `server.port` is not a config key.
- Fix: say the port is remembered for the next start.
- Decision: Do. Done.

## 2. `e` has no `--load-gpu`

- Fact: `exec` has `--load-gpu`. `e` is its shortcut and lacks it.
- Effect: with `autoload_gpu` off, `e` cannot get a GPU.
- `e` also has no `-o`. Likely intended, since overlays are positional there.
- Fix: add `--load-gpu` to `e`.
- Decision: Do. Done.

## 3. `config list` renders empty values inconsistently

- `host_apptainer` is blank in `config list`, but `config get host_apptainer` prints `apptainer`.
- `bind` shows `none`. `exclude_nodes`, `proxy_per_job` and `scheduler.bin` show nothing.
- `default_distro` shows nothing plus a hint line.
- Fix: one rendering for "empty or unset". Show the effective default for `host_apptainer`.
- Decision: `list` prints the effective value, empty when none. `get` prints the stored value, `get -l` reads one layer. Done: empty lists print blank.

## 4. Aliases and naming in an unreleased tool

- `overlay export --no-builds` is an alias of `--no-build`.
- `scheduler -Q/--queue` is an alias of `-p`.
- `overlay export --no-md5` and `--channel-subdir` use a different style from `--from-history` and `--explicit`.
- Fix: drop the aliases. Optionally rename the two flags.
- Decision: export flags mirror micromamba, so keep. Remove `scheduler -Q`. Done.

## 5. Env vars the code reads and the docs do not mention

- `CNT_TMPDIR`: user-facing, only in package READMEs.
- `CNT_BIND`: env var, not a config key, not in user docs.
- `CNT_ROOT`, `CNT_EXTRA_ROOT`: look like user settings.
- `CNT_LIBEXEC`, `CNT_CONDA_ROOT`: unclear if user-facing.
- Options: document the user-facing ones on the Configuration page, or leave them undocumented.
- Decision: no change.

## 6. `credentials.json` has no `schema_version`

- Lock, manifest, runtime metadata, helper installed record, catalog index and artifact cache all have one.
- `credentials.json` is a user file holding secrets, with `auths` and `sources` maps.
- Adding the field after release needs a migration.
- Decision: no change.

## 7. `config.yaml` has no schema version

- Renames go through `config check --fix`. The `Replaces` and `Removed` options have only test entries.
- Leaning: fine as is.
- Decision: no change.

## 8. Short flags mean different things per command

- `-e`: `--exact` in `list`, `--expand` in `avail`, `--explicit` in `overlay export`.
- `-p`: prefix, profile or path, by command.
- `-o`: overlay in `exec` and `helper run`, output in `run`.
- `-w`: writable in `exec` and `run`, cwd in `helper run`.
- Already decided: by design. Recorded here only.
- Decision: no change.

## 9. `--project` default wording

- `project` subcommands: "the current directory".
- `check`, `exec`, `run`: "the nearest project above the current directory".
- The behaviors differ on purpose. The help text could say so.
- Decision: no change.
