// llm-proxy admin console: a dependency-free SPA. All DOM is built with h(),
// which only ever sets text and attributes, so API data can never inject markup.

const SVG = 'http://www.w3.org/2000/svg';

function h(tag, attrs, ...kids) {
  const el = tag.startsWith('svg:') ? document.createElementNS(SVG, tag.slice(4)) : document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === false || v == null) continue;
    if (k === 'class') el.setAttribute('class', v);
    else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else if (k === 'value') el.value = v;
    else if (k === 'checked' || k === 'disabled' || k === 'selected') { if (v) el.setAttribute(k, ''); }
    else el.setAttribute(k, v === true ? '' : v);
  }
  for (const kid of kids.flat(Infinity)) {
    if (kid == null || kid === false) continue;
    el.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
  }
  return el;
}

// ---------- API ----------
class ApiError extends Error { constructor(msg, status, code) { super(msg); this.status = status; this.code = code; } }

async function api(method, path, body) {
  const opts = { method, headers: { 'X-Requested-With': 'llm-proxy-admin' }, credentials: 'same-origin' };
  if (body !== undefined) { opts.headers['Content-Type'] = 'application/json'; opts.body = JSON.stringify(body); }
  const res = await fetch(path, opts);
  if (res.status === 401) { location.href = '/auth/login?next=' + encodeURIComponent(location.pathname); throw new ApiError('signed out', 401); }
  const text = await res.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch { /* not JSON */ }
  if (!res.ok) throw new ApiError(data?.error?.message || `HTTP ${res.status}`, res.status, data?.error?.code);
  return data;
}

// ---------- formatting ----------
const nf = new Intl.NumberFormat('en-US');
const cf = new Intl.NumberFormat('en-US', { notation: 'compact', maximumFractionDigits: 1 });
const fmt = (n) => nf.format(Math.round(Number(n) || 0));
const compact = (n) => cf.format(Number(n) || 0);
const pct = (x) => (x * 100 >= 10 ? (x * 100).toFixed(0) : (x * 100).toFixed(1)) + '%';
const usd = (x) => (x == null ? '—' : '$' + (x >= 100 ? x.toFixed(0) : x.toFixed(2)));
const when = (ts) => (ts ? new Date(ts).toLocaleString() : '—');
const effortName = (e) => ({ EFFORT_LOW: 'low', EFFORT_MEDIUM: 'medium', EFFORT_HIGH: 'high' }[e] || 'any');
const num = (v) => { const n = Number(v); return Number.isFinite(n) ? n : 0; };

// ---------- UI helpers ----------
function toast(msg, bad) {
  const t = h('div', { class: 'toast' + (bad ? ' bad' : '') }, msg);
  document.getElementById('toasts').append(t);
  setTimeout(() => t.remove(), bad ? 7000 : 3500);
}

async function guarded(fn) {
  try { return await fn(); } catch (e) { if (e.status !== 401) toast(e.message, true); return undefined; }
}

function modal(title, body, { submit = 'Save', onSubmit, extraFoot } = {}) {
  const err = h('div', { class: 'formerr', role: 'alert' });
  const dlg = h('dialog');
  const close = () => { dlg.close(); dlg.remove(); };
  const form = h('form', { method: 'dialog', onsubmit: async (ev) => {
    ev.preventDefault();
    if (!onSubmit) return close();
    err.textContent = '';
    try { const keep = await onSubmit(new FormData(form)); if (keep !== false) close(); }
    catch (e) { if (e.status !== 401) err.textContent = e.message; }
  } },
    h('h2', {}, title), body, err,
    h('div', { class: 'foot' }, extraFoot, h('button', { type: 'button', onclick: close }, onSubmit ? 'Cancel' : 'Close'),
      onSubmit && h('button', { type: 'submit', class: 'primary' }, submit)));
  dlg.append(form);
  dlg.addEventListener('close', () => dlg.remove());
  document.body.append(dlg);
  dlg.showModal();
  return dlg;
}

function confirmBox(title, text, action, label = 'Delete') {
  modal(title, h('p', {}, text), { submit: label, onSubmit: async () => { await action(); } });
}

const field = (label, input, hint) => h('label', { class: 'f' }, label, input, hint && h('span', { class: 'note' }, hint));
const check = (name, label, on) => h('label', { class: 'f inline' }, h('input', { type: 'checkbox', name, checked: !!on }), label);

function emptyRow(cols, text) { return h('tr', {}, h('td', { colspan: cols, class: 'empty' }, text)); }

// ---------- state ----------
const S = { me: null, health: null, cfg: null };
const canWrite = () => S.me.user.role === 'admin';

async function loadCfg(force) {
  if (!S.cfg || force) S.cfg = await api('GET', '/api/proxy/config');
  return S.cfg;
}

// ---------- pages ----------
const pages = {};

