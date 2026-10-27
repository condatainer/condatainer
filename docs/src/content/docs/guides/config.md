---
title: Configuration
description: See and change CondaTainer settings, set lists of values, choose which layer to write, and override a setting with an environment variable.
---

A **setting** is a named value that changes how CondaTainer behaves, such as `build.ncpu` (the CPUs a build job asks for) or `bind` (host directories added to every container). Settings are saved in **config files**, one per [layer](layers.md#layers).

## See and change a setting

```bash
condatainer config init               # Create a config file with default values
condatainer config list               # Every setting and its current value
condatainer config get build.ncpu     # One setting
condatainer config help build.ncpu    # What it does, what it accepts, its default
condatainer config set build.ncpu 8   # Change it
condatainer config remove build.ncpu  # Go back to the default
```

- `config init -l <layer>` creates the file of a specific [layer](#choose-the-layer).
- `config list --origin` tags each value with the layer or environment variable that sets it, or `[default]`.
- `config set` prints the value it replaced.

## Lists of values

Some settings hold a list, such as `bind` or `channels`. `config help <key>` says when a setting takes a list.

```bash
condatainer config set bind /data/ref:/ref:ro /scratch/shared   # Replace the whole list
condatainer config append bind /data/extra                      # Add a value at the end
condatainer config prepend channels bioconda                    # Add a value at the front
condatainer config remove bind /data/extra                      # Remove a value
```

- `append` and `prepend` add one value per command, and move a value that is already in the list.
- `remove` takes one value at a time.
- `--bind` on `exec`, `e` and `run` adds to the `bind` list for that command.
- The lists of all layers are combined. `channels` is the exception: the layer with the highest priority that sets it wins.

## Choose the layer

The layers are `user`, `extra-root` (a lab group's shared file) and `cnt-root` (the install's file). When two files set the same setting, `user` wins over `extra-root`, and `extra-root` over `cnt-root`.

```bash
condatainer config set default_distro ubuntu24 -l cnt-root
condatainer config get build.ncpu -l user
```

- With `-l`, the command reads or writes that layer's file.
- Without `-l`, a change goes to the first of these files that exists: `user`, `extra-root`, `cnt-root`. If none exists, your `user` file is created.
- Without `-l`, a file you cannot write is skipped and the change goes to your `user` file, with a warning.

## Override with an environment variable

An **environment variable** is a named value your shell passes to the programs it starts. Each setting has one: `CNT_CONFIG_` followed by the setting name in capitals, with `.` written as `_`. `config help <key>` shows the name.

```bash
CNT_CONFIG_BUILD_NCPU=8 condatainer create samtools/1.22
export CNT_CONFIG_BIND="/data/reference:/ref:ro|/scratch/shared"
```

- An environment variable overrides all config files.
- Separate the values of a list with `|`.
