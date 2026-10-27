---
title: Site Install with Modules
description: Deploy CondaTainer cluster-wide as an Lmod or Environment Modules module.
---

On HPC clusters that use an environment module system (such as **Lmod** or **Environment Modules**), you can deploy CondaTainer as a module. Users then load the environment using `module load condatainer`. For an install without modules, see [Site Install](site_install.md).

## Architecture Overview

A modular site installation separates versioned binaries and toolchains from the shared overlay repository:

| Directory | Scope | Purpose |
|---|---|---|
| `/apps/condatainer/<version>/bin/` | Per version | Contains the `condatainer` executable. |
| `/apps/condatainer/<version>/libexec/` | Per version | Contains that version's toolchain. |
| `/shared/condatainer/` | Shared across versions | Central [`cnt-root`](../guides/layers.md) storage and config. |

Because `/shared/condatainer/` is persistent across module releases, updating or installing a new CondaTainer version never requires rebuilding existing overlays.

## 1. Download the Release Binary

As the maintaining account, download the target version into its directory:

```bash
curl -fsSL https://install.condatainer.org | bash -s -- \
  -p /apps/condatainer/<version> --version <version> --download-only
```

## 2. Create the Shared Install Directory

Create the shared repository directory:

```bash
mkdir -p /shared/condatainer
```

Regular users only need read and execute permissions here; write access is reserved for the software maintaining account.

## 3. Write the Modulefile

The modulefile adds the versioned `bin/` to `PATH` and points CondaTainer to the shared root and versioned toolchain via environment variables:

| Variable | Description |
|---|---|
| `PATH` | Prepends `/apps/condatainer/<version>/bin` |
| `CNT_ROOT` | Points to the persistent shared install directory. |
| `CNT_LIBEXEC` | Points to the toolchain (`/apps/condatainer/<version>/libexec`). |
| `CNT_CONFIG_HOST_APPTAINER` | *(Optional)* Full path to the host `apptainer` for `.def` builds. |

### Modulefile Templates

Choose the syntax matching your cluster's module system:

**Lmod (Lua)**

```lua title="<modulepath>/condatainer/<version>.lua"
-- CondaTainer modulefile
local version = "<version>"
local base = "/apps/condatainer/" .. version

prepend_path("PATH", pathJoin(base, "bin"))
setenv("CNT_ROOT", "/shared/condatainer")
setenv("CNT_LIBEXEC", pathJoin(base, "libexec"))

-- Optional: Use the site's native Apptainer for .def builds
-- setenv("CNT_CONFIG_HOST_APPTAINER", "/apps/apptainer/1.3.0/bin/apptainer")
-- depends_on("apptainer/1.3.0")
```

**Environment Modules (Tcl)**

```tcl title="<modulepath>/condatainer/<version>"
#%Module1.0
## CondaTainer modulefile
set version "<version>"
set base "/apps/condatainer/$version"

prepend-path PATH "$base/bin"
setenv CNT_ROOT "/shared/condatainer"
setenv CNT_LIBEXEC "$base/libexec"

## Optional: Use the site's native Apptainer for .def builds
# setenv CNT_CONFIG_HOST_APPTAINER "/apps/apptainer/1.3.0/bin/apptainer"
# module load apptainer/1.3.0
```

### Delegating `.def` Builds to Host Apptainer

CondaTainer uses two Apptainer binaries for different purposes:
1. **Toolchain Apptainer (`libexec/`):** The unprivileged internal binary used to run containers and mount overlay filesystems.
2. **Host Apptainer (`host_apptainer`):** The system or module binary used when compiling OS overlays from definition files (`.def`), which may require setuid or privileged capabilities.

If the site provides an Apptainer module with specific optimizations, site bind paths, or setuid helpers, set `CNT_CONFIG_HOST_APPTAINER` to point to that binary, and declare a module dependency (`depends_on` in Lmod or `module load` in Tcl) to ensure its environment is active.

## 4. Initialize Site Configuration

As the maintaining account, load the module and initialize the site configuration in the shared directory:

```bash
module load condatainer
condatainer config init -l r
```

The `-l r` (or `-l cnt-root`) flag ensures changes write to `/shared/condatainer/config.yaml`.

You can set additional cluster-wide defaults as needed:

```bash
condatainer config set default_distro ubuntu24 -l r
```

## 5. Provision the Toolchain (`libexec`)

CondaTainer's runtime dependencies (Apptainer, FUSE helpers, and `micromamba`) live in `libexec/`.

Download the prebuilt toolchain package or release corresponding to your architecture from the [condatainer/libexec](https://github.com/condatainer/libexec) repository, and unpack it into `/apps/condatainer/<version>/libexec`:

```bash
mkdir -p /apps/condatainer/<version>/libexec
# Unpack the downloaded libexec bundle for your architecture
tar -xzf libexec-<arch>.tar.gz -C /apps/condatainer/<version>/libexec
```
