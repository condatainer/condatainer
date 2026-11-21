/* ── Job store ───────────────────────────── */
// The one place the job lists are fetched. It fills allJobs (active) and
// allHistory, then fires 'jobs:changed' on document; the Jobs list, History
// table, detail panel, sidebar count and Jobs badge each listen for it.
//   - The server pushes an event on api/events when a job, its status or its
//     last message changes, and once on every (re)connect. Each event refetches.
//   - The active jobs are small and refetched on every event. History grows,
//     so it is fetched only while the History section is open, when it is
//     opened, and when the open detail panel needs a job that left the active
//     list. Until then allHistory is stale.
//   - History is read in pages. The first fetch holds every active run and the
//     server's first page of finished runs, and loadMoreHistory() adds the next
//     page after the last finished run it has. A refetch asks for as many
//     finished runs as are loaded, so the loaded pages stay.
//   - A 30 s timer, the tab becoming visible and loadJobs() also refetch, in
//     case an event was lost on the way.
let _jobsInflight = null;
let _jobsFetchAgain = false;
let _historyWanted = false;  // a caller asked for history during the fetch
let _historyStale = true;    // allHistory is older than the last event
let historyTotal = 0;        // finished runs the server holds, from X-Total-Count
let _historyLoaded = 0;      // finished runs fetched so far, in pages
let _historyCursor = null;   // ended time and id of the last finished run fetched
let _moreInflight = null;    // the next page is being fetched
let _allInflight = null;     // every remaining page is being fetched

function _isFinished(h) { return !isActiveHistoryStatus(h.status); }
function _cursorOf(h) { return { ended: h.ended_at || '0001-01-01T00:00:00Z', id: h.id }; }

// historyHasMore reports whether the server holds finished runs that are not loaded.
function historyHasMore() { return _historyLoaded < historyTotal; }

// _applyHistory replaces allHistory with a fetched list. The run in the open
// detail panel stays even when new runs pushed it past the fetched window.
function _applyHistory(list, total) {
  const keep = detailJobId && !list.some(h => h.id === detailJobId)
    ? allHistory.find(h => h.id === detailJobId && _isFinished(h)) : null;
  const finished = list.filter(_isFinished);
  _historyLoaded = finished.length;
  historyTotal = Number.isFinite(total) ? total : finished.length;
  _historyCursor = finished.length ? _cursorOf(finished[finished.length - 1]) : null;
  allHistory = keep ? list.concat([keep]) : list;
}

// forgetHistoryRun drops a run the server just removed.
function forgetHistoryRun(id) {
  const run = allHistory.find(h => h.id === id);
  allHistory = allHistory.filter(h => h.id !== id);
  if (run && _isFinished(run)) {
    historyTotal = Math.max(0, historyTotal - 1);
    _historyLoaded = Math.max(0, _historyLoaded - 1);
  }
}

function _historyVisible() {
  const sec = gid('sec-history');
  return !!sec && sec.classList.contains('active');
}

// refreshJobs refetches the active jobs, and history when it is visible or
// withHistory is true, and resolves once they are current. A call during a
// fetch shares it and makes it run once more, so a change that landed after
// the fetch started is not missed.
function refreshJobs(withHistory) {
  if (withHistory) _historyWanted = true;
  if (_jobsInflight) { _jobsFetchAgain = true; return _jobsInflight; }
  _jobsInflight = (async () => {
    do {
      _jobsFetchAgain = false;
      const history = _historyWanted || _historyVisible();
      _historyWanted = false;
      try {
        const loaded = _historyLoaded;
        const [active, hist] = await Promise.all([
          fetch('api/helpers?status=active').then(r => r.json()),
          history ? fetch('api/helpers' + (loaded ? '?limit=' + loaded : '')).then(async r => {
            const total = r.headers.get('X-Total-Count');
            return { list: (await r.json()) || [], total: total === null ? NaN : Number(total) };
          }) : null,
        ]);
        allJobs = active || [];
        if (hist) { _applyHistory(hist.list, hist.total); _historyStale = false; }
        else _historyStale = true;
        document.dispatchEvent(new CustomEvent('jobs:changed'));
      } catch { /* keep the lists on screen; the next event or timer retries */ }
    } while (_jobsFetchAgain);
  })().finally(() => { _jobsInflight = null; });
  return _jobsInflight;
}

// loadMoreHistory fetches the next page of finished runs and adds it to allHistory.
function loadMoreHistory() {
  if (_moreInflight) return _moreInflight;
  _moreInflight = (async () => {
    if (_jobsInflight) await _jobsInflight;
    if (!historyHasMore() || !_historyCursor) return;
    const c = _historyCursor;
    try {
      const r = await fetch('api/helpers?before=' + encodeURIComponent(c.ended) + '&before_id=' + encodeURIComponent(c.id));
      if (!r.ok) return;
      const page = (await r.json()) || [];
      const total = r.headers.get('X-Total-Count');
      if (total !== null && Number.isFinite(Number(total))) historyTotal = Number(total);
      const have = new Set(allHistory.map(h => h.id));
      allHistory = allHistory.concat(page.filter(h => !have.has(h.id)));
      if (page.length) {
        _historyLoaded += page.length;
        _historyCursor = _cursorOf(page[page.length - 1]);
      } else {
        _historyLoaded = historyTotal; // nothing is left after the cursor
      }
      document.dispatchEvent(new CustomEvent('jobs:changed'));
    } catch { /* the next scroll or refresh retries */ }
  })().finally(() => { _moreInflight = null; });
  return _moreInflight;
}

// loadAllHistory fetches every remaining page, for a search or a link to an old run.
function loadAllHistory() {
  if (_allInflight) return _allInflight;
  _allInflight = (async () => {
    while (historyHasMore()) {
      const before = _historyLoaded;
      await loadMoreHistory();
      if (_historyLoaded === before) break;
    }
  })().finally(() => { _allInflight = null; });
  return _allInflight;
}

function _connectJobEvents() {
  const es = new EventSource('api/events');
  es.onmessage = () => refreshJobs();
  // The browser reconnects by itself unless the server refused the stream.
  es.onerror = () => { if (es.readyState === EventSource.CLOSED) setTimeout(_connectJobEvents, 5000); };
}

function startJobStore() {
  refreshJobs();
  _connectJobEvents();
  setInterval(refreshJobs, 30000);
  document.addEventListener('visibilitychange', () => { if (!document.hidden) refreshJobs(); });
}
