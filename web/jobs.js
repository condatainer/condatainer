/* ── Message text renderer (linkifies URLs) ─ */
function appendMsgText(text, container) {
  const urlRe = /https?:\/\/\S+/g;
  let last = 0, m;
  while ((m = urlRe.exec(text)) !== null) {
    if (m.index > last) container.appendChild(document.createTextNode(text.slice(last, m.index)));
    const a = document.createElement('a');
    a.href = goURL(m[0]); a.textContent = m[0]; a.target = '_blank'; a.rel = 'noopener noreferrer';
    container.appendChild(a);
    last = m.index + m[0].length;
  }
  if (last < text.length) container.appendChild(document.createTextNode(text.slice(last)));
}

/* ── Jobs section ────────────────────────── */
let _prevJobStatuses = null; // null = first load not yet done

function loadJobs() { return refreshJobs(); }

document.addEventListener('jobs:changed', () => {
  _checkJobNotifications(allJobs);
  if (!document.querySelector('#job-list-wrap [data-confirm="1"]')) renderJobs();
  refreshDetail();
});

function _checkJobNotifications(jobs) {
  if (_prevJobStatuses === null) {
    _prevJobStatuses = {};
    jobs.forEach(j => { _prevJobStatuses[j.id] = j.status; });
    return;
  }
  const next = {};
  jobs.forEach(j => {
    next[j.id] = j.status;
    if (j.status === 'running' && _prevJobStatuses[j.id] !== 'running') {
      _fireJobNotification(j);
    }
  });
  _prevJobStatuses = next;
}

function _fireJobNotification(job) {
  if (srvNotification !== 'web' && srvNotification !== 'both') return;
  if (typeof Notification === 'undefined' || Notification.permission !== 'granted') return;
  new Notification('Helper ready: ' + job.name, {
    body: job.node ? 'Running on ' + shortNode(job.node) : 'Job started',
    tag: 'condatainer-' + job.id,
  });
}

let _jobsHTML = null; // the markup on screen, so a list that did not change is left alone

function renderJobs() {
  const wrap = gid('job-list-wrap');
  let html;
  if (!allJobs.length) {
    html = '<div class="empty"><div>No active jobs.</div>' +
      '<div class="empty-link"><a href="#" onclick="navigate(\'start\');return false;">Start a helper →</a></div></div>';
  } else {
    const stableJobIds = oldestRunningJobIdsByName(allJobs);
    html = '<div class="job-list">' + allJobs.map(j => jobCardHTML(j, stableJobIds)).join('') + '</div>';
  }
  if (html !== _jobsHTML) {
    wrap.innerHTML = html;
    _jobsHTML = html;
    if (allJobs.length) startWalltimeCountdowns();
  }
  markSelected();
}

function oldestRunningJobIdsByName(jobs) {
  const ids = {};
  jobs.forEach(j => {
    if (j.status !== 'running' || !j.name || !j.id) return;
    if (!(j.name in ids) || j.id < ids[j.name]) ids[j.name] = j.id;
  });
  return ids;
}

function stableHelperURL(j, url) {
  if (!url || j.external_url || j.port === 0) return '';
  try {
    const u = new URL(url);
    if (u.hostname !== 'localhost' && !u.hostname.endsWith('.localhost')) return '';
    u.hostname = j.name + '.localhost';
    return u.toString();
  } catch {
    return '';
  }
}

