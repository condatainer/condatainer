# internal/server

The dashboard HTTP server and the reverse proxy to helper services.

## Binding

- The server listens on `127.0.0.1` only. A remote user reaches it through an SSH port forward.
- A helper link carries the port of the browser's address bar, not the server's own. A forward may use a different local port.

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