pages.usage = async (root) => {
  const today = new Date();
  const iso = (d) => d.toISOString().slice(0, 10);
  const ago = (n) => iso(new Date(today.getTime() - n * 86400000));
  const st = { from: ago(29), to: iso(today), group: 'project', preset: 30 };
  const out = h('div');
  const groups = [['project', 'Project'], ['model', 'Model'], ['user', 'User'], ['action', 'Action'], ['vendor', 'Vendor'], ['none', 'Total only']];

  const fromIn = h('input', { type: 'date', value: st.from, onchange: () => { st.from = fromIn.value; st.preset = 0; load(); } });
  const toIn = h('input', { type: 'date', value: st.to, onchange: () => { st.to = toIn.value; st.preset = 0; load(); } });
  const groupSel = h('select', { onchange: () => { st.group = groupSel.value; load(); } },
    groups.map(([v, l]) => h('option', { value: v, selected: v === st.group }, l)));
  const presets = h('div', { class: 'seg' }, [7, 30, 90].map((n) => h('button', { type: 'button', class: st.preset === n ? 'on' : '', onclick: () => {
    st.preset = n; st.from = ago(n - 1); st.to = iso(today); fromIn.value = st.from; toIn.value = st.to;
    [...presets.children].forEach((b) => b.classList.toggle('on', b.textContent === n + 'd')); load();
  } }, n + 'd')));

  root.append(
    h('div', { class: 'head' }, h('div', {}, h('h1', {}, 'Usage'), h('p', { class: 'sub' }, 'Who spends your tokens, and are they worth it?'))),
    h('div', { class: 'controls' }, presets, field('From', fromIn), field('To', toIn), field('Group by', groupSel)),
    out);

  async function load() {
    out.replaceChildren(h('p', { class: 'sub' }, 'Loading…'));
    const q = new URLSearchParams({ from: st.from, to: st.to, group: st.group });
    const res = await guarded(() => api('GET', '/api/usage?' + q));
    if (!res) { out.replaceChildren(h('p', { class: 'err' }, 'Could not load usage.')); return; }
    out.replaceChildren(renderUsage(res.report, st.group));
  }
  load();
};

function renderUsage(rep, group) {
  const t = rep.totals;
  const tiles = h('div', { class: 'tiles' },
    tile('Tokens', compact(t.tokens_total), `${compact(t.tokens_input)} in · ${compact(t.tokens_output)} out`),
    tile('Requests', compact(t.requests), t.requests ? `${fmt(t.tokens_per_request)} tokens each` : ''),
    tile('Failed', pct(t.failure_rate), `${fmt(t.failed)} requests · ${compact(t.failed_tokens)} tokens wasted`),
    tile('Avg latency', t.requests ? (t.avg_latency_ms / 1000).toFixed(2) + ' s' : '—', ''),
    rep.cost_known ? tile('Est. cost', usd(t.cost_usd), 'from model list prices') : null);
  if (!t.requests) return h('div', {}, tiles, h('div', { class: 'panel empty' }, 'No usage recorded in this range.'));

  const max = Math.max(...rep.groups.map((g) => g.tokens_total), 1);
  const rows = rep.groups.map((g) => h('tr', {},
    h('td', {}, h('span', { class: 'mono' }, g.key), ' ', g.verdict && h('span', { class: 'badge ' + g.verdict, title: g.verdict === 'greedy' ? 'Big slice of all tokens at a high cost per request, or many tokens lost to failures' : 'Solid share, lean per request, reliable' }, g.verdict)),
    h('td', { class: 'num' }, fmt(g.tokens_total), h('span', { class: 'bar', style: `width:${Math.max(1, (g.tokens_total / max) * 100)}%` })),
    h('td', { class: 'num' }, pct(g.token_share)),
    h('td', { class: 'num' }, fmt(g.requests)),
    h('td', { class: 'num' }, fmt(g.tokens_per_request)),
    h('td', { class: 'num' }, g.failure_rate > 0 ? h('span', { class: g.failure_rate > 0.05 ? 'err' : '' }, pct(g.failure_rate)) : '0%'),
    h('td', { class: 'num' }, (g.avg_latency_ms / 1000).toFixed(2) + ' s'),
    rep.cost_known && h('td', { class: 'num' }, usd(g.cost_usd))));

  const labels = { project: 'Project', model: 'Model', user: 'User', action: 'Action', vendor: 'Vendor', none: 'All' };
  return h('div', {}, tiles,
    h('div', { class: 'panel' }, h('h2', {}, 'Tokens per day'), dailyChart(rep.daily)),
    group !== 'none' && h('div', { class: 'panel flush' }, h('table', {},
      h('thead', {}, h('tr', {}, h('th', {}, labels[group]), h('th', { class: 'num' }, 'Tokens'), h('th', { class: 'num' }, 'Share'),
        h('th', { class: 'num' }, 'Requests'), h('th', { class: 'num' }, 'Tokens / req'), h('th', { class: 'num' }, 'Failed'),
        h('th', { class: 'num' }, 'Latency'), rep.cost_known && h('th', { class: 'num' }, 'Est. cost'))),
      h('tbody', {}, rows))),
    h('p', { class: 'note' }, 'Greedy: ≥10% of all tokens at ≥1.5× the average tokens per request, or ≥5% of tokens with ≥20% of them lost to failed requests. Worthy: ≥1% of tokens, at or below the average per request, ≤5% failures. Needs at least two groups and 5 requests.'));
}

