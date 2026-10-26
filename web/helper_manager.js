/* ── Helper manager ──────────────────────── */
// A modal with two tabs: the helpers the sources offer, and the helpers installed by directory.
// The lists come from GET /api/helpers/manager. Add, Upgrade and Remove post to
// /api/helpers/{add,upgrade,remove} and follow the task's log.
let _hm = null;       // last response of /api/helpers/manager
let _hmTab = 'available';
let _hmShownAvail = [];
let _hmShownInstalled = [];

function openHelperManager() {
  gid('hm-search').value = '';
  hmSwitchTab('available');
  gid('hm-modal').classList.add('open');
  hmLoad();
}

async function hmLoad() {
  gid('hm-available-list').innerHTML = '<div class="modal-empty">Loading…</div>';
  gid('hm-installed-list').innerHTML = '<div class="modal-empty">Loading…</div>';
  try {
    const r = await fetch('/api/helpers/manager');
    if (!r.ok) throw new Error(await r.text());
    _hm = await r.json();
  } catch (e) {
    _hm = null;
    gid('hm-available-list').innerHTML = '<div class="modal-empty">' + escHtml(String(e)) + '</div>';
    gid('hm-installed-list').innerHTML = '';
    return;
  }
  hmRender();
}

function hmSwitchTab(tab) {
  _hmTab = tab;
  ['available', 'installed'].forEach(t => {
    gid('hm-tab-' + t + '-btn').classList.toggle('active', t === tab);
    gid('hm-tab-' + t).classList.toggle('active', t === tab);
  });
  hmRender();
}

function _hmBadge(state) {
  if (state === 'newer') return '<span class="badge badge-amber">newer version</span>';
  if (state === 'added') return '<span class="badge badge-green">added</span>';
  return '';
}

function hmRender() {
  if (!_hm) return;
  const terms = searchTerms(gid('hm-search').value);
  const match = h => !terms.length || matchesAllTerms(h.name + ' ' + (h.description || ''), terms);

  // Available
  _hmShownAvail = _hm.offered.filter(match);
  let html = '';
  if (_hm.offered_error) {
    html += '<div class="modal-empty">' + escHtml(_hm.offered_error) + '</div>';
  } else if (!_hmShownAvail.length) {
    html += '<div class="modal-empty">' + (terms.length ? 'No matches.' : 'No helpers offered.') + '</div>';
  }
  html += _hmShownAvail.map((h, i) =>
    '<div class="modal-row">' +
      '<span class="modal-row-name"><span class="oc-row-name">' + escHtml(h.name) + '</span>' +
        (h.description ? '<span class="oc-description">' + escHtml(h.description) + '</span>' : '') + '</span>' +
      '<span class="modal-row-size">' + _hmBadge(h.state) + '</span>' +
      (h.state ? '' : '<button class="btn btn-sm btn-primary" onclick="hmAdd(' + i + ')">Add</button>') +
    '</div>').join('');
  gid('hm-available-list').innerHTML = html;

  // Installed
  _hmShownInstalled = [];
  html = '';
  for (const g of _hm.installed) {
    const rows = g.scripts.filter(match);
    if (!rows.length) continue;
    html += '<div class="oc-group-hdr">' + escHtml(g.layer) + ' · ' + escHtml(g.dir) + '</div>';
    for (const s of rows) {
      const i = _hmShownInstalled.push({ layer: g.layer, script: s }) - 1;
      const state = s.newer ? 'newer' : (s.added ? 'added' : '');
      html += '<div class="modal-row">' +
        '<span class="modal-row-name"><span class="oc-row-name">' + escHtml(s.name) + '</span>' +
          (s.description ? '<span class="oc-description">' + escHtml(s.description) + '</span>' : '') + '</span>' +
        '<span class="modal-row-size">' + _hmBadge(state) +
          (s.hidden_by ? ' <span class="badge badge-grey" title="' + escHtml(s.hidden_by) + '">hidden</span>' : '') + '</span>' +
        (s.newer ? '<button class="btn btn-sm" onclick="hmUpgrade(' + i + ')">Upgrade</button>' : '') +
        (s.added ? '<button class="btn btn-sm btn-flat btn-icon btn-icon-danger" title="Remove" onclick="hmRemove(' + i + ')">' + iconSvg('delete') + '</button>' : '') +
      '</div>';
    }
  }
  if (!html) html = '<div class="modal-empty">' + (terms.length ? 'No matches.' : 'No helpers installed.') + '</div>';
  gid('hm-installed-list').innerHTML = html;

  const outdated = _hm.installed.reduce((n, g) => n + g.scripts.filter(s => s.newer).length, 0);
  gid('hm-tab-installed-btn').textContent = 'Installed' + (outdated ? ' (' + outdated + ' to upgrade)' : '');
  gid('hm-upgrade-all').hidden = !outdated;

  const sel = gid('hm-layer');
  const layers = _hm.layers || [];
  const previous = sel.value;
  sel.innerHTML = '<option value="">default</option>' +
    layers.map(l => '<option value="' + escHtml(l) + '">' + escHtml(l) + '</option>').join('');
  if (layers.includes(previous)) sel.value = previous;
  gid('hm-layer-wrap').hidden = layers.length < 2 || _hmTab !== 'available';
}

