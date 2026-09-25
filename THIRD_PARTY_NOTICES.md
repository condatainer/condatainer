# Third-Party Notices

CondaTainer itself is under the BSD 3-Clause License (see [LICENSE](LICENSE)).

## External tools

These are installed on the host or provisioned by `condatainer update --libexec`. They run as separate programs and are not part of the CondaTainer binary.

| Tool | Used for | License |
| --- | --- | --- |
| [Apptainer](https://github.com/apptainer/apptainer) | running containers, mounting overlays | BSD-3-Clause |
| [Micromamba](https://github.com/mamba-org/micromamba-releases) | solving and installing Conda environments | BSD-3-Clause, MIT, OpenSSL |
| [squashfs-tools](https://github.com/plougher/squashfs-tools) | packing and reading read-only images | GPL-2.0-or-later |
| [squashfuse](https://github.com/vasi/squashfuse) | mounting read-only images | BSD-2-Clause |
| [fuse-overlayfs](https://github.com/containers/fuse-overlayfs) | merging an environment with its snapshot when freezing | GPL-3.0-or-later |
| [libfuse](https://github.com/libfuse/libfuse) | FUSE mounts | LGPL-2.1-only (library), GPL-2.0-only (utilities) |
| [e2fsprogs](https://github.com/tytso/e2fsprogs) | creating, checking, resizing and mounting writable images | GPL-2.0-only (programs), LGPL-2.0 (libraries), BSD-3-Clause (libuuid) |
| [conda-forge](https://conda-forge.org) and [Bioconda](https://bioconda.github.io) packages | the tools above, and user environments | per package |

## Go libraries compiled into the binary

| Module | License |
| --- | --- |
| [github.com/chzyer/readline](https://github.com/chzyer/readline) | MIT |
| [github.com/fatih/color](https://github.com/fatih/color) | MIT |
| [github.com/fsnotify/fsnotify](https://github.com/fsnotify/fsnotify) | BSD-3-Clause |
| [github.com/go-viper/mapstructure](https://github.com/go-viper/mapstructure) | MIT |
| [github.com/mattn/go-colorable](https://github.com/mattn/go-colorable) | MIT |
| [github.com/mattn/go-isatty](https://github.com/mattn/go-isatty) | MIT |
| [github.com/opencontainers/go-digest](https://github.com/opencontainers/go-digest) | Apache-2.0 |
| [github.com/opencontainers/image-spec](https://github.com/opencontainers/image-spec) | Apache-2.0 |
| [github.com/pelletier/go-toml](https://github.com/pelletier/go-toml) | MIT |
| [github.com/sagikazarmark/locafero](https://github.com/sagikazarmark/locafero) | MIT |
| [github.com/spf13/afero](https://github.com/spf13/afero) | Apache-2.0 |
| [github.com/spf13/cast](https://github.com/spf13/cast) | MIT |
| [github.com/spf13/cobra](https://github.com/spf13/cobra) | Apache-2.0 |
| [github.com/spf13/pflag](https://github.com/spf13/pflag) | BSD-3-Clause |
| [github.com/spf13/viper](https://github.com/spf13/viper) | MIT |
| [github.com/subosito/gotenv](https://github.com/subosito/gotenv) | MIT |
| [go.yaml.in/yaml](https://github.com/yaml/go-yaml) | Apache-2.0 |
| [golang.org/x/crypto](https://github.com/condatainer/x-crypto) (fork with host-based auth) | BSD-3-Clause |
| [golang.org/x/mod](https://pkg.go.dev/golang.org/x/mod) | BSD-3-Clause |
| [golang.org/x/sync](https://pkg.go.dev/golang.org/x/sync) | BSD-3-Clause |
| [golang.org/x/sys](https://pkg.go.dev/golang.org/x/sys) | BSD-3-Clause |
| [golang.org/x/term](https://pkg.go.dev/golang.org/x/term) | BSD-3-Clause |
| [golang.org/x/text](https://pkg.go.dev/golang.org/x/text) | BSD-3-Clause |
| [oras.land/oras-go](https://github.com/oras-project/oras-go) | Apache-2.0 |
