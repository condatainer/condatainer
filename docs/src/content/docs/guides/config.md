---
title: Configuration
description: See and change CondaTainer settings, set lists of values, choose which layer to write, and override a setting with an environment variable.
---

A **setting** is a named value that changes how CondaTainer behaves, such as `build.ncpu` (the CPUs a build job asks for) or `bind` (host directories added to every container). Settings are saved in **config files**, one per [layer](layers.md#layers). The [Configuration Reference](../reference/config.md) lists every setting, what it accepts and its default.

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

## Choose the layer

The layers are `user`, `extra-root` (a lab group's shared file) and `cnt-root` (the install's file). When two files set the same setting, `user` wins over `extra-root`, and `extra-root` over `cnt-root`.

```bash
condatainer config set default_distro ubuntu24 -l cnt-root
condatainer config get build.ncpu -l user
```

- With `-l`, the command reads or writes that layer's file.
- Without `-l`, a change goes to the first of these files that exists: `user`, `extra-root`, `cnt-root`. If none exists, your `user` file is created.
- Without `-l`, a file you cannot write is skipped and the change goes to your `user` file, with a warning.

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
- The lists of all layers are **combined**, in layer order: the values of `user` first, then `extra-root`, then `cnt-root`. A value that several layers list appears once, where the highest of those layers puts it.
- `channels` is the exception: the layer with the highest priority that sets it **wins**, and the other layers' values are ignored.

## Recipe sources

A **recipe source** is a collection of recipes, given as a URL or a directory, that `create` and `avail` search. The list of sources is the `sources` setting, kept in each layer's config file. Change it with `config source`:

```bash
# Every source in search order, with its layer
condatainer config source list

# Add one to user layer
condatainer config source add lab /shared/lab/recipes -l user

# Remove one from user layer
condatainer config source remove lab -l user
```

- `config set sources` is refused. And sources cannot be  configured via environment variable. Use `config source add`, `move` and `remove`.

The sources of all layers are combined, like other lists. When a recipe name is in more than one source, the first match wins. Two orders decide which source comes first:

- **Between layers:** `user`, then `extra-root`, then `cnt-root`. The public `cnt` source comes after all of them, unless a layer lists it.
- **Within a layer:** the order of that layer's list. A source you add goes last in its layer.

To change the order within a layer:

```bash
# Put a source first in its layer
condatainer config source add lab /shared/lab/recipes -l user --first

# Move a source before another one in the same layer
condatainer config source move lab --before site -l user
```

To write your own recipes, see [Custom Build Recipe](../advanced/custom_recipe.mdx). To share them through a source, see [Custom Recipe Source](../advanced/custom_source.mdx).

## Override with an environment variable

An **environment variable** is a named value your shell passes to the programs it starts. Each setting has one: `CNT_CONFIG_` followed by the setting name in capitals, with `.` written as `_`. `config help <key>` shows the name, and so does the [Configuration Reference](../reference/config.md).

```bash
CNT_CONFIG_BUILD_NCPU=8 condatainer create samtools/1.22
export CNT_CONFIG_BIND="/data/reference:/ref:ro|/scratch/shared"
```

- An environment variable overrides all config files.
- Separate the values of a list with `|`.
