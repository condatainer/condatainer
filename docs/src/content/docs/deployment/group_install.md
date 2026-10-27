---
title: Group Install
description: Install CondaTainer once in a shared directory, for a whole group.
---

A group installs CondaTainer once, in a directory every member can reach. Each member runs one command to set up their shell, and overlays built there serve the whole group.

## 1. Pick a directory

The directory must be:

- **owned by the group and group-writable**, since members build overlays into it;
- **reachable and writable from the compute nodes**, since jobs read overlays from it, and builds submitted as jobs write overlays into it.

```bash
mkdir -p /path/to/lab/condatainer
chgrp <group> /path/to/lab/condatainer
chmod 2775 /path/to/lab/condatainer   # group-writable, with the setgid bit
```

The setgid bit (the `2`) makes everything created inside belong to the group.

## 2. Run the installer

```bash
curl -fsSL https://install.condatainer.org | bash -s -- -p /path/to/lab/condatainer
```

The installer:

1. Creates `bin/` in the directory and downloads `condatainer` into it.
2. Generates the group's `config.yaml`.
3. Downloads the toolchain into `libexec/`: Apptainer with the tools it uses, and Micromamba. Asks first.
4. Offers to set up your own shell. You are a member too, so say yes.

## 3. Add members

Each member runs this once, from their own account:

```bash
/path/to/lab/condatainer/bin/condatainer shell init
```

It puts the group's `bin/` on their `PATH`, loads tab completion, and defines `mm` in containers.

Check from a member's account:

```bash
condatainer config paths
condatainer avail
```

## Where overlays go

- A member builds into the group's directory, and one copy serves the whole group.
- CondaTainer makes what it creates there group-writable.
- A member's own copy of an overlay is used before the group's, for that member only.

## Notes

### Keep settings and updates to one person

To let only the person who installed CondaTainer change its settings or update it, remove the group's write access to those parts:

```bash
cd /path/to/lab/condatainer
chmod g-w config.yaml
chmod -R g-w bin libexec
```

- Members still build overlays into the group's directory.
- A member's `condatainer config set` writes to their own [config](../guides/config.md) instead.
- `self-update` and `update --libexec` then work only for that person.

### Share files you write in a project directory

CondaTainer makes the overlays it builds group-writable. Files your own scripts and jobs write follow your `umask`, the default permissions your shell gives new files. A `umask` of `022`, common on clusters, lets the group read those files but not change them.

To let the group write them, add this to each member's `~/.bashrc` or `~/.zshrc`, or to the top of a job script:

```bash
umask 002
```

- The setgid bit on a shared directory keeps new files in the group, and `umask 002` lets that group write them.
- It applies to every file the shell creates afterwards, not only project files.
