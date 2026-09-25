# CondaTainer

<div>
<a href="#condatainer"><img src="assets/logo_cnt.svg" height="70" alt="CondaTainer Logo"></a>
</div>

**CondaTainer** is a rootless CLI that packages *tools*, *data*, and *Conda environments* into portable images that run seamlessly on HPC.

- **Modular**: Pack Conda environments and data as individual images that work together.
- **Reproducible**: Push and pull from OCI registries to guarantee exact versions per project.
- **Web-App Ready**: Launch *RStudio*, *code-server*, and *VNC* with one command.
- **NetworkFS Friendly**: Pack thousands of files into single images and stage on fast node-local paths.
- **Scheduler Native**: Integrates out-of-the-box with HPC workload managers like Slurm.

> [!NOTE]
> *Slurm* is the primary, tested scheduler. *PBS*, *LSF*, *HTCondor* are experimental, bug reports are welcome!
>
> For `qsub`, only **PBS Pro** (OpenPBS) is supported. Torque and SGE are not supported.

## Installation

```bash
curl -fsSL https://raw.githubusercontent.com/condatainer/condatainer/main/assets/install.sh | bash
```

You will be prompted to confirm the installation path (defaults to `$SCRATCH/condatainer/` or `$HOME/condatainer/`). The script run configuration and ask for installing missing tools and the shell update.

## Quick Start

Find, install and use a tool. Default recipes come from the [cnt](https://github.com/condatainer/cnt) collection:

```bash
condatainer avail                  # List available recipes
condatainer create salmon/1.10.2   # Install one
condatainer exec -o salmon/1.10.2 salmon --version
```

Declare what a script needs with `#DEP:` and its resources with `#SBATCH`, then install and run it:

```bash
#!/bin/bash
#SBATCH --cpus-per-task=8
#SBATCH --mem=30G
#DEP:salmon/1.10.2
#DEP:grch38/salmon/1.10.2/gencode47-k31

salmon quant -i $SALMON_INDEX_DIR -l A -r reads.fq -p $NCPUS -o quants
```

```bash
condatainer check -a script.sh     # Install missing dependencies
condatainer run script.sh          # Submit it to the scheduler
```

Lock a project, share it, and restore it elsewhere:

```bash
condatainer project lock                                   # Pin every #DEP: to an exact artifact
condatainer project registry set ghcr.io/my-lab/analysis   # Where to publish (once)
condatainer project registry push                          # Publish the pinned artifacts

condatainer project restore                                # On another machine: fetch or rebuild them
```

## Acknowledgements

CondaTainer builds on many open-source tools. Their licenses are listed in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

This project used computational resources provided by

<div>
<a href="https://honghan-lab.com" target="_blank" rel="noopener noreferrer"><img src="assets/logo_hanlab.svg" height="45" alt="Han Lab Logo"></a>
&nbsp;&nbsp;&nbsp;
<a href="https://www.mcmaster.ca" target="_blank" rel="noopener noreferrer"><img src="assets/logo_mcmaster.svg" height="45" alt="McMaster University Logo"></a>
&nbsp;&nbsp;&nbsp;
<a href="https://alliancecan.ca" target="_blank" rel="noopener noreferrer"><img src="assets/logo_alliance.svg" height="40" alt="Digital Research Alliance of Canada Logo"></a>
</div>
