---
title: Identity and Equivalence
description: Understand how CondaTainer tracks overlay provenance and determines when builds can be safely reused.
---

A **recipe** defines how to build an overlay: specifying the Conda packages to install or the shell script. However, running the same recipe at different times does not guarantee an identical result: Conda might resolve a newer build of a dependency, or an upstream server update a file.

To balance exact and practical reproducibility, every overlay is assigned two fingerprints: **Identity** and **Equivalence**.

## The Core Concept

| Key | Question it answers | What it represents |
|---|---|---|
| **Identity** | *"What exact inputs produced this?"* | Strict provenance and exact build history. |
| **Equivalence** | *"Can this stand in for that?"* | Functional compatibility, ignoring minor differences. |

- **Identity** is strict and deterministic. If two overlays share the same identity, they were built from the exact same inputs (identical package files, source hashes, and scripts).
- **Equivalence** focuses on practical reuse. It omits transient details that vary between builds but are unlikely to alter computational results (such as minor build iterations of an underlying dependency). Equivalence is a design judgment about the recipe, enabling CondaTainer to safely pull prebuilt overlays from registries or reuse an already-installed build.

## Inspecting Keys

You can inspect the keys assigned to any installed overlay using `condatainer info`:

```bash
condatainer info samtools/1.24
```

```text
Keys
  Artifact:      samtools/1.24
  Identity:      conda-explicit-v1@sha256:4b53c176...
  Equivalence:   conda-environment-v1@sha256:a0ee3b83...
```

Each key consists of a **scheme** name and a **hash digest** (e.g., `conda-explicit-v1@sha256:...`). The scheme defines the specific rules and inputs used to compute the digest.

## How Keys Are Calculated

Different kinds of overlays derive their keys from different inputs:

| Overlay Type | Identity (Strict) | Equivalence (Functional) |
|---|---|---|
| **Conda Packages** | The exact explicit URL/filename, version, build string, and channel for every package installed. | Package names and version constraints, ignoring specific build strings and hashes. |
| **Build Recipes** | Script content (stripped of comments), checksums of all `#SOURCE:` files, `#ENV:` variables, and template parameters. | Script content, `#ENV:` variables, and template parameters (omitting downloaded source file checksums). |
| **Recipes with Dependencies** | Identity of the build recipe combined with the exact identities of all declared dependencies. | Equivalence of the build recipe combined with the semantic contributions of each dependency. |
| **Singularity Definitions** | Comment-stripped definition, template parameters, and the resolved upstream image digest. | Comment-stripped definition, and template parameters (omitting upstream image digest). |
| **Frozen Environments** | Content digest of the overlay's filesystem. | Identical to the Identity digest. |

### Conda Overlays

When building from Conda packages:
- **Identity** corresponds to a fully resolved Conda explicit specification: the exact artifact URL, version, channel, and build hash of every package and dependency.
- **Equivalence** captures only the requested package names and versions (e.g., `samtools=1.24`, `htslib=1.24`), omitting the underlying build hashes.

*Example:* If you build an overlay today and another next month, Conda may pick a newer sub-dependency build (e.g., a rebuilt `zlib`). The two overlays will have **different Identities**, but **identical Equivalence**.

### Custom Build Recipes

For overlays created from standalone scripts without dependencies (see [Custom Build Recipe](../advanced/custom_recipe.mdx)):
- **Identity** hashes the normalized script (excluding comments), the exact cryptographic checksums of every downloaded source file (`#SOURCE:`), environment variables (`#ENV:`), and template values.
- **Equivalence** hashes the recipe definition and environment settings, but omits the downloaded file checksums.

If an upstream provider re-packages a source archive without changing the contents or build recipe, the overlay's identity updates, but its equivalence remains stable.

### Singularity / Apptainer Definitions

For OS overlays built from an Apptainer or Singularity definition file (`.def`) or synthesized from an image URI (e.g., `docker://ubuntu:24.04`):
- **Identity** (`definition-identity-v1`) hashes the comment-stripped definition content, template placeholders, and the resolved cryptographic digest of the upstream bootstrap image (`From:`).
- **Equivalence** (`definition-equiv-v1`) hashes the comment-stripped definition content, and template placeholders, but omits the resolved upstream image digest.

*Example:* Rebuilding an OS overlay from `docker://ubuntu:24.04` after Canonical pushes an updated Ubuntu base image results in a different **Identity** (because the upstream container image digest changed), but retains the same **Equivalence** (because the definition file itself is identical). Definitions cannot have overlay dependencies.

### Recipes with Dependencies

Data overlays often depend on tools or other data. For example, a Salmon transcriptome index recipe may declare dependencies on `salmon/2.7.0` and a reference transcript set. 

Dependencies affect the keys differently depending on their role:

| Dependency Type | Contributes to Identity | Contributes to Equivalence |
|---|---|---|
| **Data Overlay** | Full Identity | Full Equivalence |
| **Primary Tool** (named in overlay name) | Full Identity | Tool name and version only |
| **Build-time Helper Tool** | Full Identity | *Ignored* |

*Example:* In `grch38/salmon/2.7.0/gencode47-k31`:
- `salmon/2.7.0` is a **primary tool** explicitly tied to the overlay. Changing Salmon to `2.8.0` alters both Identity and Equivalence.
- A helper tool used purely during construction, such as `samtools` used to create a FASTA index (`faidx`) for reference data—is part of build history. Using a different build of `samtools` changes the overlay's **Identity**, but does **not** change its **Equivalence**.

### Frozen Environments (`env.sqf`)

A snapshot does not have an underlying recipe file to derive keys from. Instead, its Identity and Equivalence are identical and are computed directly from the cryptographic content digest of the overlay's filesystem itself.

## Practical Applications

These keys govern how CondaTainer optimizes workflow execution:

- **Prebuilt Downloads:** When running `condatainer create`, CondaTainer checks whether a remote registry hosts an overlay with an *equivalent* key. If found, it downloads the prebuilt `.sqf instead of compiling it locally.
- **Coexisting Builds:** Multiple builds of the same package name can coexist in your store. The default build retains the standard name (e.g., `samtools/1.24`), while alternate builds are stored under their unique identity hashes. View them with `condatainer store list`.
- **Project Portability:** A CondaTainer [project](project.mdx) pins the exact identities of all used overlays into `cnt-lock/`. During `condatainer project restore`, CondaTainer accepts equivalent builds by default for maximum convenience, or can enforce exactness when configured via `condatainer project set-match identity`.
