---
title: Environment Overlay
description: Keep a writable overlay, add packages and other software to it, and freeze it.
---

An environment overlay provides a writable workspace (`env.img`) where you can actively install Conda packages and other software.

Once your environment is complete, you can "freeze" it into a read-only snapshot (`env.sqf`).

If both files exist, they act as a **pair**: loading the `.img` automatically loads the underlying `.sqf` with it.

:::note
If you already have a fixed list of Conda packages, use `condatainer create` directly. See [create overlay from Conda packages](overlays.md#from-conda-packages).
:::

## Create

To create a new writable environment overlay in current directory:

```bash
condatainer overlay create                       # a default 10GiB env.img
condatainer overlay create -s 20G                # with 20GiB
condatainer overlay create -- python=3.12 numpy  # with pre-installs packages
condatainer overlay create -f environment.yml    # using a Conda env file
```

:::tip
Reading and writing a writable layer is slower than host fast storage. If you know the packages beforehand, give them when you create it: `overlay create -- <packages>`.
:::

### Size and Files

```bash
condatainer info env.img                            # Size, disk and inode usage, owner, packages
condatainer overlay resize env.img -s 30G           # Grow or shrink
condatainer overlay create big.img -s 40G -p large  # Few, large files
condatainer overlay create tmp.img -s 20G --sparse  # Take disk space only as it fills
```

- `--sparse` does not allocate the whole size in advance. The file takes disk space only as it fills.
- An overlay holds a fixed number of files, set when it is created. A file or directory uses one **inode**. `-p` picks how many the overlay gets for its size:

| Profile | Use |
|---|---|
| `small` | Many small files, such as Conda and Python packages. |
| `balanced` (default) | General use. |
| `large` | Few large files, such as genomes and databases. |



## Make Modifications

### Add Conda packages

To modify your environment, start an interactive shell. The `e` command automatically loads the `env.img` found in your current directory in writable mode:

```bash
condatainer e
# Shorthand for: condatainer exec -o env.img -w
```

Inside the container, use `mm` to manage Conda packages. `mm` is a shell function for `condatainer env`:

```bash
mm install r-base=4.4 r-tidyverse   # Install new packages
mm install pytorch -c pytorch       # Install from a specific channel
mm list                             # List installed packages
mm remove r-tidyverse               # Remove a package
mm update --all                     # Update all installed packages
```

- Tab completes the `mm` commands.
- `condatainer shell init` sets `mm` up for bash, zsh and fish. The installer offers it.
- In fish, `mm install`, `mm update` and `mm remove` re-run only the `.fish` activation scripts of packages. Most packages ship `.sh` scripts, which apply in a new container shell.

### Install R packages

You can also use your writable environment to overwrite OS overlays files, like installing R packages:

```bash
condatainer e r/4.4.3 r-build-essential env.img -- R
```

This opens an interactive R terminal where you can install packages directly into the writable overlay:

```r
.libPaths() # Verify the package installation path
# [1] "/opt/R/4.4.3/lib/R/library" (to writable overlay)
install.packages("pak")
library(pak)
```

### Set environment variables

Some software ecosystems rely on environment variables to determine where packages are installed (e.g., `GOPATH` for Go). You can permanently bind variables to your overlay by creating an `.env` file right next to it:

```bash title="env.img.env"
GOPATH=/ext3/go   ## Path where Go packages will be stored
```

- Text after `##` is treated as a note and will be displayed when the container starts.
- When you freeze the environment later, these variables are permanently baked into `env.sqf`.

```bash
# In container
go env GOPATH
go install honnef.co/go/tools/cmd/staticcheck@latest
go install golang.org/x/tools/cmd/deadcode@latest
```

## Writable vs. Read-Only Modes

An `env.img` file can be opened in two different modes:

| Mode | Use Case | Commands |
|---|---|---|
| **Writable** | Exclusive access (only one container can use it at a time) | `e`, `exec -w`, `run -w` |
| **Read-only** | Shared access (multiple containers at the same time) | `exec`, `run`, `e -r` |

- If a container opens the overlay in writable mode, no other container can use it until the first one exits.
- If the overlay is currently in use by read-only containers, it cannot be opened in writable mode.

## Check and Repair

```bash
condatainer overlay fscheck env.img      # Check, and repair what it can
condatainer overlay fscheck env.img -f   # Check even if it is marked clean
```

See [Environment Overlay Issues](../troubleshooting/environment_overlay.md) for an overlay that was not shut down cleanly.

## Run with it

`exec` uses the environment read-only by default:

```bash
condatainer exec -o env.img python src/train.py
condatainer exec -o r/4.4.3 -o r-build-essential -o env.img Rscript src/script.R
```

In a script run with `condatainer run`, declare it like any other overlay:

```bash title="src/run_r.sh"
#!/bin/bash
#DEP: r/4.4.3
#DEP: r-build-essential
#DEP: env.img

Rscript src/script.R
```

Then execute it:

```bash
condatainer run src/run_r.sh
```

## Export the Conda Environment

```bash
condatainer overlay export env.img                 # environment.yml on standard output
condatainer overlay export env.img -p env          # Write env.yml
condatainer overlay export env.img --from-history  # Only the packages you asked for
condatainer overlay export env.img -e              # Explicit spec, an exact list for create -f
```

:::caution[Conda packages only]
The export lists Conda packages and nothing else. It does not include R packages installed with `install.packages`, files you copied in, or anything installed another way.

If the environment holds anything other than Conda packages, [freeze](#freeze) it and push the `env.sqf` instead. See [Publish to a Registry](project.mdx#publish-to-a-registry).
:::

## Freeze

Freezing packs the contents of `env.img` into an immutable, highly-compressed `env.sqf` snapshot.

```bash
condatainer overlay freeze env.img   # Creates env.sqf and removes env.img
```

- After freezing, commands like `e` will automatically use the new read-only file.
- **Important:** An `env.sqf` cannot be rebuilt from a recipe, so keep a copy.

### Add packages after freezing

To add packages afterwards, create a new `env.img` beside it:

```bash
condatainer overlay create   # Creates an empty env.img
# ├── env.img  (writable, captures new changes)
# └── env.sqf  (read-only, contains previous packages)
```

The new `env.img` and the `env.sqf` snapshot now act as a pair:

- Using the `.img` also loads the `.sqf` underneath.
- Any new packages you install will land in the `.img`.

For example, installing a new R package:

```bash
# Automatically loads the base env.sqf underneath env.img
condatainer e r/4.4.3 r-build-essential env.img -- R --slave -e '
pak::pak("tidyverse")
library(tidyverse)
'
```

When you are finished, freeze the environment again. CondaTainer will merge the new `.img` into the existing `.sqf`:

```bash
condatainer overlay freeze env.img
```

## Advanced

### Group Use

When sharing a writable environment among multiple users, file ownership rules determine who can make changes.

A writable `.img` overlay consists of two internal directories:
- **`upper`**: All file changes (installations, edits, deletions) land here.
- **`work`**: A scratch space used by the kernel to apply changes.

**File Ownership:** The `upper` directory belong to the User ID (UID) of the person who created them.
- Normally, you retain your own UID inside the container, meaning you own the files you create.
- If you use the `--fakeroot` flag, you appear as `root` (UID 0) inside the container. And the `upper`/`work` need to be owned by `root`
- If you attempt to load a writable overlay that contains files owned by a different UID or GID, you will encounter permission errors. See [Fix Ownership](#fix-ownership).

**Why `.sqf` is different:**
When CondaTainer creates a read-only `.sqf` file, it stores every file internally as owned by `root` (UID 0 / GID 0). During running, Apptainer will call `squashfuse_ll`, which maps these files to the user running the container.

**Best Practice for Sharing:**
To safely share an environment with a team, freeze the base environment into a shared `env.sqf`, and let each member create their own personal `env-<user>.img` on top of it:

```bash
# 1. The builds and freezes the base environment
condatainer overlay freeze env.img

# 2. Make the frozen snapshot readable by the group
chgrp xxxlab env.sqf
chmod g+r env.sqf

# 3. Each team member creates their own writable layer
condatainer overlay create env-$USER.img

# 4. Starting the shell with user's img + shared sqf
condatainer e
```

- Everyone shares the same base read-only software without ownership conflicts.
- `env-<user>.img` will pair with `env.sqf`.
- If `.img` is missing, `condatainer e` will load the shared `env.sqf`.

### Fix Ownership

Files owned by another user ID make a writable overlay fail with permission errors. `overlay chown` changes the owner without mounting:

```bash
condatainer overlay chown env.img                    # Everything owned by you
condatainer overlay chown env.img --root             # Owned by root, for --fakeroot
condatainer overlay chown env.img -u 1001 -g 1001    # A specific owner
condatainer overlay chown env.img -p /ext3 -p /data  # Only these paths inside the overlay
```