const tile = (k, v, s) => h('div', { class: 'tile' }, h('div', { class: 'k' }, k), h('div', { class: 'v' }, v), h('div', { class: 's' }, s || ' '));

function dailyChart(days) {
  const W = 800, H = 150, pad = 18, gap = 2;
  const svg = h('svg:svg', { class: 'chart', viewBox: `0 0 ${W} ${H}`, preserveAspectRatio: 'none', role: 'img', 'aria-label': 'Tokens per day' });
  const max = Math.max(...days.map((d) => d.tokens), 1);
  const bw = Math.min(36, Math.max(2, (W - gap * days.length) / Math.max(days.length, 1)));
  days.forEach((d, i) => {
    const hh = Math.max(1, ((H - pad) * d.tokens) / max);
    const x = i * (bw + gap);
    const title = h('svg:title', {}, `${d.bucket}: ${fmt(d.tokens)} tokens, ${fmt(d.requests)} requests, ${fmt(d.failed)} failed`);
    svg.append(h('svg:rect', { class: 't', x, y: H - pad - hh, width: bw, height: hh, rx: 1.5 }, title));
  });
  if (days.length) {
    svg.append(h('svg:text', { x: 0, y: H - 4 }, days[0].bucket), h('svg:text', { x: W, y: H - 4, 'text-anchor': 'end' }, days[days.length - 1].bucket));
  }
  return svg;
}

// ----- models -----
const VENDORS = ['openai', 'anthropic', 'google', 'deepseek', 'groq', 'mistral', 'ollama', 'typesafe', 'local'];
const CAPS = ['google_search', 'no_structured_output', 'tts', 'image', 'judge'];

