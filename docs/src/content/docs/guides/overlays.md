---
title: Module Overlay
description: Find, create, run and manage module overlays of software and data.
---

Overlays are the building blocks of CondaTainer. The `create` command builds **module overlays**: read-only `.sqf` images of an operating system, a tool, or a dataset. `exec` and `run` mount them, and by combining them you can assemble a complete, reproducible environment for your need.

## Find

### Recipes

Use the `avail` command to explore available build recipes:

```bash
condatainer avail                # every recipe
condatainer avail salmon         # names containing "salmon"
condatainer avail grch38 salmon  # names containing both
condatainer avail 'grch38/*'     # wildcards and regex also work
```

Each result displays its description and the recipe source. You can use the `--source <name>` flag to restrict your search to a specific source.

**Template Recipes:** Some recipes are **templates**: a single recipe designed to build many variants. The `avail` command shows templates in a collapsed format:

```text
grch38/salmon-gencode-template  [728 variants, cnt]
  Salmon {salmon_version} transcriptome index for GENCODE {gencode_version}
  GRCh38 (k-mer {kmer_length})
  → grch38/salmon/{salmon_version}/gencode{gencode_version}-k{kmer_length}
  - salmon_version:   1.9.0-2.8.0  (26 values)
  - gencode_version:  23-50  (28 values)
  - kmer_length:      31, *
```

To create a specific variant, simply fill your desired values into the `→` pattern and use that full name. You never need to use the template's base name:

```bash
condatainer create grch38/salmon/2.7.0/gencode47-k31
```

A `*` among the values means the template accepts any custom value. For example, passing `kmer_length` as 25 gives `...-k25`. You can use `avail -e` to list every possible variant by its full rendered name.

### Conda packages

You can also search for standard Conda packages:

```bash
condatainer search samtools              # List versions of samtools
condatainer search -f samtool            # Allow approximate name matching
condatainer search samtools -c bioconda  # Restrict search channel
```

