# Plan: one layout for a `cmd/` file

Status: not implemented. Nothing in `cmd/` moves until the layout below is agreed.

## Goal

A `cmd/` file reads top to bottom the same way in every file. A reader who knows one command
file finds the flags, the registration and the helpers of another in the same places. The rule
is written in `CLAUDE.md`, so a new command follows it without a survey.

## Current state

The files do not agree. Measured on the current tree:

| File | Order |
|---|---|
| `store.go` | group var, `init()`, the three commands together, then the helpers in the order the commands use them |
| `store_gc.go` | the two commands, then their helpers |
| `helper_scripts.go` | the command vars, `init()`, then the `run*` implementations in command order, with shared helpers beside their first user |
| `config.go` | shared helpers and completions first, then the group, then the commands in help order, `init()` last |
| `overlay.go` | create's help and implementation, then create's command, the other commands, `init()`, then the export helpers |
| `project.go` | each command followed by some of its helpers, but the helpers shared by `validate` and `registry push` sit between `registry gc` and `validate`, and `reportLockState` sits after `restore` |

Which commands get their own file is also left to judgement. `CLAUDE.md` says "One file per
subcommand in `cmd/`". That holds for the top-level commands (`run.go`, `check.go`, `exec.go`).
A group's members live in the group file, and only the larger ones are split out (`store_add.go`,
`store_gc.go`, `overlay_freeze.go`, `helper_scripts.go`, `config_source.go`).

## Design

### Order inside a file

Recommended, since it is what `store.go` and `helper_scripts.go` already do:

1. The group or command variable, then its flag variables.
2. The constructors or command vars, in the order `--help` lists them.
3. `init()`: the registration of the commands and their flags.
4. The implementations (`runXxx`), in the same order as the commands.
5. Helpers, each after the first function that uses it. A helper shared by several commands
   goes after the last of them.

- A command and its implementation are never separated by another command.
- Flag variables are local to the constructor when the constructor builds the command, and
  package variables only when a helper outside the constructor reads them.
- A file that holds a group registers the group's commands in one `AddCommand` list.

### Which commands get their own file

- A top-level command has one file, named after it.
- A member of a group stays in the group file. A command that only builds its flags and
  calls into `internal/` is not worth a file.
- A member gets `<group>_<name>.go` only for shared or complex logic, judged case by case
  when the code is written. No count of functions or lines decides it: the files that exist
  today do not follow one.
  - Shared: several members use the logic, such as `helper_scripts.go` for the commands that
    share the layer flags and the source list.
  - Complex: the logic is a flow of its own, such as freeze and unfreeze.
- The group file registers every member, wherever the member lives.
- Existing source files are not split or merged by this plan.

### Tests

- A test file sits next to the source file it tests and has its name: `store_gc.go` and
  `store_gc_test.go`.
- A member split into its own source file has its tests in its own `_test.go` file.
- The tests follow the order of the code they test.
- Merging the existing topic test files is not part of this plan.

### Where the rule is written

- `CLAUDE.md`, under "Code rules", as a short bullet list. The package READMEs hold decisions
  about behavior, and a file layout is neither.

## Changes

| File | Change |
|---|---|
| `CLAUDE.md` | the layout rule, and the line "One file per subcommand in `cmd/`" says top-level |
| every `cmd/*.go` that differs | reordered to the rule; no source file is split or merged by this plan |

## Stages

Each stage is staged for review. A reorder moves code and changes no line of it.

1. Agree the order and write it in `CLAUDE.md`.
2. Reorder the `cmd/` files that differ, one file per commit. The check for each is that the
   multiset of non-blank lines is the same before and after, `go build`, `go vet` and
   `go test ./cmd` pass, and `staticcheck ./cmd` reports nothing new.

## Tests

- No behavior changes, so no test is added.
- A reorder is accepted when the test suite passes unchanged and the diff is only moved lines.

## Open

- Whether stage 2 reorders every differing file now, or each file when a change touches it.
- Whether the rule covers `internal/` packages, or only `cmd/`.
