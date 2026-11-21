# internal/server

The dashboard HTTP server and the reverse proxy to helper services.

## Binding

- The server listens on `127.0.0.1` only. A remote user reaches it through an SSH port forward.
- A helper link carries the port of the browser's address bar, not the server's own. A forward may use a different local port.

## Token

- The listening port is open to every user of the login node, so every request needs the owner's token, for every route and every helper.
- The token is one file per user, `server.token` in the state directory, mode 0600. A server never passes it as an argument.
  - It outlives every server. Login nodes that share a home share the token.
  - A running server starts a read of the file at least every 3 seconds while it serves requests, and when a request does not match. It does not watch the file, because another node's write on NFS raises no event.
  - A file that is missing, short, has other characters than letters, digits and `. _ ~ -`, or is readable by group or others is not used. The server keeps the token it has. A starting server stops.
- The token is replaced after `server.token_days` days, 30 by default. The age is the modification time of the file, so a hand edit and `--rotate-token` both reset it.
  - A running server and a starting server both replace an old token. Every browser then signs in again.
  - A node reads the file again before it writes. If another node already replaced the token, it takes that one. Two nodes that replace together each write a token, and the last wins.
  - `server.token_days` is the age of the token, not of a browser's cookie.
  - The age is read from the open file, not from a `stat`, because NFS can serve a stat from its attribute cache.
  - The read runs in the background, outside the lock, one at a time. A request is judged by the token in memory, so a stalled NFS read never holds up a request.
  - A request that does not match waits up to 1 second for the read it started, so a token just written works at once. After that it gets 401.
- A sign-in link carries `?cnt_token=`. The server sets a cookie for that host and redirects to the URL without it.
  - The parameter is not `token`, because a helper app puts its own `token` in its links.
  - The cookie is `cnt_token_{port}` with the port the browser used, so two forwarded servers on one host keep apart.
  - `SameSite=Lax`, not `Strict`. A dashboard link to a helper is a cross-site navigation, and `Strict` would drop the cookie on it.
  - The cookie lives 400 days on the dashboard and on a name origin. On an id origin it ends with the run, like a helper's own cookies.
- The proxy removes the server's cookies from a request before the helper sees it.
- `server.require_token` off lets every request through and creates no file.

## Helper links

- Each helper is served on its own origin, `{id}.localhost:{port}`. The proxy forwards the whole path as it is.
  - A helper app is built for the root of its origin, so no path needs a rewrite.
  - Path mode (`/h/{id}/`) is deferred. Each helper needs its own base-path setting, and the apps differ in how they take it.
- `{name}.localhost` goes to the earliest submitted running helper of that name.
  - Its origin never changes, so the browser keeps the app's own settings, layout and trust. An id origin is new on every run.
  - A name link never edits a response.
- A request is routed by its `Host` suffix `.localhost`. Any other host gets the dashboard.
- The proxy rewrites `Host` and `Origin` to the backend address, so the app's own host and origin checks pass.

## Cookies of an id link

- A cookie set through an id link gets at most the time left in the run's walltime, and at least 1 day.
  - The id link is never opened again after the run, so its cookies would otherwise stay in the browser until their own age ends.
  - The floor keeps a cookie alive when little walltime is left.
- A run with no known walltime gets 7 days.
- Only `Max-Age`, or `Expires` when there is no `Max-Age`, is edited. A session cookie is not touched.
- `localStorage`, IndexedDB and service workers have no age, so the cap does not reach them. The user clears site data for `localhost`.
- The server cannot read or delete another origin's cookies. The cap is the only control it has.

## Helper list

- The list reply holds every active run and one page of finished runs. A page is 50 runs.
  - Active runs are few and are always in the first reply. A later page holds finished runs only.
  - A long history sent whole makes the dashboard slow to load and to refresh.
- Finished runs are ordered by end time, then id, newest first. A page continues from the last run it held, not from an offset.
  - An offset would repeat or skip runs when a run ends or is removed between two requests.
  - The cursor still works when the run it names was removed.
- `X-Total-Count` is the number of finished runs that match. The page uses it to tell whether more remain.
- How many runs the disk keeps is `helper.history_limit`. Paging only changes how many are sent at once.