Alternatively, you can search directly on [anaconda.org](https://anaconda.org). Any package found there can be created as an overlay, provided it belongs to one of your configured channels (which default to `conda-forge` and `bioconda`).

## Create

### From a name

```bash
condatainer create samtools/1.22
condatainer create grch38/salmon/2.7.0/gencode47-k31
```

When you run `create`, CondaTainer first searches for a recipe; if none is found, it falls back to looking for a Conda package.

- **Version Matching:** If you omit the version or provide a partial one, CondaTainer automatically selects the newest matching version.
- **Templates Fill:** Template targets can be directly created by providing its full rendered name.
- **Dependencies:** Any dependencies declared by a recipe are built automatically. For example, building a Salmon index overlay will create the transcript overlay.

**Build Options:**

| Flag | Effect |
|---|---|
| `--update` | Rebuild an overlay even if already exists. |
| `--no-prebuilt` | Force fresh build instead of downloading a prebuilt. |
| `--no-submit` | Run the build process locally instead of use scheduler. |

:::note
A build can be submitted to the scheduler as a job. `create` then exits with code 3.
:::

### From Conda packages

To bundle several Conda packages into a single overlay, use the `-n` (name) or `-p` (prefix) flags:

```bash
# one overlay named nvim
condatainer create -n nvim nvim nodejs

# one overlay, rnaseq.sqf in this directory
condatainer create -p rnaseq salmon star samtools
```

`-f` allows to build a overlay from a Conda file. If location not specified, next to the file.

```bash
condatainer create -f environment.yml  # Conda environment file
condatainer create -f spec.txt         # Conda explicit spec
```

### From a build recipe

`-f` also allows to build from a custom buile recipe. If location not specified, next to the file, overritten by `-p` or `-n`:

```bash
condatainer create -f build.sh
```

### From a Singularity definition or image

Similarly, you can build an overlay from a Singularity definition file (`-f`) or an image URI (`--from`):

```bash
condatainer create -f image.def  # Singularity definition
condatainer create --from docker://ubuntu:24.04 -p ubuntu
```

`--from` accepts any image URI (such as `docker://`) and requires you to provide a destination name (`-n`) or prefix (`-p`).

### Where it goes

By default, an overlay is saved to the writable shared installation directory. Otherwise, it is saved to your personal directory. You can run `condatainer list` to see where each overlay resides.

You can customize the destination using these flags:

- **`-l <layer>`**: Explicitly choose the destination [layer](layers.md). Use `u` (or `user`) for your personal directory, and `e` (`extra-root`) or `r` (`cnt-root`) for a shared directory.
- **`-p <prefix>`**: Write the overlay as a specific file (e.g., `rnaseq.sqf`). To use it, refer to the overlay by its path (e.g., `exec -o ./rnaseq.sqf`).

## Run

`exec` starts a container with the overlays given by `-o`. With a command, it runs it; without one, it opens a Bash shell:

```bash
condatainer exec \
  -o salmon/2.7.0 \
  -o grch38/salmon/2.7.0/gencode47-k31
```

Inside the container:

- **OS Default:** If no OS overlay specified, the `<default_distro>/base` will be used. 
- **Software PATH Edit** `<prefix>/bin` will be added to container's `PATH`.
- **Data ENVs** Data overlays expose their files by setting specific environment variables (like `$SALMON_INDEX_DIR`). The interactive shell will list these variables. They can be found via `condatainer info <name>`.

:::caution
Never write a data overlay's variables directly on an `exec` or `e` command line. The host shell expands them before the container starts, and there they are empty:

```bash
# WRONG: -i gets an empty path
condatainer exec -o salmon/2.7.0 \
  -o grch38/salmon/2.7.0/gencode47-k31 \
    salmon quant -i "$SALMON_INDEX_DIR" ...
```

Instead, write [a script](#in-a-script), or wrap it in `bash -c '...'` with single quotes (no expansion):

```bash
condatainer exec -o salmon/2.7.0 \
  -o grch38/salmon/2.7.0/gencode47-k31 \
  bash -c '
    salmon quant -i "$SALMON_INDEX_DIR" ...
  '
```
:::

The `e` command is a convenient shorthand for `exec`:

- Module overlay names as direct arguments.
- Auto load writable [environment overlay](environments.md) `env.img`.

```bash
condatainer e salmon/2.7.0 \
  grch38/salmon/2.7.0/gencode47-k31 \
  -- salmon --version
```

**Useful flags for both commands:**

| Flag | Effect |
|---|---|
| `--bind HOST:CONTAINER` | Make a host directory available inside the container <br/> (add `:ro` for read-only access). |
| `--env KEY=VALUE` | Set a custom environment variable inside the container. |

### GPUs

When the node has an NVIDIA or AMD GPU, `exec`, `e`, `run` and [helpers](helper.mdx) make it available inside the container automatically. To request GPUs from the scheduler, see [Important Resource Flags](scheduler.mdx#important-resource-flags).

- To turn this off, run `condatainer config set autoload_gpu false` (see [Configuration](config.md)).
- With it off, `run` and helpers still use the GPU when they request one with `-g` or a `#GPU:` line.
- With it off, `exec --load-gpu` makes the GPU available for that one command.

### In a script

To run your own scripts inside a container, define the required overlays using `#DEP:` lines. You can then execute the script using `run`:

```bash title="quant.sh"
#!/bin/bash
#DEP: salmon/2.7.0
#DEP: grch38/salmon/2.7.0/gencode47-k31

salmon quant -i "$SALMON_INDEX_DIR" -l A \
  -1 "$1".fq.gz -2 "$1".fq.gz -o quant/"$1"
```

```bash
condatainer run quant.sh sample1
```

- A `#DEP:` line accepts the same syntax as the `-o`.
- Without scheduler directives, `run` runs the script on the current machine.

## Manage

```bash
condatainer list                  # List everything installed, grouped by directory
condatainer list salmon           # List installed containing "salmon"
condatainer info salmon/2.7.0     # Show details and environment variables
condatainer remove samtools/1.22  # Delete an overlay (prompts for confirmation)
```

`remove` refuses an overlay that is write-protected or in use.