function jobCardHTML(j, stableJobIds) {
  const endsAt     = j.ends_at_ms || 0;
  const showEndsIn = endsAt && j.status === 'running';
  const walltime   = fmtTimeDetail(j.walltime_secs);
  const walltimeEl = showEndsIn
    ? '<span data-ends="' + endsAt + '" class="walltime-span"></span>'
    : (walltime ? 'Walltime ' + escHtml(walltime) : '');
  const overlay = j.env_overlay || '';
  const url     = j.access_url || j.external_url || '';
  const stableURL = stableJobIds[j.name] === j.id ? stableHelperURL(j, url) : '';
  const lastMsg = j.last_message || '';
  const id      = escHtml(j.id);
  return (
    '<div class="job-card" id="jcard-' + id + '" tabindex="0" role="button" onclick="openDetail(' + jsArg(j.id) + ')">' +
      '<div class="jc-top">' +
        '<span class="jc-name">' + escHtml(j.name) + '</span>' +
        '<span class="badge ' + badgeClass(j.status) + '">' + escHtml(j.status) + '</span>' +
      '</div>' +
      '<div class="jc-meta">' +
        '<span title="' + escHtml(j.node || '') + '">' + escHtml(shortNode(j.node) || '—') + '</span><span class="jc-sep">|</span>' +
        pathTailHtml(j.cwd) +
        (overlay
          ? '<span class="jc-sep">|</span>' + pathTailHtml(overlay, '200px')
          : '') +
      '</div>' +
      '<div class="jc-walltime" id="jwall-' + id + '">' + walltimeEl + '</div>' +
      (lastMsg ? '<div class="jc-lastmsg">"' + escHtml(lastMsg) + '"</div>' : '') +
      (url
        ? '<div class="jc-access" onclick="event.stopPropagation()">' +
            '<a class="jc-link" href="' + escHtml(goURL(url)) + '" target="_blank" title="' + escHtml(url) + '">' +
              '<span class="jc-link-text">' + escHtml(url) + '</span><span class="jc-link-ext">↗</span>' +
            '</a>' +
            '<button class="btn btn-sm jc-icon-btn" onclick="copyStr(' + jsArg(url) + ',this)" title="Copy URL">' + iconSvg('content_copy') + '</button>' +
            (stableURL
              ? '<a class="btn btn-sm jc-icon-btn" href="' + escHtml(goURL(stableURL)) + '" target="_blank" title="' + escHtml(stableURL) + '">' + iconSvg('link_2') + '</a>'
              : '') +
          '</div>'
        : '') +
      '<div class="jc-stop-wrap" onclick="event.stopPropagation()">' +
        '<button class="btn btn-danger btn-sm" onclick="stopJob(' + jsArg(j.id) + ',this)">Stop</button>' +
      '</div>' +
    '</div>'
  );
}

async function stopJob(id, btn) {
  if (btn.dataset.confirm === '1') {
    btn.dataset.confirm = '';
    btn.innerHTML = 'Stopping…';
    btn.disabled = true;

    let ok = false;
    try {
      const r = await fetch('api/helpers/' + encodeURIComponent(id) + '/stop', { method: 'POST' });
      ok = r.ok;
    } catch {}

    _jobsHTML = null; // the button was changed by hand, so the list is built again
    await refreshJobs();
    if (detailJobId === id) closeDetail();

    if (!ok) {
      const card = gid('jcard-' + id);
      const newBtn = card && card.querySelector('.btn-danger');
      if (newBtn) {
        newBtn.textContent = 'Stop failed';
        setTimeout(() => { if (newBtn.textContent === 'Stop failed') newBtn.textContent = 'Stop'; }, 3000);
      }
    }
  } else {
    btn.dataset.confirm = '1';
    btn.innerHTML = 'Confirm?';
    setTimeout(() => {
      if (btn.dataset.confirm === '1') {
        btn.dataset.confirm = '';
        btn.innerHTML = 'Stop';
        renderJobs(); // refreshes that came during the confirm were held back
      }
    }, 3000);
  }
}

/* ── Walltime countdowns ──────────────────── */
function startWalltimeCountdowns() {
  tickCountdowns();
  if (!window._walltimeTick) window._walltimeTick = setInterval(tickCountdowns, 30000);
}
function tickCountdowns() {
  document.querySelectorAll('.walltime-span').forEach(el => {
    const ends = Number(el.dataset.ends);
    const diff = ends - Date.now();
    const parent = el.parentElement;
    if (diff <= 0) {
      el.innerHTML = iconSvg('warning') + ' Walltime reached';
      parent && parent.classList.add('warn');
    } else {
      const h = Math.floor(diff / 3600e3);
      const m = Math.floor((diff % 3600e3) / 60e3);
      el.textContent = 'Ends in ' + h + 'h ' + m + 'm';
      parent && parent.classList.remove('warn');
    }
  });
}

/* ── Detail panel ────────────────────────── */
function openDetail(id) {
  const job = allJobs.find(j => j.id === id) || allHistory.find(j => j.id === id);
  if (!job) return;
  const detail = gid('detail');
  if (detailJobId === id && detail && detail.classList.contains('open')) {
    closeDetail();
    return;
  }
  detailJobId = id;
  const activeSec = document.querySelector('.section.active');
  const secId = activeSec ? activeSec.id.slice(4) : '';
  if (secId === 'jobs' || secId === 'history') setHash('#' + secId + '/' + id, false);

  renderDetail(job);
  switchDetailTab(detailTabActive);
  detail.removeAttribute('inert');
  detail.setAttribute('aria-hidden', 'false');
  detail.classList.add('open');
}

// refreshDetail redraws the open detail panel from the latest lists, and
// closes it when its job is gone. The messages and log tab are left alone.
// A job that left the active list is looked up in a fresh history.
function refreshDetail() {
  if (!detailJobId) return;
  const active = allJobs.find(j => j.id === detailJobId);
  if (!active && _historyStale) { loadHistory(); return; }
  const job = active || allHistory.find(j => j.id === detailJobId);
  if (job) renderDetail(job);
  else closeDetail();
}

