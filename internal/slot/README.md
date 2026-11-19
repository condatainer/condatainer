# internal/slot

Reads `#SLOT:` headers, binds each slot to an overlay, and checks what the overlay provides.

## Decisions

- `run` and a helper share this package, so one declaration means one thing in both.
- A slot is a place, not a name. The script reads a variable, and the overlay bound to the slot sets it.
- A slot with no default must be bound, since a `#DEP:` always names an overlay. `optional=true` is the only way to leave one empty, and only a helper may write it.
- A choice can carry a short name, `choices=mouse=grcm39/genome/gencode`, which `--slot` and `default=` accept.
  - The author chooses the name, so no typed prefix is ever guessed. A prefix of a name was tried and removed, since a silent pick could mount the wrong genome.
  - Everything after parsing holds the full overlay: the default, the pins, the history, a submitted job and a condition. Only `--slot` and `default=` read a short name.
  - `=` always introduces a short name in these two options, so the old `name=version` spelling is not read there.
- `strict` limits the choices, not how a name is completed. A value that is a choice as written (its full name or short name) binds at once. Any other value resolves like a `#DEP:` name first, so `samtools/1` can reach `samtools/1.24`, and the overlay it resolves to must be exactly a choice.
  - A bare or partial name completes only to a version that starts with a digit, so a data overlay such as `grch38/genome/gencode` is written in full.
  - An unlisted value that nothing installed answers is refused and is never built.
  - Reusing a past run's slots is exact. A recorded overlay that is no longer one of the choices is dropped.
- A slot follows its script in a project too. The lock pins the default and the choices, and a `strict=false` slot bound to another overlay mounts it unpinned, with a note.
- `group=` binds slots that must change together, such as a genome and its index, with one `--slot GROUP=SHORT_NAME`.
  - It exists because pairing a genome with an index of another species is silent and wrong. One value cannot make that mistake.
  - The group value is a short name, so every slot of a group lists the same short names and has a short name on every choice. Checking this when the script is read means a bound group always fits every slot.
  - Defaults are all given or none, with one short name. A group with a default for one slot and not another would bind half of it.
  - A group is not written with one of its slots, and if one slot is written, all are. A half-bound group is refused, and an overlay outside the choices needs `strict=false` and every slot written.
  - A group is a condition subject matched against its short name. It has a value only when all its slots hold choices of one short name. Otherwise it matches nothing.
  - Only `run` reads `group=`. A helper's slots are mostly optional, and the dashboard fills one slot at a time, so a half-filled group would be refused there.
- `strict` is on by default, so a script that lists its choices is not run with another overlay by accident.
- `env=` reads the overlay's `runtime.json`. Nothing is mounted to check it.
- `bin=` lists the overlay's `PATH` folders in the image and looks for the command.
  - Nothing is recorded in the overlay, so the answer is always true and works for overlays built earlier.
  - Only a `.sqf` is listed.
- Two slots that list one variable in `env=` must export the same value. Otherwise one overlay would overwrite the other's variable.
- A submitted `run` job repeats each slot by exact name, so a newer overlay installed while it waits cannot change what it mounts.
- A missing overlay is built by a helper launch and reported by `run`, the same as for `#DEP:`.
- `subject` matches a condition against a bound overlay's exact name. It is a package of its own because `scheduler` reads conditions, and `slot` imports a package that imports `scheduler`.
- A plain condition value matches by whole parts, after a `/` or a `.`, so `gencode/4` matches `gencode/4.1` and not `gencode/47`.
- `regex:` marks a regular expression, which is used as written and matches anywhere in the name unless it holds `^` or `$`. A module name never holds `:`, so the prefix is unambiguous.
