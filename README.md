# CondaTainer

<div>
<a href="#condatainer"><img src="assets/logo_cnt.svg" height="70" alt="CondaTainer Logo"></a>
</div>

**CondaTainer** is a rootless CLI that packages *tools*, *data*, and *Conda environments* into portable images that run seamlessly on HPC.

- **Modular**: Conda environments and data as separate images that work together.
- **Reproducible**: Pin exact versions per project and share them through OCI registries.
- **Web-App Ready**: Run *RStudio*, *code-server* and *VNC* on compute nodes from a dashboard.
- **NetworkFS Friendly**: Thousands of files become one image, staged on node-local disk.
- **Scheduler Native**: Works with HPC schedulers such as Slurm.

> [!NOTE]
> *Slurm* is the primary, tested scheduler. *PBS* and *LSF* are experimental, bug reports are welcome!
>
> For `qsub`, only **PBS Pro** (OpenPBS) is supported. Torque and SGE are not supported.

## Installation

```bash
curl -fsSL https://install.condatainer.org | bash
```

The script asks where to install (default `$SCRATCH/condatainer/`, else `$HOME/condatainer/`), whether to set up your shell, and whether to download the toolchain.

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

salmon quant -i $SALMON_INDEX_DIR -l A -r reads.fq -p $NCPU -o quants
```

```bash
condatainer check -a script.sh     # Install missing dependencies
condatainer run script.sh          # Submit it to the scheduler
```

Run a web app on a compute node from the dashboard:

```bash
# Start the dashboard on the login node, then add and start a helper in it
condatainer server start
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