pages.models = async (root) => {
  const body = h('tbody');
  const render = async (force) => {
    const cfg = await guarded(() => loadCfg(force));
    if (!cfg) return;
    body.replaceChildren(...(cfg.models.length ? cfg.models.map(modelRow) : [emptyRow(9, 'No models yet. Add one to start routing.')]));
  };
  const modelRow = (m) => h('tr', { class: m.enabled ? '' : 'off' },
    h('td', {}, h('span', { class: 'mono' }, m.id)), h('td', {}, m.vendor),
    h('td', {}, m.efforts.map((e) => h('span', { class: 'chip' }, effortName(e)))),
    h('td', {}, m.capabilities.map((c) => h('span', { class: 'chip' }, c))),
    h('td', { class: 'num' }, num(m.rpm) || '∞'),
    h('td', { class: 'num' }, `$${m.price_in_per_mtok} / $${m.price_out_per_mtok}`),
    h('td', { class: 'num' }, num(m.daily_tokens_per_key) ? compact(m.daily_tokens_per_key) : '∞'),
    h('td', {}, m.enabled ? 'on' : 'off'),
    h('td', {}, canWrite() && h('div', { class: 'actions' },
      h('button', { class: 'sm', onclick: () => editModel(m) }, 'Edit'),
      h('button', { class: 'sm danger', onclick: () => confirmBox('Delete model', `Delete ${m.id}? Rules that use it must be changed first.`, async () => { await api('DELETE', '/api/proxy/models/' + encodeURIComponent(m.id)); toast('Model deleted'); await render(true); }) }, 'Delete'))));

  function editModel(m) {
    const isNew = !m;
    m = m || { id: '', vendor: 'openai', endpoint: '', efforts: ['EFFORT_LOW', 'EFFORT_MEDIUM', 'EFFORT_HIGH'], capabilities: [], rpm: 0, price_in_per_mtok: 0, price_out_per_mtok: 0, price_in_peak_per_mtok: 0, price_out_peak_per_mtok: 0, price_in_batch_per_mtok: 0, price_out_batch_per_mtok: 0, daily_tokens_per_key: 0, daily_tokens_per_user: 0, enabled: true };
    const ef = (e) => check('effort_' + e, e.toLowerCase(), m.efforts.includes('EFFORT_' + e));
    const form = h('div', { style: 'display:grid;gap:12px' },
      h('div', { class: 'row' }, field('Model id', h('input', { name: 'id', value: m.id, required: true, disabled: !isNew }), 'Sent to the vendor and referenced by rules'),
        field('Vendor', h('select', { name: 'vendor' }, VENDORS.map((v) => h('option', { value: v, selected: v === m.vendor }, v))))),
      field('Endpoint override', h('input', { name: 'endpoint', value: m.endpoint, placeholder: 'optional base URL' })),
      h('div', {}, h('div', { class: 'note' }, 'Efforts this model may serve'), h('div', { class: 'controls' }, ef('LOW'), ef('MEDIUM'), ef('HIGH'))),
      field('Capabilities', h('input', { name: 'capabilities', value: m.capabilities.join(', '), list: 'caps', placeholder: 'comma separated' }), 'Known: ' + CAPS.join(', ')),
      h('datalist', { id: 'caps' }, CAPS.map((c) => h('option', { value: c }))),
      h('div', { class: 'row' }, field('Price in ($/Mtok)', h('input', { name: 'price_in_per_mtok', type: 'number', step: 'any', min: 0, value: m.price_in_per_mtok })),
        field('Price out ($/Mtok)', h('input', { name: 'price_out_per_mtok', type: 'number', step: 'any', min: 0, value: m.price_out_per_mtok }))),
      h('div', { class: 'row' }, field('Peak price in', h('input', { name: 'price_in_peak_per_mtok', type: 'number', step: 'any', min: 0, value: m.price_in_peak_per_mtok })),
        field('Peak price out', h('input', { name: 'price_out_peak_per_mtok', type: 'number', step: 'any', min: 0, value: m.price_out_peak_per_mtok }))),
      h('div', { class: 'row' }, field('Batch price in', h('input', { name: 'price_in_batch_per_mtok', type: 'number', step: 'any', min: 0, value: m.price_in_batch_per_mtok })),
        field('Batch price out', h('input', { name: 'price_out_batch_per_mtok', type: 'number', step: 'any', min: 0, value: m.price_out_batch_per_mtok }))),
      h('div', { class: 'row' }, field('Requests / min per key', h('input', { name: 'rpm', type: 'number', min: 0, value: num(m.rpm) }), '0 = unlimited'),
        field('Daily tokens per key', h('input', { name: 'daily_tokens_per_key', type: 'number', min: 0, value: num(m.daily_tokens_per_key) }), '0 = unlimited')),
      field('Daily tokens per user', h('input', { name: 'daily_tokens_per_user', type: 'number', min: 0, value: num(m.daily_tokens_per_user) })),
      check('enabled', 'Enabled', m.enabled));
    modal(isNew ? 'Add model' : 'Edit ' + m.id, form, { onSubmit: async (fd) => {
      const id = isNew ? String(fd.get('id')).trim() : m.id;
      const spec = {
        vendor: fd.get('vendor'), endpoint: String(fd.get('endpoint')).trim(),
        efforts: ['LOW', 'MEDIUM', 'HIGH'].filter((e) => fd.get('effort_' + e)).map((e) => 'EFFORT_' + e),
        capabilities: String(fd.get('capabilities')).split(',').map((s) => s.trim()).filter(Boolean),
        rpm: num(fd.get('rpm')), price_in_per_mtok: num(fd.get('price_in_per_mtok')), price_out_per_mtok: num(fd.get('price_out_per_mtok')),
        price_in_peak_per_mtok: num(fd.get('price_in_peak_per_mtok')), price_out_peak_per_mtok: num(fd.get('price_out_peak_per_mtok')),
        price_in_batch_per_mtok: num(fd.get('price_in_batch_per_mtok')), price_out_batch_per_mtok: num(fd.get('price_out_batch_per_mtok')),
        daily_tokens_per_key: num(fd.get('daily_tokens_per_key')), daily_tokens_per_user: num(fd.get('daily_tokens_per_user')),
        enabled: !!fd.get('enabled'),
      };
      await api('PUT', '/api/proxy/models/' + encodeURIComponent(id), spec);
      toast('Model saved'); await render(true);
    } });
  }

  root.append(
    h('div', { class: 'head' }, h('div', {}, h('h1', {}, 'Models'), h('p', { class: 'sub' }, 'The registry rules route to. Prices feed the cost estimates.')),
      canWrite() && h('button', { class: 'primary', onclick: () => editModel(null) }, 'Add model')),
    h('div', { class: 'panel flush' }, h('table', {}, h('thead', {}, h('tr', {}, ['Id', 'Vendor', 'Efforts', 'Capabilities'].map((x) => h('th', {}, x)),
      h('th', { class: 'num' }, 'RPM'), h('th', { class: 'num' }, 'In / out $/Mtok'), h('th', { class: 'num' }, 'Daily tokens'), h('th', {}, 'State'), h('th'))), body)));
  render();
};