// markSelected highlights the open job's card or row in the section on screen.
// The Jobs list keeps a card for a running job while History is shown, so only
// the active section is searched.
function markSelected() {
  document.querySelectorAll('.job-card,.data-table tbody tr').forEach(e => e.classList.remove('selected'));
  const section = document.querySelector('.section.active');
  if (!detailJobId || !section) return;
  const item = Array.from(section.querySelectorAll('.job-card, tbody tr[data-id]'))
    .find(e => (e.dataset.id || e.id.replace(/^jcard-/, '')) === detailJobId);
  if (item) item.classList.add('selected');
}

// Enter or Space on a focused card or row opens its detail panel.
document.addEventListener('keydown', e => {
  if (e.key !== 'Enter' && e.key !== ' ') return;
  const item = e.target.closest && e.target.closest('.job-card, tbody tr[data-id]');
  if (!item || e.target !== item) return;
  e.preventDefault();
  openDetail(item.dataset.id || item.id.replace(/^jcard-/, ''));
});

// renderDetail fills the detail panel's header, meta, resources, parameters and
// buttons from job, and marks its card or row as selected.
function renderDetail(job) {
  const id = job.id;
  const isRunning = job.status === 'running' || job.status === 'pending' || job.status === 'starting';

  markSelected();

  gid('d-title').textContent = job.name;
  gid('d-badge').innerHTML = '<span class="badge ' + badgeClass(job.status) + '">' + escHtml(job.status) + '</span>';

  // Meta — grouped: Location | Container | Job | Access
  let meta = '';
  // Location
  meta += '<span class="d-key">Working Dir</span>' + pathTailHtml(job.cwd, null, 'd-val');
  // Container
  meta += '<span class="d-key">Env Overlay</span>' + pathTailHtml(job.env_overlay, null, 'd-val');
  if (job.overlays && job.overlays.length) {
    meta += '<span class="d-key">Modules</span><span class="d-val-col">' +
      job.overlays.map(p => {
        const o = allOverlays.find(ov => ov.path === p || ov.name === p);
        return o
          ? '<span title="' + escHtml(p) + '">' + escHtml(o.name) + '</span>'
          : pathTailHtml(p, null);
      }).join('') + '</span>';
  }
  // Job
  if (job.runner) meta += '<span class="d-key">Runner</span><span class="d-val">' + escHtml(job.runner) + '</span>';
  if (job.job_id) meta += '<span class="d-key">Job ID</span><span class="d-val">' + escHtml(job.job_id) + '</span>';
  meta += '<span class="d-key">Started</span><span class="d-val">' + (job.started_at ? fmtDate(job.started_at) : '—') + '</span>';
  if (job.node) meta += '<span class="d-key">Node</span><span class="d-val">' + escHtml(job.node) + '</span>';
  if (job.port > 0) {
    const bindAddr = (job.connect === 'direct' ? '0.0.0.0' : '127.0.0.1') + ':' + job.port;
    meta += '<span class="d-key">Bind</span><span class="d-val">' + escHtml(bindAddr) + '</span>';
  }
  // Access
  const url = job.access_url || job.external_url;
  if (url) meta += '<span class="d-key">URL</span><span class="d-val"><a href="' + escHtml(goURL(url)) + '" target="_blank">' + escHtml(url) + '</a></span>';
  gid('d-meta').innerHTML = meta;

  // Resources
  gid('d-resources').innerHTML =
    '<div class="d-res"><span class="d-res-k">CPUs</span><span class="d-res-v">'    + escHtml(job.cpu || '—')          + '</span></div>' +
    '<div class="d-res"><span class="d-res-k">Memory</span><span class="d-res-v">'  + escHtml(job.mem || '—')          + '</span></div>' +
    '<div class="d-res"><span class="d-res-k">GPU</span><span class="d-res-v">'     + escHtml(job.gpu || '—')          + '</span></div>' +
    '<div class="d-res"><span class="d-res-k">Walltime</span><span class="d-res-v">'+ escHtml(fmtTimeDetail(job.walltime_secs) || '—') + '</span></div>';

  // Params
  const params    = job.params || {};
  const paramKeys = Object.keys(params);
  const dp = gid('d-params');
  if (paramKeys.length) {
    dp.style.display = '';
    dp.innerHTML = paramKeys.map(k =>
      '<div class="d-param-row"><span class="d-param-k">' + escHtml(k) + '</span>' +
      '<span class="d-param-v">' + escHtml(params[k]) + '</span></div>'
    ).join('');
  } else { dp.style.display = 'none'; }

  gid('d-stop').style.display = isRunning ? '' : 'none';
  gid('d-rerun').disabled = isRunning;
  gid('d-log-link').href = 'api/helpers/' + encodeURIComponent(id) + '/joblog';
}