// hmPost posts a change and follows its task, then reloads the manager and the Start page list.
async function hmPost(title, url, body) {
  let r;
  try {
    r = await fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
  } catch (e) { showProgressError(title, String(e)); return; }
  if (!r.ok) { showProgressError(title, (await r.text()) || String(r.status)); return; }
  const { id } = await r.json();
  openLogProgress(title, id, async () => {
    await hmLoad();
    _helpersLoaded = false;
    await loadHelpers(true);
  });
}

// A layer the server cannot parse is sent as no layer.
function _hmLayerArg(layer) { return layer === 'unknown' ? '' : layer; }

function hmAdd(i) {
  const h = _hmShownAvail[i];
  hmPost('Adding ' + h.name, '/api/helpers/add', { names: [h.name], layer: gid('hm-layer').value });
}

function hmUpgrade(i) {
  const { layer, script } = _hmShownInstalled[i];
  hmPost('Upgrading ' + script.name, '/api/helpers/upgrade', { names: [script.name], layer: _hmLayerArg(layer) });
}

async function hmRemove(i) {
  const { layer, script } = _hmShownInstalled[i];
  const ok = await askConfirm('Remove helper "' + script.name + '" from the ' + layer + ' layer? A helper already running keeps its own copy.',
    { title: 'Remove helper', okLabel: 'Remove' });
  if (!ok) return;
  hmPost('Removing ' + script.name, '/api/helpers/remove', { names: [script.name], layer: _hmLayerArg(layer) });
}

// hmUpgradeAll shows what an upgrade of every added helper would do, and asks before it does it.
async function hmUpgradeAll() {
  let plan;
  try {
    const r = await fetch('/api/helpers/upgrade', {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ dry_run: true }),
    });
    if (!r.ok) throw new Error((await r.text()) || String(r.status));
    plan = (await r.json()).items;
  } catch (e) { showProgressError('Upgrading helpers', String(e)); return; }

  const replace = plan.filter(p => p.action === 'replace');
  const kept = plan.filter(p => p.action !== 'replace' && p.action !== 'current');
  if (!replace.length) {
    showProgressError('Upgrading helpers', 'Nothing to replace.');
    return;
  }
  let msg = 'Replace ' + replace.length + ' helper(s)?\n' + replace.map(p => '  ' + p.name + '  (' + p.layer + ')').join('\n');
  if (kept.length) {
    msg += '\n\nKept: ' + kept.map(p => p.name + ' (' + p.action + ')').join(', ');
  }
  if (!(await askConfirm(msg, { title: 'Upgrade helpers', okLabel: 'Upgrade' }))) return;
  hmPost('Upgrading helpers', '/api/helpers/upgrade', {});
}