// ----- rules -----
pages.rules = async (root) => {
  const body = h('tbody');
  let rules = [];
  const render = async (force) => {
    const cfg = await guarded(() => loadCfg(force));
    if (!cfg) return;
    rules = [...cfg.rules].sort((a, b) => b.priority - a.priority);
    body.replaceChildren(...(rules.length ? rules.map(ruleRow) : [emptyRow(7, 'No rules yet. Add a catch-all rule first.')]));
  };
  const save = async (next, msg) => {
    await api('PUT', '/api/proxy/rules', { rules: next });
    toast(msg); await render(true);
  };
  const ruleRow = (r) => h('tr', { class: r.enabled ? '' : 'off' },
    h('td', { class: 'num' }, r.priority), h('td', {}, h('span', { class: 'mono' }, r.name)),
    h('td', {}, effortName(r.effort)),
    h('td', {}, Object.keys(r.attributes || {}).length ? Object.entries(r.attributes).map(([k, v]) => h('span', { class: 'chip mono' }, `${k}=${v}`)) : h('span', { class: 'note' }, 'catch-all')),
    h('td', {}, r.use.map((u, i) => [i > 0 && ' → ', h('span', { class: 'mono' }, u)])),
    h('td', {}, r.enabled ? 'on' : 'off'),
    h('td', {}, canWrite() && h('div', { class: 'actions' },
      h('button', { class: 'sm', onclick: () => edit(r) }, 'Edit'),
      h('button', { class: 'sm danger', onclick: () => confirmBox('Delete rule', `Delete ${r.name}? The remaining rules must still cover every effort.`, () => save(rules.filter((x) => x.name !== r.name), 'Rule deleted')) }, 'Delete'))));

  async function edit(r) {
    const cfg = await guarded(() => loadCfg());
    if (!cfg) return;
    const isNew = !r;
    const nextPrio = (rules[0]?.priority || 0) + 10;
    r = r || { name: '', priority: nextPrio, effort: 'EFFORT_UNSPECIFIED', attributes: {}, use: [], enabled: true };
    const attrs = Object.entries(r.attributes || {}).map(([k, v]) => `${k}=${v}`).join('\n');
    const form = h('div', { style: 'display:grid;gap:12px' },
      h('div', { class: 'row' }, field('Name', h('input', { name: 'name', value: r.name, required: true, disabled: !isNew })),
        field('Priority', h('input', { name: 'priority', type: 'number', value: r.priority, required: true }), 'Higher wins; unique')),
      field('Effort', h('select', { name: 'effort' }, [['EFFORT_UNSPECIFIED', 'any'], ['EFFORT_LOW', 'low'], ['EFFORT_MEDIUM', 'medium'], ['EFFORT_HIGH', 'high']].map(([v, l]) => h('option', { value: v, selected: v === r.effort }, l)))),
      field('Attributes (one key=value per line)', h('textarea', { name: 'attributes', rows: 3, placeholder: 'subject=french\nlanguage=es' }, attrs), 'Empty = catch-all for the effort'),
      field('Model chain, cheapest first', h('input', { name: 'use', value: r.use.join(', '), list: 'mids', required: true, placeholder: 'model-a, model-b' }), 'Comma separated registry ids; later ones are fallbacks'),
      h('datalist', { id: 'mids' }, cfg.models.map((m) => h('option', { value: m.id }))),
      check('enabled', 'Enabled', r.enabled));
    modal(isNew ? 'Add rule' : 'Edit ' + r.name, form, { onSubmit: async (fd) => {
      const attributes = {};
      for (const line of String(fd.get('attributes')).split('\n')) {
        const t = line.trim(); if (!t) continue;
        const i = t.indexOf('=');
        if (i < 1) throw new Error(`Attribute line "${t}" must look like key=value`);
        attributes[t.slice(0, i).trim()] = t.slice(i + 1).trim();
      }
      const rule = { name: isNew ? String(fd.get('name')).trim() : r.name, priority: num(fd.get('priority')), effort: fd.get('effort'), attributes,
        use: String(fd.get('use')).split(',').map((s) => s.trim()).filter(Boolean), enabled: !!fd.get('enabled') };
      const next = isNew ? [...rules, rule] : rules.map((x) => (x.name === r.name ? rule : x));
      await save(next, 'Rules saved');
    } });
  }

  root.append(
    h('div', { class: 'head' }, h('div', {}, h('h1', {}, 'Rules'), h('p', { class: 'sub' }, 'The highest-priority matching rule decides which models answer. Every effort needs a catch-all.')),
      canWrite() && h('button', { class: 'primary', onclick: () => edit(null) }, 'Add rule')),
    h('div', { class: 'panel flush' }, h('table', {}, h('thead', {}, h('tr', {}, h('th', { class: 'num' }, 'Priority'), ['Name', 'Effort', 'Matches', 'Chain', 'State'].map((x) => h('th', {}, x)), h('th'))), body)),
    h('p', { class: 'note' }, 'Speech, image and judgment calls route at low effort with the reserved attribute modality=tts|image|judge.'));
  render();
};

