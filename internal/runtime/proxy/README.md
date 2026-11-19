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
- The Go client sends a keepalive every 60 s. A keepalive that fails or gets no reply within 30 s closes the client, so the dashboard reopens the tunnel.
  - A silent drop would otherwise wait for TCP to give up, which takes many minutes.

## Exec transport

- Some clusters only allow connections that belong to a job. SSH to a compute node fails, or its ports are filtered.
- The dashboard then uses the scheduler's exec-into-job command to run a relay on the node.
  - The command's stdin and stdout become one bidirectional stream to the helper.
  - Only SLURM has such a command.
- The relay is this binary's hidden `_proxy_relay`, not an interpreter. A cluster cannot be assumed to provide Python.
- Each frame ends with a newline, because `srun` holds input back until a newline or EOF.
- Only loopback targets are allowed.
- There is no flow control. A caller that stops reading without closing can stall the other connections on that relay.

## Exec liveness

- The dashboard pings the relay every 30 s, and the relay answers.
  - Any frame from the node counts as a sign of life.
  - A relay silent for 90 s is killed, which closes `done` so the dashboard reopens it.
  - The ping is also traffic on an otherwise idle connection.
- A dial the node does not answer within 15 s kills the relay and fails with an error of its own.
  - The node's own dial gives up after 10 s, so 15 s never cuts a slow service short.
  - The error is not a context error. The reverse proxy takes a cancelled context for a client that left, and does not evict on it.
- A relay that is stuck behind a caller that stopped reading answers no ping. It is replaced, which also ends the stall.

### srun

- The stream between `srun` and the node is one TCP connection with no keepalive.
- A path that forgets an idle connection drops it with no reset, and `srun` keeps running.
- The relay command runs in its own process group, and a dead relay is killed as a group.
  - `srun` runs a helper process that can hold the pipe open.
