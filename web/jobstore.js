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
//   - A 30 s timer, the tab becoming visible and loadJobs() also refetch, in
//     case an event was lost on the way.
let _jobsInflight = null;
let _jobsFetchAgain = false;
let _historyWanted = false;  // a caller asked for history during the fetch
let _historyStale = true;    // allHistory is older than the last event

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
        const [active, hist] = await Promise.all([
          fetch('api/helpers?status=active').then(r => r.json()),
          history ? fetch('api/helpers').then(r => r.json()) : null,
        ]);
        allJobs = active || [];
        if (history) { allHistory = hist || []; _historyStale = false; }
        else _historyStale = true;
        document.dispatchEvent(new CustomEvent('jobs:changed'));
      } catch { /* keep the lists on screen; the next event or timer retries */ }
    } while (_jobsFetchAgain);
  })().finally(() => { _jobsInflight = null; });
  return _jobsInflight;
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