// ----- keys -----
pages.keys = async (root) => {
  const keysBody = h('tbody'), vendorBody = h('tbody');
  const render = async (force) => {
    const [keys, cfg] = await Promise.all([guarded(() => api('GET', '/api/proxy/keys')), guarded(() => loadCfg(force))]);
    if (!keys || !cfg) return;
    keysBody.replaceChildren(...(keys.keys.length ? keys.keys.map((k) => h('tr', { class: k.disabled ? 'off' : '' },
      h('td', {}, h('span', { class: 'mono' }, k.name)), h('td', {}, k.scopes.map((s) => h('span', { class: 'chip' }, s))),
      h('td', {}, when(k.created_at)), h('td', {}, k.disabled ? 'revoked' : 'active'),
      h('td', {}, canWrite() && !k.disabled && h('div', { class: 'actions' }, h('button', { class: 'sm danger', onclick: () => confirmBox('Revoke key', `Revoke ${k.name}? Clients using it stop working immediately.`, async () => { await api('DELETE', '/api/proxy/keys/' + encodeURIComponent(k.name)); toast('Key revoked'); await render(true); }, 'Revoke') }, 'Revoke'))))) : [emptyRow(5, 'No API keys.')]));
    vendorBody.replaceChildren(...(cfg.vendor_keys.length ? cfg.vendor_keys.map((v) => h('tr', {},
      h('td', {}, h('span', { class: 'mono' }, v.api_key_name)), h('td', {}, v.vendor), h('td', {}, 'v' + v.key_version), h('td', {}, when(v.created_at)),
      h('td', {}, canWrite() && h('div', { class: 'actions' }, h('button', { class: 'sm danger', onclick: () => confirmBox('Remove vendor key', `Remove the ${v.vendor} credential of ${v.api_key_name}?`, async () => { await api('DELETE', `/api/proxy/vendor-keys?api_key_name=${encodeURIComponent(v.api_key_name)}&vendor=${encodeURIComponent(v.vendor)}`); toast('Credential removed'); await render(true); }, 'Remove') }, 'Remove'))))) : [emptyRow(5, 'No vendor credentials yet.')]));
    keysBody.dataset.names = keys.keys.filter((k) => !k.disabled).map((k) => k.name).join(',');
  };

  const mint = () => modal('Mint API key', h('div', { style: 'display:grid;gap:12px' },
    field('Name', h('input', { name: 'name', required: true, placeholder: 'billing-service' }), 'One key per client service'),
    h('div', {}, h('div', { class: 'note' }, 'Scopes'), h('div', { class: 'controls' }, check('generate', 'generate', true), check('admin', 'admin', false)))),
    { submit: 'Mint', onSubmit: async (fd) => {
      const scopes = ['generate', 'admin'].filter((s) => fd.get(s));
      const res = await api('POST', '/api/proxy/keys', { name: String(fd.get('name')).trim(), scopes });
      await render(true);
      modal('Copy your new key', h('div', { style: 'display:grid;gap:10px' }, h('p', {}, 'This is the only time it is shown.'), h('div', { class: 'secret mono' }, res.plain_key),
        h('button', { type: 'button', onclick: async () => { await navigator.clipboard?.writeText(res.plain_key); toast('Copied'); } }, 'Copy')));
    } });

  const setVendor = () => {
    const names = (keysBody.dataset.names || '').split(',').filter(Boolean);
    modal('Set vendor credential', h('div', { style: 'display:grid;gap:12px' },
      field('Assign to API key', h('select', { name: 'api_key_name' }, names.map((n) => h('option', { value: n }, n)))),
      field('Vendor', h('select', { name: 'vendor' }, VENDORS.map((v) => h('option', { value: v }, v)))),
      field('Vendor API key', h('input', { name: 'plain_key', type: 'password', required: true, autocomplete: 'off' }), 'Stored encrypted. It is never shown again.')),
      { onSubmit: async (fd) => { await api('PUT', '/api/proxy/vendor-keys', { api_key_name: fd.get('api_key_name'), vendor: fd.get('vendor'), plain_key: fd.get('plain_key') }); toast('Credential saved'); await render(true); } });
  };

  const rotate = () => confirmBox('Re-encrypt vendor keys', 'Re-encrypts every stored vendor credential with the newest key in LLMPROXY_ENCRYPTION_KEYS. Do this after adding a new key version.', async () => {
    const r = await api('POST', '/api/proxy/rotate'); toast(`Re-encrypted ${r.reencrypted ?? 0} credentials`);
  }, 'Re-encrypt');

  root.append(
    h('div', { class: 'head' }, h('div', {}, h('h1', {}, 'Keys'), h('p', { class: 'sub' }, 'Client API keys, and the vendor credentials each client may use.'))),
    h('div', { class: 'panel' }, h('div', { class: 'head' }, h('h2', {}, 'API keys'), canWrite() && h('button', { class: 'primary', onclick: mint }, 'Mint key')),
      h('div', { class: 'flush', style: 'overflow-x:auto' }, h('table', {}, h('thead', {}, h('tr', {}, ['Name', 'Scopes', 'Created', 'State'].map((x) => h('th', {}, x)), h('th'))), keysBody))),
    h('div', { class: 'panel' }, h('div', { class: 'head' }, h('h2', {}, 'Vendor credentials'), canWrite() && h('div', { class: 'actions' }, h('button', { onclick: rotate }, 'Re-encrypt all'), h('button', { class: 'primary', onclick: setVendor }, 'Set credential'))),
      h('div', { style: 'overflow-x:auto' }, h('table', {}, h('thead', {}, h('tr', {}, ['API key', 'Vendor', 'Key version', 'Set'].map((x) => h('th', {}, x)), h('th'))), vendorBody))));
  render();
};

