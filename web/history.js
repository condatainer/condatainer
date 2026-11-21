/* ── History section ─────────────────────── */
function loadHistory() { return refreshJobs(true); }

document.addEventListener('jobs:changed', () => {
  if (_historyVisible() && !document.querySelector('#hist-tbody [data-confirm="1"]')) renderHistory(gid('hist-search').value);
});

// renderHistory renders the history table, keeping only entries that match
// every whitespace-separated term of filter (across name/label/status/node/cwd).
let _histHTML = null; // the markup on screen, so a table that did not change is left alone
let _histObserver = null;

// _observeHistoryMore loads the next page when the last row, the "Loading…" row, scrolls into view.
function _observeHistoryMore() {
  const sentinel = gid('hist-more');
  if (!sentinel) return;
  if (typeof IntersectionObserver === 'undefined') { loadAllHistory(); return; }
  if (!_histObserver) {
    _histObserver = new IntersectionObserver(entries => {
      if (entries.some(e => e.isIntersecting)) loadMoreHistory();
    });
  }
  _histObserver.disconnect();
  _histObserver.observe(sentinel);
}

function renderHistory(filter) {
  const terms = searchTerms(filter);
  const q = terms.length;
  let rows = q
    ? allHistory.filter(h =>
        matchesAllTerms([h.name, h.label, h.status, h.node, h.cwd].join(' '), terms))
    : allHistory.slice();
  rows.sort((a, b) => {
    const aActive = isActiveHistoryStatus(a.status) ? 1 : 0;
    const bActive = isActiveHistoryStatus(b.status) ? 1 : 0;
    if (aActive !== bActive) return bActive - aActive;
    const at = a.ended_at ? new Date(a.ended_at).getTime() : 0;
    const bt = b.ended_at ? new Date(b.ended_at).getTime() : 0;
    return bt - at;
  });
  const tbody = gid('hist-tbody');
  const more = historyHasMore();
  if (q && more) loadAllHistory(); // a search looks at every page
  const moreRow = more ? '<tr id="hist-more"><td colspan="6" class="td-empty">Loading…</td></tr>' : '';
  let html;
  if (!rows.length && !more) {
    html = '<tr><td colspan="6" class="td-empty">' +
      (q ? 'No matches.' : 'No history yet.') + '</td></tr>';
  } else html = rows.map(h => {
    const id  = escHtml(h.id);
    return (
      '<tr data-id="' + id + '" tabindex="0" onclick="openDetail(' + jsArg(h.id) + ')">' +
        '<td class="td-name">' + escHtml(h.label || h.name) + '</td>' +
        '<td class="td-fit"><span class="badge ' + badgeClass(h.status) + '">' + escHtml(h.status) + '</span></td>' +
        '<td class="mono td-muted td-node" title="' + escHtml(h.node || '') + '">' + escHtml(shortNode(h.node) || '—') + '</td>' +
        '<td class="mono path-cell td-muted">' + pathTailHtml(h.cwd) + '</td>' +
        '<td class="td-muted td-fit">' + (h.ended_at ? fmtRelTime(h.ended_at) : '—') + '</td>' +
        '<td class="td-nowrap td-fit">' +
          (isActiveHistoryStatus(h.status)
            ? '<button class="btn btn-sm btn-flat btn-icon" disabled title="Job is still active">' + iconSvg('replay') + '</button>'
            : '<button class="btn btn-sm btn-flat btn-icon" onclick="rerunHistoryJob(event,' + jsArg(h.id) + ')" title="Rerun">' + iconSvg('replay') + '</button>') +
          ' ' + (isActiveHistoryStatus(h.status)
            ? '<button class="btn btn-sm btn-flat btn-icon btn-icon-danger" disabled title="Stop the job before removing it">' + iconSvg('delete') + '</button>'
            : '<button class="btn btn-sm btn-flat btn-icon btn-icon-danger" onclick="removeHistoryEntry(event,' + jsArg(h.id) + ',this)" title="Remove entry">' + iconSvg('delete') + '</button>') +
        '</td>' +
      '</tr>'
    );
  }).join('') + moreRow;
  if (html !== _histHTML) {
    tbody.innerHTML = html;
    _histHTML = html;
    _observeHistoryMore();
  }
  markSelected();
}

function filterHistory(q) { renderHistory(q); }

function isActiveHistoryStatus(status) {
  return status === 'pending' || status === 'starting' || status === 'running';
}

// clearFinishedHistory removes every finished (non-active) history entry
// after confirmation (DELETE /api/helpers/finished).
async function clearFinishedHistory() {
  const finished = allHistory.filter(h => !isActiveHistoryStatus(h.status));
  const total = Math.max(historyTotal, finished.length); // the server removes every finished run, loaded or not
  if (!total) return;
  const ok = await askConfirm('Remove ' + total + ' finished ' +
    (total === 1 ? 'entry' : 'entries') + ' from history? ' +
    'This cannot be undone.',
    { title: 'Clear finished history', okLabel: 'Clear' });
  if (!ok) return;
  try {
    const r = await fetch('api/helpers/finished', { method: 'DELETE' });
    if (!r.ok) { alert('Could not clear history: ' + await r.text()); return; }
  } catch (e) {
    alert('Could not clear history: ' + e);
    return;
  }
  if (detailJobId && finished.some(h => h.id === detailJobId)) closeDetail();
  loadHistory();
}

function rerunHistoryJob(ev, id) {
  if (ev) {
    ev.preventDefault();
    ev.stopPropagation();
  }
  rerunJob(id);
}

async function removeHistoryEntry(ev, id, btn) {
  if (ev) { ev.preventDefault(); ev.stopPropagation(); }
  if (!btn) return;

  if (btn.dataset.confirm !== '1') {
    btn.dataset.confirm = '1';
    btn.innerHTML = iconSvg('question_mark');
    setTimeout(() => {
      if (btn.dataset.confirm === '1') {
        btn.dataset.confirm = '';
        btn.innerHTML = iconSvg('delete');
        if (_historyVisible()) renderHistory(gid('hist-search').value); // refreshes that came during the confirm were held back
      }
    }, 3000);
    return;
  }

  btn.dataset.confirm = '';
  btn.disabled = true;
  try {
    const r = await fetch('api/helpers/' + id + '/delete', { method: 'DELETE' });
    if (!r.ok) {
      const msg = await r.text();
      btn.disabled = false;
      btn.innerHTML = iconSvg('delete');
      alert('Could not remove entry: ' + msg);
      return;
    }
    if (detailJobId === id) closeDetail();
    forgetHistoryRun(id);
    renderHistory(gid('hist-search').value);
  } catch (e) {
    btn.disabled = false;
    btn.innerHTML = iconSvg('delete');
    alert('Could not remove entry: ' + e);
  }
}
