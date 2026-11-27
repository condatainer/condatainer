# internal/utils/units

## Typed time and size

- `ParseTimeInput` and `ParseSizeInput` read what a person types for a job's time and memory, and an overlay's size: flags, config, headers, the web form.
- A time takes one of these forms. The first number is always hours.
  - `12`, or `1.5` with a decimal: hours.
  - `12:30`: hours and minutes. `12:30:15`: hours, minutes and seconds.
  - `1-12`, `1-12:30`, `1-12:30:15`: a day count in front of the above.
  - Words, in any case: `4d12h`, `2h30m`, `90m`, `1.5h`.
- A size is a number, with an optional decimal, then an optional unit.
  - No unit is GiB, the unit a job is sized in.
  - A unit is `K`, `M`, `G` or `T`, each also written with `B` or `iB`, in any case, after an optional space.
  - Every spelling is binary: `G`, `GB` and `GiB` are all 1024 MB. Displays write `GiB`, and a typed `G` has always meant 1024 MB.
  - A decimal is rounded up to whole MB, so a request is never below what was typed.
- Refused: zero, a negative value, minutes or seconds of 60 and over, a decimal beside `:` or `D-`, and any other unit or word. An empty value is unset.
- The first number of a time is hours, so `30:00` is 30 hours.
  - Slurm reads `30` as minutes and `30:00` as minutes and seconds. Nobody sizes a job in seconds, and one rule for everything typed is easier to hold.
  - What a scheduler wrote is read by that scheduler's own reader, with its meaning.