// ----- playground -----
pages.playground = async (root) => {
  const cfg = await guarded(() => loadCfg());
  if (!cfg) return;
  const out = h('div');
  const model = h('select', { name: 'model' }, h('option', { value: 'auto' }, 'auto (let the rules choose)'), cfg.models.filter((m) => m.enabled).map((m) => h('option', { value: m.id }, m.id)));
  const effort = h('select', {}, ['low', 'medium', 'high'].map((e) => h('option', { value: e, selected: e === 'medium' }, e)));
  const system = h('textarea', { rows: 2, placeholder: 'Optional system instruction' });
  const prompt = h('textarea', { rows: 5, placeholder: 'Ask something…' });
  const attrs = h('input', { placeholder: 'attributes, e.g. subject=french,task=quiz' });
  const run = h('button', { class: 'primary', type: 'button', onclick: async () => {
    if (!prompt.value.trim()) return;
    run.disabled = true; out.replaceChildren(h('p', { class: 'sub' }, 'Thinking…'));
    const attributes = {};
    for (const p of attrs.value.split(',')) { const i = p.indexOf('='); if (i > 0) attributes[p.slice(0, i).trim()] = p.slice(i + 1).trim(); }
    try {
      const r = await api('POST', '/api/proxy/playground', { model: model.value, effort: effort.value, system: system.value, prompt: prompt.value, attributes });
      out.replaceChildren(h('div', { class: 'answer' }, (r.choices || []).join('\n---\n')),
        h('p', { class: 'note' }, `model ${r.resolved_model} · vendor ${r.resolved_vendor} · rule ${r.matched_rule || '(override)'} · ${r.usage?.input ?? 0} in / ${r.usage?.output ?? 0} out tokens`));
    } catch (e) { if (e.status !== 401) out.replaceChildren(h('p', { class: 'err' }, e.message)); }
    run.disabled = false;
  } }, 'Run');
  root.append(h('div', { class: 'head' }, h('div', {}, h('h1', {}, 'Playground'), h('p', { class: 'sub' }, 'Send a prompt through the same routing your clients use. Counts toward usage as console.playground.'))),
    h('div', { class: 'panel', style: 'display:grid;gap:12px' },
      h('div', { class: 'controls' }, field('Model', model), field('Effort', effort, 'Used when the model is auto'), field('Attributes', attrs)),
      field('System', system), field('Prompt', prompt), h('div', {}, run), out));
};

// ----- nodes -----
pages.nodes = async (root) => {
  const nodesBody = h('tbody'), nodeKeysBody = h('tbody');
  const render = async () => {
    const [nodes, keys] = await Promise.all([
      guarded(() => api('GET', '/api/proxy/nodes')),
      guarded(() => api('GET', '/api/proxy/node-keys')),
    ]);
    if (nodes && nodes.nodes) {
      nodesBody.replaceChildren(...(nodes.nodes.length ? nodes.nodes.map((n) => h('tr', { class: n.state !== 'NODE_STATE_HEALTHY' ? 'off' : '' },
        h('td', {}, h('span', { class: 'mono' }, n.node_id)),
        h('td', {}, n.name),
        h('td', {}, h('span', { class: 'chip ' + (n.state === 'NODE_STATE_HEALTHY' ? 'ok' : 'bad') }, n.state.replace('NODE_STATE_', ''))),
        h('td', {}, (n.models || []).join(', ')),
        h('td', { class: 'num' }, `${n.active_requests} / ${n.max_concurrent}`),
        h('td', { class: 'num' }, n.vram_total_mb ? `${Math.round(n.vram_free_mb / 1024)}G / ${Math.round(n.vram_total_mb / 1024)}G` : '—'),
        h('td', { class: 'num' }, n.tps_ema ? `${n.tps_ema.toFixed(1)} tps` : '—'),
        h('td', {}, when(n.last_heartbeat)),
        h('td', {}, canWrite() && n.state !== 'NODE_STATE_DRAINING' && n.state !== 'NODE_STATE_OFFLINE' && h('div', { class: 'actions' },
          h('button', { class: 'sm danger', onclick: () => confirmBox('Drain node', `Drain ${n.name}? It will stop accepting new requests.`, async () => {
            await api('POST', `/api/proxy/nodes/${encodeURIComponent(n.node_id)}/drain`);
            toast('Node marked for drain');
            await render();
          }, 'Drain') }, 'Drain'))))
      ) : [emptyRow(9, 'No local nodes connected.')]));
    }
    if (keys && keys.keys) {
      nodeKeysBody.replaceChildren(...(keys.keys.length ? keys.keys.map((k) => h('tr', { class: k.disabled ? 'off' : '' },
        h('td', {}, h('span', { class: 'mono' }, k.name)),
        h('td', {}, h('span', { class: 'mono' }, k.node_id)),
        h('td', {}, when(k.created_at)),
        h('td', {}, k.disabled ? 'revoked' : 'active'),
        h('td', {}, canWrite() && !k.disabled && h('div', { class: 'actions' },
          h('button', { class: 'sm danger', onclick: () => confirmBox('Revoke node key', `Revoke key for node ${k.node_id}?`, async () => {
            await api('POST', '/api/proxy/node-keys/revoke', { id: k.id });
            toast('Node key revoked');
            await render();
          }, 'Revoke') }, 'Revoke'))))
      ) : [emptyRow(5, 'No node keys minted.')]));
    }
  };

  const mint = () => modal('Mint node key', h('div', { style: 'display:grid;gap:12px' },
    field('Key Name', h('input', { name: 'name', required: true, placeholder: 'desktop-rtx4090' })),
    field('Node ID', h('input', { name: 'node_id', required: true, placeholder: 'worker-1' }), 'Bound to this specific node ID'),
    field('Allowed CIDRs', h('input', { name: 'allowed_cidrs', placeholder: 'comma separated, optional' })),
    field('Allowed Models', h('input', { name: 'allowed_models', placeholder: 'comma separated, optional' })),
    h('div', { class: 'row' },
      field('Max Base Weight', h('input', { name: 'max_base_weight', type: 'number', value: '1000' })),
      field('Max Concurrency', h('input', { name: 'max_concurrency', type: 'number', value: '16' }))
    )), {
    submit: 'Mint',
    onSubmit: async (fd) => {
      const cidrs = String(fd.get('allowed_cidrs') || '').split(',').map((s) => s.trim()).filter(Boolean);
      const models = String(fd.get('allowed_models') || '').split(',').map((s) => s.trim()).filter(Boolean);
      const res = await api('POST', '/api/proxy/node-keys', {
        name: String(fd.get('name')).trim(),
        node_id: String(fd.get('node_id')).trim(),
        allowed_cidrs: cidrs,
        allowed_models: models,
        max_base_weight: num(fd.get('max_base_weight')) || 1000,
        max_concurrency: num(fd.get('max_concurrency')) || 16,
      });
      await render();
      modal('Copy node authentication key', h('div', { style: 'display:grid;gap:10px' },
        h('p', {}, 'Pass this to the node via NODE_KEY. This is the only time it is shown:'),
        h('div', { class: 'secret mono' }, res.plain_key),
        h('button', { type: 'button', onclick: async () => { await navigator.clipboard?.writeText(res.plain_key); toast('Copied'); } }, 'Copy')
      ));
    }
  });

  root.append(
    h('div', { class: 'head' },
      h('div', {}, h('h1', {}, 'Local Nodes'), h('p', { class: 'sub' }, 'Self-registering inference machines and their authentication keys.'))),
    h('div', { class: 'panel' },
      h('div', { class: 'head' }, h('h2', {}, 'Active Nodes'), h('button', { onclick: render }, 'Refresh')),
      h('div', { style: 'overflow-x:auto' }, h('table', {},
        h('thead', {}, h('tr', {}, ['Node ID', 'Name', 'State', 'Models', 'Slots', 'VRAM', 'Speed', 'Heartbeat'].map((x) => h('th', {}, x)), h('th'))),
        nodesBody))),
    h('div', { class: 'panel' },
      h('div', { class: 'head' }, h('h2', {}, 'Node Keys'), canWrite() && h('button', { class: 'primary', onclick: mint }, 'Mint node key')),
      h('div', { style: 'overflow-x:auto' }, h('table', {},
        h('thead', {}, h('tr', {}, ['Name', 'Node ID', 'Created', 'State'].map((x) => h('th', {}, x)), h('th'))),
        nodeKeysBody)))
  );
  render();
};

