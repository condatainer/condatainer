# internal/runtime/proxy

SSH tunnel and proxy for HPC compute nodes, and the transports the dashboard uses to reach a helper.

## Modes

- Shared: started on a login node, binds `0.0.0.0`, lives until stopped.
- Per-job: started on a compute node inside the job, binds `127.0.0.1`, lives until the job ends.

## Tunnel

- The Go SSH library is tried first, then system `ssh -D` on a Unix socket, then on `127.0.0.1` for old OpenSSH.
- Go SSH comes first to save process count.
  - A login node often limits how many processes a user can run.
  - The Go client is in-process, so a tunnel costs no extra process.
  - One authenticated SSH connection carries every proxied connection.
- The dashboard's reverse proxy uses the same function, with its own socket path per tunnel, since it can hold tunnels to several nodes.

## Exec transport

- Some clusters only allow connections that belong to a job. SSH to a compute node fails, or its ports are filtered.
- The dashboard then uses the scheduler's exec-into-job command to run a relay on the node.
  - The command's stdin and stdout become one bidirectional stream to the helper.
  - Only SLURM has such a command.
- The relay is this binary's hidden `_exec_relay`, not an interpreter. A cluster cannot be assumed to provide Python.
- Each frame ends with a newline, because `srun` holds input back until a newline or EOF.
- Only loopback targets are allowed.
- There is no flow control. A caller that stops reading without closing can stall the other connections on that relay.
