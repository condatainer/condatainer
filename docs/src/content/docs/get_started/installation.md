---
title: Installation
description: Install CondaTainer for personal use on Linux.
---

## Prerequisites

- **Linux** on amd64 (x86_64) or arm64 (aarch64).
- **Host Apptainer or Singularity** is optional. CondaTainer builds `.def` files with the downloaded Apptainer, or with `host_apptainer` if set.

**NOTE**: Slurm is the primary supported [scheduler](../guides/scheduler.mdx). PBS (PBS Pro or OpenPBS) and LSF support is experimental.

## Install

```bash
curl -fsSL https://install.condatainer.org | bash
```

The script asks where to install, whether to set up your shell, and whether to download the toolchain.

:::note
To install for a group or a whole cluster, see [Group install](../deployment/group_install.md) or [Site install](../deployment/site_install.md).
:::

## Verify

Restart your terminal, or run `source ~/.bashrc`. Then list the available [recipes](quick_start.mdx#find-an-overlay-source):

```bash
condatainer avail
```

## Set up your shell

If you skipped shell init, or changed shell, run it yourself:

```bash
condatainer shell init # Host only
```

- It supports bash, zsh and fish, and detects the one you are running. Use `--shell` to name another.
- It writes a block marked `# >>> CONDATAINER >>>` to `~/.bashrc` or `~/.zshrc`, or the file `~/.config/fish/conf.d/condatainer.fish`. Text outside the block is never changed.

## Adjust configurations

CondaTainer needs Apptainer, Micromamba and other tools. They are downloaded into `libexec`. If that was skipped or failed, download them later:

```bash
condatainer update --libexec
```

To build from Singularity `.def` files with the host's Apptainer instead:

```bash
module load apptainer/1.5
condatainer config set host_apptainer "$(command -v apptainer)"
```

## Uninstall

1. **Remove the shell setup:**

   ```bash
   condatainer shell remove
   ```

   It deletes the block marked `# >>> CONDATAINER >>>` from your `~/.bashrc` and `~/.zshrc`, and the fish file. Use `--shell` to remove from some only.

2. **Remove the data directories.** Check which exist:

   | Location | Holds |
   |---|---|
   | `<install-dir>/` | The `condatainer` binary, the toolchain and shared overlays. |
   | `$SCRATCH/condatainer/` | Personal overlays, when `$SCRATCH` is set. |
   | `~/.local/share/condatainer/` | Personal overlays (or `$XDG_DATA_HOME/condatainer/`). |

3. **Remove personal config.**  `~/.config/condatainer/` (or `$XDG_CONFIG_HOME/condatainer/`).

4. **Remove state and cache:** `~/.local/state/condatainer/` and `~/.cache/condatainer/` (or under `$XDG_STATE_HOME` and `$XDG_CACHE_HOME`).
