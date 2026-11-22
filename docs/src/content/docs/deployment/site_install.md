---
title: Site Install
description: Install CondaTainer cluster-wide for all users from a central maintaining account, with support for group-level shared overlays.
---

A **site install** provides CondaTainer cluster-wide to all users from a central maintaining account. In this model:
- The site installation (`cnt-root`) is read-only for regular users.
- Users read central overlays provided by the cluster and build personal overlays into their own accounts.
- Research labs can layer their own shared group directories (`extra-root`) on top without needing cluster administrator privileges.

## Prerequisites

Before installing CondaTainer cluster-wide, ensure that the compute and login nodes satisfy:
- **Unprivileged user namespaces:** Required by container runtimes for unprivileged execution. Ubuntu 24.04 and later blocks them by default, see [Ubuntu 24.04 and later](#ubuntu-2404-and-later).
- **FUSE support:** The `/dev/fuse` device must be available with read/write permissions for unprivileged users.

## 1. Run the Site Installer

Log in as the maintaining account (e.g., `condatainer-admin` or a dedicated software account) and install into a clean, dedicated directory:

```bash
curl -fsSL https://install.condatainer.org | bash -s -- -p /opt/condatainer --no-shell -y
chmod 644 /opt/condatainer/config.yaml   # Only if your umask is stricter than 022
```

With `--no-shell`, the installer leaves your own shell alone and:
1. Creates `bin/` and downloads the `condatainer` binary.
2. Creates the site [configuration](../guides/config.md) file, `/opt/condatainer/config.yaml`.
3. Downloads the toolchain into `/opt/condatainer/libexec/` (including container runtime tools, fuse helpers, and `micromamba`).

The `chmod` keeps the configuration readable by everyone. It is needed only with a stricter umask.

:::note
Do not set `-p /usr` or `-p /usr/local`. CondaTainer treats its installation directory as both a binary home and a shared overlay repository (`cnt-root`). Install into a dedicated directory like `/opt/condatainer` or `/shared/software/condatainer`.
:::

## Ubuntu 24.04 and later

These versions block user namespaces for programs with no AppArmor profile, so the Apptainer in the toolchain is refused. Check with `sysctl kernel.apparmor_restrict_unprivileged_userns`. A value of `1` means it is blocked.

Allow that one Apptainer with a profile. As root, write the profile with the real path of the toolchain directory, then load it:

```bash
cat > /etc/apparmor.d/condatainer-apptainer <<'EOF'
abi <abi/4.0>,
include <tunables/global>

profile condatainer-apptainer /opt/condatainer/libexec/{bin/apptainer,lib/apptainer/bin/starter} flags=(unconfined) {
  userns,

  include if exists <local/condatainer-apptainer>
}
EOF
apparmor_parser -r /etc/apparmor.d/condatainer-apptainer
```

- Write the real path, with no symlinks. The error message names the path that was refused.
- Do this on every node that runs containers.
- The profile names paths, so `update --libexec` does not change it.
- Keep `flags=(unconfined)`. Without it the profile confines the program, and it fails before it starts.
- The profile allows user namespaces for that Apptainer and the programs it starts. Other programs stay restricted.
- The other way is `sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0`. It lifts the restriction for every program on the machine.

## 2. Configure Cluster Defaults

Global defaults for all users are stored in the installation directory's `config.yaml`.

You can inspect and set site-wide configurations using `-l cnt-root` (or `-l r`):

```bash
# View active settings
condatainer config list --origin

# Example: Set default scheduler or default distro cluster-wide
condatainer config set default_distro ubuntu24 -l r

# Example: Use the site Apptainer instead of the toolchain's
condatainer config set host_apptainer /usr/bin/apptainer -l r
```

If the cluster provides a curated Apptainer installation (or module), setting `host_apptainer` makes it run containers and build `.def` files in place of the unprivileged binary in `libexec/`. Unset, no Apptainer outside `libexec/` is used.

- It must be Apptainer 1.4 or newer.
- A build uses the fakeroot from `libexec/`, which runs in images of any age.
- A non-setuid Apptainer that ships its own fakeroot cannot take it, so its builds use the one in `libexec/` and print a warning.

Individual users can override any site setting in their own `~/.config/condatainer/config.yaml`.

## 3. Make the Command Reachable

Provide the `condatainer` executable to all users using one of the following methods.

*(If your cluster uses Environment Modules or Lmod, see [Site Install with Modules](site_modules.md) instead).*

**Option A: Symlink into a shared `bin` directory** (Recommended)

Link the executable into a location already on every user's default `PATH`:

```bash
ln -s /opt/condatainer/bin/condatainer /usr/local/bin/condatainer
```

CondaTainer resolves symlinks automatically, so it will continue using `/opt/condatainer` as its root install directory.

**Option B: System-wide environment profile**

Alternatively, export the `bin/` directory via a shared profile script:

```bash title="/etc/profile.d/condatainer.sh"
export PATH="/opt/condatainer/bin:$PATH"
```

**Shell Completion**

To enable Bash tab-completion for all users, generate the completion script into the system completion directory:

```bash
condatainer shell completion bash > /etc/bash_completion.d/condatainer
```

## 4. Provide Shared Overlays

Overlays built by the maintaining account land in the central site directory (`cnt-root`), where every user can read and use them immediately:

```bash
# Provide standard base OS overlays
condatainer create ubuntu24/base

# Provide common high-use tools
condatainer create samtools/1.22
condatainer create bwa-mem2/2.2.1
```

Because regular users do not have write access to `/opt/condatainer`, any overlays they create will automatically be stored in their personal directory or their lab's group directory.

## Group Layers on Top

Individual research groups or labs can add their own shared layer over the central site installation. This allows lab members to share datasets and tools without requiring administrator intervention.

**1. Set up the group directory**

The lab creates a shared directory owned by their POSIX group with the setgid bit enabled:

```bash
mkdir -p /shared/lab/condatainer
chgrp mylab /shared/lab/condatainer
chmod 2775 /shared/lab/condatainer   # setgid ensures new files belong to the group
```

**2. Configure member environments**

Each lab member adds `CNT_EXTRA_ROOT` to their `~/.bashrc` or `~/.zshrc`:

```bash
export CNT_EXTRA_ROOT=/shared/lab/condatainer
```

## Overlay Layer Resolution

With a site install and optional group layers, CondaTainer organizes software into hierarchical [layers](../guides/layers.md):

| Layer | Location | Managed By | Purpose |
|---|---|---|---|
| **scratch** | `$SCRATCH/condatainer` | User | Personal large storage (when `$SCRATCH` is defined). |
| **user** | `~/.local/share/condatainer` | User | Personal private overlays. |
| **extra-root** | `$CNT_EXTRA_ROOT` | Group / Lab | Shared team overlays and tools. |
| **cnt-root** | Site installation directory | Admin | Cluster-wide tools and base OS images. |

### How Layers Interact

- **Lookup (Nearest first):**  
  `scratch` → `user` → `extra-root` → `cnt-root`  
  When executing a command (`condatainer exec`), CondaTainer checks layers nearest to the user first. A personal overlay in `user` can safely shadow a shared group or site overlay.

- **Creation (Furthest first):**  
  `extra-root` → `cnt-root` → `scratch` → `user`  
  When building an overlay (`condatainer create`), CondaTainer automatically saves to the first writable layer in reverse order. If a user is part of a shared lab group (`extra-root`), new overlays land there so the entire team can reuse them.

You can inspect the active layer paths at any time:

```bash
condatainer config paths
```

## Maintenance and Updates

The maintaining account can update CondaTainer and its underlying toolchain independently:

```bash
condatainer self-update       # Update the condatainer CLI

condatainer update --libexec  # Update the libexec toolchain
```
