# internal/credential

Stores secrets per config layer: registry logins and recipe-source tokens.

## One store

- Registries and recipe sources share one file per layer, `credentials.json`, beside `config.yaml`.
- `config.yaml` cannot hold a secret. A shared layer's config must stay readable by everyone using the install.
- The package does not import `config`. `config` names the files, so both `registry` and the catalog can use it without a cycle.
- Registry logins sit under `auths`, Docker's shape, and recipe sources under `sources`, in the same shape. A key is a host, or a URL path under it with no scheme.
- Separate sections, not one map: a token is only ever looked up for, and sent to, its own kind of server.
- A source's registry token is stored in the source's entry, with the registry it is for, not under `auths`.
- So the file itself says what belongs to a source: removing the source removes its registry token, and a `registry login` is never touched by a source command.

## Lookup

- The most specific key wins, then the nearest layer.
- So a personal entry for one repository beats a group's entry for the host, and a person's entry for the same key beats the group's.
- An unreadable file holds nothing. A broken shared file must not stop a person's own credential from working.

## Files

- Layered like config: user, extra-root, app-root.
- A file follows its directory: `0600` in a personal one, group read-write in a group-writable one. Otherwise a shared credential is readable only by whoever saved it.
- The user layer's directory is created private. A shared layer's directory is never created: it belongs to whoever set it up.
- Who can read or replace a file comes from mode bits only. ACLs are not consulted.