// ---------- shell ----------
function route() {
  const name = (location.hash.replace(/^#\/?/, '') || defaultPage()).split('/')[0];
  return pages[name] && available(name) ? name : defaultPage();
}
const available = (n) => (n === 'usage' ? S.me.features.usage : S.me.features.proxy);
const defaultPage = () => (S.me.features.usage ? 'usage' : 'models');

function shell() {
  const cur = route();
  const link = (id, label) => h('a', { href: '#/' + id, class: id === cur ? 'on' : '' }, label);
  const f = S.me.features;
  const chip = (label, st) => st?.enabled && h('span', { class: 'chip', title: st.error || 'reachable' }, h('span', { class: 'dot ' + (st.ok ? 'ok' : 'bad') }), label);
  const statusBox = h('div', { class: 'status', id: 'status' });
  const paintStatus = () => statusBox.replaceChildren(chip('llm-proxy', S.health?.proxy) || '', chip('tokenmeter', S.health?.usage) || '');
  paintStatus(); S.paintStatus = () => document.getElementById('status') && (statusBox.isConnected ? paintStatus() : null);
  const main = h('main', { class: 'main' });
  const u = S.me.user;
  const app = document.getElementById('app');
  app.replaceChildren(h('div', { class: 'shell' },
    h('nav', { class: 'side' }, h('div', { class: 'brand' }, h('i'), 'llm-proxy admin'),
      h('div', { class: 'nav' },
        f.usage && [h('div', { class: 'grp' }, 'Spend'), link('usage', 'Usage')],
        f.proxy && [h('div', { class: 'grp' }, 'Gateway'), link('models', 'Models'), link('rules', 'Rules'), link('keys', 'Keys'), link('nodes', 'Nodes'), link('playground', 'Playground')]),
      h('div', { class: 'spacer' }),
      statusBox,
      h('div', { class: 'who' }, u.picture && h('img', { src: u.picture, alt: '', referrerpolicy: 'no-referrer' }),
        h('span', { class: 'name', title: u.email }, u.name || u.email), u.role === 'viewer' && h('span', { class: 'chip' }, 'read-only'),
        h('button', { class: 'sm', onclick: async () => { await fetch('/auth/logout', { method: 'POST', headers: { 'X-Requested-With': 'llm-proxy-admin' } }); location.href = '/'; } }, 'Sign out'))),
    main));
  Promise.resolve(pages[cur](main)).catch((e) => toast(e.message, true));
}

async function boot() {
  try {
    S.me = await api('GET', '/api/me');
    shell();
    api('GET', '/api/health').then((h2) => { S.health = h2; S.paintStatus?.(); }).catch(() => {});
    window.addEventListener('hashchange', shell);
  } catch (e) {
    if (e.status !== 401) document.getElementById('app').replaceChildren(h('p', { class: 'boot err' }, 'Cannot load the console: ' + e.message));
  }
}
boot();
