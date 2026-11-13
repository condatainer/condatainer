# internal/runtime/hostrun

The relay that lets a program in a container run a command on the host.

## Why a relay

- The container has no scheduler client, and the host's client cannot run inside it. Its libraries and plugins need the host's glibc.
- The container maps only part of the user's uid and gid, so a command that needs the full identity works only when the host runs it.
- Host-based login needs a setuid `ssh-keysign`. A user container cannot give that to any binary, so the host does the login.

## Where it opens

- A launch opens it when `host_run.launch` names its kind. The default is `helper`.
- `host_run.launch` merges every layer's list, as `scheduler.proxy_per_job` does, so a site adds to what a user sets. `none` in any layer turns every launch off.
  - The default is not part of the merge. It applies only when no layer sets the key.
- It opens on a compute node, a login node and a single server alike.
- A launch inside a container opens none, since its host parent is the container, not the host.
  - A container inside a container has no host-run. Nothing needs the host from there, so the inherited bind and socket variable are removed.
- Builds never open it.
- The container side reads no config. The host side of the launch reads the keys once.
- A relay that cannot open is a warning. The launch goes on without it.

## Where a command runs

- `host_run.on` and `--on` are one name. A call beats the key.
- `localhost` is the node the launch runs on.
- `login` is `scheduler.LoginNode()`, the node recorded in `CNT_LOGIN_NODE`.
- A node name must be a plain host name, because it is passed to `ssh`.
- A command on another node runs through the host's own `ssh` in batch mode. No connection is kept between calls.
- `ssh` trusts a host key the first time it sees it (`accept-new`) and refuses a changed one.
  - The login node is recorded by its full host name, which the user's `known_hosts` may not hold. Without this the call fails on the key.
  - OpenSSH before 7.6 fails on the option, so it is left out there. The version is read once per `ssh`.
- Each word is quoted for the remote shell, since `ssh` joins its arguments into one string.
- There is no fallback to another node. A retry after a dropped connection could submit twice.
- A command run on the node `login` names gets `CNT_LOGIN_NODE` set, so a job it submits carries the record.
- A leading `condatainer` is the host's own executable, so the version that started the job runs.

## Safety

- The socket is in a `0700` directory under the node-local temp directory. The server checks the peer uid.
- There is no command list by default. The container is the user's own account, and a tool inside needs `ls` as much as `sbatch`.
- Any code in the container can run a command as the user on the host. `host_run.launch` limits which launches open it.

## Limits

- A Unix socket path is at most 107 bytes. The host path is the one that counts.
- Standard input is sent only with `--stdin`, up to 1 MiB.
  - A caller whose stdin is an open pipe that never closes cannot hang a call.
- A command is run directly, never through a shell.
- The working directory is the container's. A directory missing on the host is an error.
- Closing the client stops the command. Over `ssh` that stops the local `ssh`, and the remote command may keep running.

## Protocol

- One connection per request. The request is one JSON line, then the standard input.
- The reply is length-prefixed frames of stdout and stderr, then the exit code.
  - The streams stay apart, so `id=$(host-run sbatch --parsable job.sh)` captures the job id alone.
- The frames are not the dashboard relay's. Those are text lines that `srun` forwards, which this socket does not need.