function switchDetailTab(tab) {
  detailTabActive = tab;
  document.querySelectorAll('.d-tab').forEach(t => t.classList.toggle('active', t.dataset.dtab === tab));
  const content = gid('d-tab-content');

  if (detailSSE) { detailSSE.close(); detailSSE = null; }

  if (tab === 'messages') {
    content.innerHTML = '';
    detailSSE = new EventSource('api/helpers/' + encodeURIComponent(detailJobId) + '/logs');
    detailSSE.onopen = function() {
      // Clear on each (re)connect so backfill doesn't duplicate old messages
      content.innerHTML = '';
    };
    detailSSE.onmessage = function(ev) {
      try {
        const m   = JSON.parse(ev.data);
        const lvl = m.level === 'warn' ? 'warn' : m.level === 'error' ? 'err' : 'info';
        const div = document.createElement('div');
        div.className = 'log-line log-' + lvl;
        if (m.ts) {
          const ts = document.createElement('span');
          ts.className = 'log-ts';
          try { ts.textContent = new Date(m.ts).toLocaleTimeString(); } catch {}
          div.appendChild(ts);
        }
        appendMsgText(m.text || '', div);
        content.appendChild(div);
        content.scrollTop = content.scrollHeight;
        // Update last-message snippet on the card
        const card = gid('jcard-' + detailJobId);
        if (card) {
          const lm = card.querySelector('.jc-lastmsg');
          if (lm) lm.textContent = '"' + (m.text || '') + '"';
        }
      } catch {}
    };
    detailSSE.onerror = function() {
      // CONNECTING means the browser is auto-reconnecting — don't interfere.
      if (detailSSE && detailSSE.readyState === EventSource.CONNECTING) return;
      if (detailSSE) { detailSSE.close(); detailSSE = null; }
      const dim = document.createElement('div');
      dim.className = 'log-line log-dim';
      dim.textContent = '— stream ended —';
      content.appendChild(dim);
    };
  } else {
    // Job log tab — request last 1000 lines to avoid freezing on large logs
    content.innerHTML = '<div class="state-muted">Loading log…</div>';
    fetch('api/helpers/' + encodeURIComponent(detailJobId) + '/joblog?tail=1000')
      .then(r => {
        const truncated = r.headers.get('X-Log-Truncated') === 'true';
        return r.text().then(text => ({ text, truncated }));
      })
      .then(({ text, truncated }) => {
        content.innerHTML = '';
        if (truncated) {
          const notice = document.createElement('div');
          notice.className = 'log-line log-dim';
          notice.classList.add('log-notice');
          notice.textContent = '— showing last 1000 lines (use Full log ↗ for complete output) —';
          content.appendChild(notice);
        }
        const pre = document.createElement('pre');
        pre.className = 'log-pre';
        pre.textContent = text || '(empty log)';
        content.appendChild(pre);
        content.scrollTop = content.scrollHeight;
      })
      .catch(() => { content.innerHTML = '<div class="state-danger">Failed to load log.</div>'; });
  }
}

function closeDetail() {
  const detail = gid('detail');
  detail.classList.remove('open');
  detail.setAttribute('inert', '');
  detail.setAttribute('aria-hidden', 'true');
  if (detail.contains(document.activeElement)) document.activeElement.blur();
  document.querySelectorAll('.job-card,.data-table tbody tr').forEach(e => e.classList.remove('selected'));
  if (detailSSE) { detailSSE.close(); detailSSE = null; }
  detailJobId = null;
  if (/^#(jobs|history)\/./.test(location.hash)) setHash(location.hash.split('/')[0], false);
}

function detailStop() {
  const btn = gid('d-stop');
  if (btn && detailJobId) stopJob(detailJobId, btn);
}

async function rerunJob(jobOrId) {
  const id = typeof jobOrId === 'string' ? jobOrId : detailJobId;
  const job = typeof jobOrId === 'object' && jobOrId
    ? jobOrId
    : allJobs.find(j => j.id === id) || allHistory.find(j => j.id === id);
  if (!job) return;
  await ensureOverlaysLoaded();
  queueStartSelection(job.name, {
    cpu:     job.cpu,
    mem:     job.mem,
    wall:    fmtTimeDetail(job.walltime_secs),
    gpu:     job.gpu     || '',
    account:   job.account   || '',
    partition: job.partition || '',
    noProject: !!job.no_project,
    cwd:     job.cwd     || '',
    overlay: job.env_overlay || '',
    modules:   (job.overlays || []).map(p => allOverlays.find(o => o.name === p || o.path === p)).filter(Boolean),
    externals: (job.overlays || []).filter(p => !allOverlays.find(o => o.name === p || o.path === p)),
    params:  job.params  || {},
  });
  navigate('start');
}
