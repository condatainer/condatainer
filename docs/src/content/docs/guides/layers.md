---
title: Layers and Store
description: Understand where CondaTainer stores overlays across user, group, and site levels, and how the store manages multiple builds of the same name.
---

CondaTainer stores overlays across multiple hierarchical directories called **layers**. This structure allows personal, lab-wide, and cluster-wide installations to share overlays without duplicating files. 

When a package has multiple distinct builds (for example, two different versions or builds of `samtools/1.22`), CondaTainer preserves them side-by-side using a dedicated sub-directory called the **store**.

## Layers

A layer is a filesystem directory where overlays reside. CondaTainer recognizes four standard layers:

| Layer | Flag (`-l`) | Location | Description |
|---|---|---|---|
| **scratch** | — | `$SCRATCH/condatainer` | Personal large storage <br/> (when `$SCRATCH` set). |
| **user** | `u` or `user` | `~/.local/share/condatainer` <br/> (or `$XDG_DATA_HOME/condatainer`) | Personal user directory. |
| **extra-root** | `e` or `extra-root` | Specified by `$CNT_EXTRA_ROOT` | Shared group. |
| **cnt-root** | `r` or `cnt-root` | Primary install dir (or `$CNT_ROOT`) | Central installation directory. |

To inspect your currently active layers and see where new builds will be saved:

```bash
condatainer config paths
```

### Layer Resolution Order

CondaTainer resolves read and write operations using two complementary rules:

1. **Commands search nearest first (Lookup):**
   When looking for an overlay to execute (`condatainer exec`), CondaTainer checks layers in order:
   `scratch` → `user` → `extra-root` → `cnt-root`
   The nearest copy found is used. This allows a personal overlay to shadow a shared one.

2. **Builds target furthest first (Sharing):**
   When building a new overlay with `condatainer create`, CondaTainer writes to the first *writable* layer in reverse order:
   `extra-root` → `cnt-root` → `scratch` → `user`
   This allows overlays to be shared between users in a group or site, avoiding redundant builds.

### Targeting Specific Layers

You can explicitly override the target layer with `-l`:

```bash
condatainer create -l user samtools/1.22  # Build into your personal directory
condatainer list -l extra-root            # List overlays in the group directory
condatainer remove -l user samtools/1.22  # Remove only your personal copy
```

[Configuration](config.md) files also follow layer hierarchy: `condatainer config init -l extra-root` initializes settings for your shared group layer.

### Common Deployment Scenarios

| Setup | Active Layers | Build Behavior |
|---|---|---|
| **Personal** | `cnt-root` is your own install. | Builds directly into the installation directory. |
| **Group** | One shared directory: group install (`cnt-root`) or `$CNT_EXTRA_ROOT` over a site install. | Builds into the shared group directory for all members. |
| **Site** | Central read-only `cnt-root` by HPC admin. Users have `user`, groups have `extra-root`. | Builds into group's `extra-root` or the user's `user` layer. |

- **Personal:** Installed into your personal directory. Builds go directly into your installation directory.
- **Group:** A shared group directory (such as a lab directory). Software is built once by any team member and immediately accessible by everyone. (See [Group install](../deployment/group_install.md)).
- **Site:** A cluster-wide read-only installation provided by administrators. Users or groups layer their own directories on top. (See [Site install](../deployment/site_install.md)).

## The Store

### Why the Store Exists

Within any single layer directory, an overlay typically occupies a **plain name slot** (e.g., `samtools/1.22`).

However, building the same recipe at a later date may resolve newer sub-dependencies or updated source files. Even though the package name is unchanged, the new build has a different [Identity](identity.md).

To prevent newer builds from overwriting older builds, CondaTainer uses the **store**:
- The plain name slot (`samtools/1.22`) points to one primary build.
- Any additional builds of that same package name are placed into a `store/` sub-directory and tagged with their identity: `name@identity`.
- Standard commands (`condatainer exec -o samtools/1.22`) continue to access the primary plain name.
- Reproducible [projects](project.mdx) pin the exact identity hash, ensuring they always mount their intended build.

### Managing Store Entries

List all entries across all layers, or inspect specific builds:

```bash
condatainer store list                        # List all store entries by layer
condatainer store list star/2.7.11b           # List all builds of a specific tool
condatainer store list star/2.7.11b --detail  # Include layout, file size, and paths
```

Identity hashes can be shortened to unique prefixes, e.g., `star/2.7.11b@9f2c1ab`).

#### Store Commands

You can manipulate individual store builds with dedicated subcommands:

```bash
# Promote a store build to become the default plain name (swap)
condatainer store use star/2.7.11b@9f2c1ab

# Print the file path of a specific build
condatainer store path star/2.7.11b@9f2c1ab

# Import an existing standalone overlay file into the store
condatainer store add ./star-2.7.11b.sqf

# Delete a specific build from the store
condatainer store remove star/2.7.11b@9f2c1ab

# Verify that store overlays match their recorded identities
condatainer store validate star/2.7.11b@9f2c1ab
```

### Cleaning Up Unused Builds (Garbage Collection)

Over time, obsolete builds in the store can consume disk space. Clean them up using `condatainer store gc`:

```bash
# Report overlays that are eligible for deletion
condatainer store gc

# Delete them from your personal layer, after asking
condatainer store gc --layer user
```

**Garbage Collection Rules:**

- **Grace Period:** An overlay has not been accessed or modified within the grace period (`store_gc_grace`, 30 days by default).
- **Safety Scope:** Deleting requires `--layer`, ensuring garbage collection only affects the specific layer.
- **Write-Protection:** Read-only overlays cannot be deleted by `gc`.
- **Projects:** A project lock does not prevent an unused store overlay from being cleaned up. Run `project restore` for overlay restore.
