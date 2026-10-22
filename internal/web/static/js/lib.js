// Shared UI kit. htm templates compile to Preact vnodes, which only ever set
// text and properties, so API data can never inject markup.
import { h, render, Fragment } from '../vendor/preact.mjs';
import { useState, useEffect, useRef, useMemo, useCallback } from '../vendor/hooks.mjs';
import htm from '../vendor/htm.mjs';

export const html = htm.bind(h);
export { h, render, Fragment, useState, useEffect, useRef, useMemo, useCallback };

// ---------- API ----------
export class ApiError extends Error {
  constructor(msg, status, code) { super(msg); this.status = status; this.code = code; }
}

const CSRF = { 'X-Requested-With': 'ktms-admin' };

export async function api(method, path, body, signal) {
  const opts = { method, headers: { ...CSRF }, credentials: 'same-origin', signal };
  if (body !== undefined) { opts.headers['Content-Type'] = 'application/json'; opts.body = JSON.stringify(body); }
  const res = await fetch(path, opts);
  if (res.status === 401) {
    location.href = '/auth/login?next=' + encodeURIComponent(location.pathname + location.hash);
    throw new ApiError('Signed out', 401);
  }
  const text = await res.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch { /* not JSON */ }
  if (!res.ok) throw new ApiError(friendly(res.status, data?.error?.message), res.status, data?.error?.code);
  return data;
}

export async function apiStream(path, body, signal) {
  const res = await fetch(path, { method: 'POST', headers: { ...CSRF, 'Content-Type': 'application/json' }, body: JSON.stringify(body), signal, credentials: 'same-origin' });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new ApiError(friendly(res.status, data?.error?.message), res.status, data?.error?.code);
  }
  return res;
}

// The gateway's messages are already sentences; status codes only explain
// what kind of failure it was when there is no message at all.
const KNOWN = {
  no_vendor_key: 'No vendor credential covers the chosen models for this key: add one on the Keys page.',
  no_capable_model: 'No enabled model in the matching rule can serve this request.',
  not_configured: 'The gateway has no rules yet: add models and a catch-all rule first.',
  vendors_unavailable: 'Every vendor in the chain failed or is paused: try again shortly.',
};

function friendly(status, msg) {
  if (msg) {
    msg = msg.replace(/^rpc error: code = \w+ desc = /, '');
    const code = msg.split(':')[0].trim();
    return KNOWN[code] ? `${KNOWN[code]} (${msg})` : msg;
  }
  return ({
    400: 'The request was not valid.',
    403: 'Your account may not do this.',
    404: 'Not found.',
    429: 'Too many requests: try again shortly.',
    502: 'The gateway refused the console.',
    503: 'The gateway is unreachable.',
  })[status] || `Request failed (HTTP ${status}).`;
}

// ---------- formatting ----------
const nf = new Intl.NumberFormat('en-US');
const cf = new Intl.NumberFormat('en-US', { notation: 'compact', maximumFractionDigits: 1 });
export const num = (v) => { const n = Number(v); return Number.isFinite(n) ? n : 0; };
export const fmt = (n) => nf.format(Math.round(num(n)));
export const compact = (n) => cf.format(num(n));
export const pct = (x) => { const p = num(x) * 100; return (p >= 10 || p === 0 ? p.toFixed(0) : p.toFixed(1)) + '%'; };
export const usd = (x) => {
  if (x == null) return '—';
  const v = num(x);
  if (v === 0) return '$0';
  if (Math.abs(v) < 0.01) return '<$0.01';
  return '$' + (Math.abs(v) >= 1000 ? nf.format(Math.round(v)) : v.toFixed(2));
};
export const when = (ts) => (ts ? new Date(ts).toLocaleString() : '—');
export const ago = (ts) => {
  if (!ts) return '—';
  const s = Math.round((Date.now() - new Date(ts).getTime()) / 1000);
  if (s < 60) return 'just now';
  if (s < 3600) return Math.round(s / 60) + ' min ago';
  if (s < 86400) return Math.round(s / 3600) + ' h ago';
  return Math.round(s / 86400) + ' d ago';
};
export const EFFORTS = [['EFFORT_UNSPECIFIED', 'any'], ['EFFORT_LOW', 'low'], ['EFFORT_MEDIUM', 'medium'], ['EFFORT_HIGH', 'high']];
export const effortName = (e) => (Object.fromEntries(EFFORTS)[e] || 'any');
export const splitList = (s) => String(s || '').split(/[,\n]/).map((x) => x.trim()).filter(Boolean);
export const parseAttrs = (s) => {
  const out = {};
  for (const p of splitList(s)) {
    const i = p.indexOf('=');
    if (i > 0) out[p.slice(0, i).trim()] = p.slice(i + 1).trim();
  }
  return out;
};
export const cx = (...xs) => xs.filter(Boolean).join(' ');

// ---------- toasts ----------
let toastSet = null;
let toastSeq = 0;
export function toast(msg, bad) {
  if (!toastSet) return;
  const id = ++toastSeq;
  toastSet((ts) => [...ts, { id, msg, bad }]);
  setTimeout(() => toastSet((ts) => ts.filter((t) => t.id !== id)), bad ? 7000 : 3500);
}
export function Toasts() {
  const [ts, set] = useState([]);
  toastSet = set;
  return html`<div class="toasts" role="status" aria-live="polite">
    ${ts.map((t) => html`<div key=${t.id} class=${cx('toast', t.bad && 'bad')}>${t.msg}</div>`)}
  </div>`;
}

// ---------- dialogs ----------
// openDialog mounts a component into its own <dialog>; the component gets
// close(result) and the promise resolves with that result.
export function openDialog(Comp, props = {}) {
  return new Promise((resolve) => {
    const host = document.createElement('div');
    document.body.append(host);
    const close = (result) => { render(null, host); host.remove(); resolve(result); };
    render(html`<${DialogFrame} close=${close} wide=${props.wide}><${Comp} ...${props} close=${close} /><//>`, host);
  });
}

function DialogFrame({ close, wide, children }) {
  const ref = useRef();
  useEffect(() => {
    const d = ref.current;
    d.showModal();
    const onCancel = (e) => { e.preventDefault(); close(undefined); };
    d.addEventListener('cancel', onCancel);
    return () => d.removeEventListener('cancel', onCancel);
  }, []);
  return html`<dialog ref=${ref} class=${wide ? 'wide' : ''}>${children}</dialog>`;
}

// FormDialog is the standard form shell: title, body, error line, footer.
export function FormDialog({ title, sub, submit = 'Save', danger, onSubmit, close, children, cancel = 'Cancel', foot }) {
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);
  const submitIt = async (e) => {
    e.preventDefault();
    if (!onSubmit) return close();
    setErr(''); setBusy(true);
    try {
      const r = await onSubmit(new FormData(e.currentTarget));
      if (r !== false) close(r === undefined ? true : r);
    } catch (ex) {
      if (ex.status !== 401) setErr(ex.message);
    } finally { setBusy(false); }
  };
  return html`<form method="dialog" onSubmit=${submitIt}>
    <div class="dlg-head"><h2>${title}</h2>${sub && html`<p>${sub}</p>`}</div>
    <div class="dlg-body">${children}${err && html`<div class="notice bad" role="alert">${err}</div>`}</div>
    <div class="dlg-foot">
      ${foot}
      <button type="button" onClick=${() => close()}>${onSubmit ? cancel : 'Close'}</button>
      ${onSubmit && html`<button type="submit" class=${danger ? 'danger solid' : 'primary'} disabled=${busy}>${busy ? 'Working…' : submit}</button>`}
    </div>
  </form>`;
}

function ConfirmBody({ title, text, confirm, danger, action, close }) {
  return html`<${FormDialog} title=${title} submit=${confirm} danger=${danger} close=${close} onSubmit=${async () => { await action(); }}>
    <p>${text}</p>
  <//>`;
}

// confirmAction asks before a destructive or irreversible step.
export const confirmAction = (opts) => openDialog(ConfirmBody, { confirm: 'Delete', danger: true, ...opts });

function SecretBody({ title, text, secret, close }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => { try { await navigator.clipboard.writeText(secret); setCopied(true); } catch { toast('Copy failed: select the key and copy it by hand', true); } };
  return html`<${FormDialog} title=${title} close=${close}>
    <div class="notice ok">${text}</div>
    <div class="secret"><code>${secret}</code><button type="button" class="sm" onClick=${copy}>${copied ? 'Copied' : 'Copy'}</button></div>
  <//>`;
}
export const showSecret = (title, text, secret) => openDialog(SecretBody, { title, text, secret });

// ---------- small components ----------
export function Field({ label, hint, error, children, class: klass }) {
  return html`<label class=${cx('field', klass)}>${label}${children}${error ? html`<span class="error">${error}</span>` : hint && html`<span class="hint">${hint}</span>`}</label>`;
}

export function Check({ label, ...rest }) {
  return html`<label class="field inline"><input type="checkbox" ...${rest} />${label}</label>`;
}

export function Seg({ options, value, onChange, label, dark }) {
  return html`<div class=${cx('seg', dark && 'dark')} role="group" aria-label=${label}>
    ${options.map(([v, l]) => html`<button type="button" key=${v} aria-pressed=${v === value ? 'true' : 'false'} onClick=${() => onChange(v)}>${l}</button>`)}
  </div>`;
}

export function PageHead({ title, sub, badge, children }) {
  return html`<header class="page-head">
    <div class="titles"><div class="cluster"><h1>${title}</h1>${badge}</div>${sub && html`<p>${sub}</p>`}</div>
    ${children && html`<div class="actions">${children}</div>`}
  </header>`;
}

export function Card({ title, sub, actions, children, flush, foot, label }) {
  return html`<section class="card" aria-label=${label || title}>
    ${(title || actions) && html`<div class="card-head"><div class="stack" style=${{ gap: '2px' }}>${title && html`<h2>${title}</h2>`}${sub && html`<span class="sub">${sub}</span>`}</div>${actions && html`<div class="cluster">${actions}</div>`}</div>`}
    ${flush ? children : html`<div class="card-body">${children}</div>`}
    ${foot && html`<div class="card-foot">${foot}</div>`}
  </section>`;
}

export const Tile = ({ k, v, s }) => html`<div class="tile"><div class="k">${k}</div><div class="v">${v}</div>${s && html`<div class="s">${s}</div>`}</div>`;

export function Empty({ title, children, action }) {
  return html`<div class="empty">${title && html`<b>${title}</b>`}${children && html`<p>${children}</p>`}${action}</div>`;
}

export function SkeletonRows({ cols, rows = 4 }) {
  return Array.from({ length: rows }, (_, i) => html`<tr key=${i}>${Array.from({ length: cols }, (_, j) => html`<td key=${j}><div class="skeleton" style=${{ width: j === 0 ? '70%' : '50%' }}></div></td>`)}</tr>`);
}

export function Table({ head, rows, loading, empty, cols }) {
  const n = cols || head.length;
  return html`<div class="table-wrap"><table>
    <thead><tr>${head.map((c) => (typeof c === 'string' ? html`<th>${c}</th>` : html`<th class=${c.num ? 'num' : ''}><span class=${c.sr ? 'sr-only' : ''}>${c.label}</span></th>`))}</tr></thead>
    <tbody>${loading ? html`<${SkeletonRows} cols=${n} />` : rows.length ? rows : html`<tr><td colspan=${n}><${Empty} ...${empty} /></td></tr>`}</tbody>
  </table></div>`;
}

export function Meter({ value, tone }) {
  const w = Math.max(value > 0 ? 1 : 0, Math.min(100, value * 100));
  return html`<div class="meter" role="meter" aria-valuemin="0" aria-valuemax="100" aria-valuenow=${Math.round(w)}><div class=${tone || ''} style=${{ width: w + '%' }}></div></div>`;
}

export const Badge = ({ tone, children, title }) => html`<span class=${cx('badge', tone)} title=${title}>${children}</span>`;

// useAsync runs a loader on mount and whenever deps change; reload re-runs it.
export function useAsync(fn, deps = []) {
  const [st, setSt] = useState({ loading: true, data: undefined, error: undefined });
  const [n, setN] = useState(0);
  useEffect(() => {
    let live = true;
    setSt((s) => ({ ...s, loading: true }));
    Promise.resolve().then(fn).then(
      (data) => live && setSt({ loading: false, data, error: undefined }),
      (error) => { if (!live) return; if (error.status !== 401) setSt({ loading: false, data: undefined, error }); },
    );
    return () => { live = false; };
  }, [...deps, n]);
  const reload = useCallback(() => setN((x) => x + 1), []);
  return { ...st, reload };
}

export function LoadError({ error, retry }) {
  if (!error) return null;
  return html`<div class="notice bad cluster" role="alert"><span>${error.message}</span>${retry && html`<button type="button" class="sm" onClick=${retry}>Try again</button>`}</div>`;
}

// run wraps a mutation: toasts the result or the error, then refreshes.
export async function run(fn, ok, after) {
  try {
    const r = await fn();
    if (ok) toast(ok);
    if (after) await after(r);
    return r;
  } catch (e) {
    if (e.status !== 401) toast(e.message, true);
    return undefined;
  }
}

// ---------- icons ----------
const P = {
  usage: 'M4 20V10M10 20V4M16 20v-7M22 20H2',
  tenants: 'M3 21V7l9-4 9 4v14M9 21v-6h6v6',
  cabinet: 'M4 21v-2a6 6 0 0 1 12 0v2M10 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8M20 21v-2a5 5 0 0 0-3-4.6',
  models: 'M12 2 3 7l9 5 9-5-9-5zM3 12l9 5 9-5M3 17l9 5 9-5',
  rules: 'M4 6h10M4 12h16M4 18h7M17 3l3 3-3 3',
  limits: 'M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM12 7v5l3 2',
  keys: 'M15 7a4 4 0 1 1-3.9 4.9L3 20v-3h3v-3h3l2.1-2.1A4 4 0 0 1 15 7z',
  nodes: 'M4 4h16v6H4zM4 14h16v6H4zM8 7h.01M8 17h.01',
  playground: 'M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z',
  judge: 'M12 3v18M5 7h14M5 7l-3 7a4 4 0 0 0 6 0zM19 7l-3 7a4 4 0 0 0 6 0z',
  logo: 'M4 7h10M4 17h16M14 7l3-3M14 7l3 3',
  menu: 'M3 6h18M3 12h18M3 18h18',
  sun: 'M12 17a5 5 0 1 0 0-10 5 5 0 0 0 0 10zM12 1v2M12 21v2M4.2 4.2l1.4 1.4M18.4 18.4l1.4 1.4M1 12h2M21 12h2M4.2 19.8l1.4-1.4M18.4 5.6l1.4-1.4',
  moon: 'M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z',
  grip: 'M9 5h.01M9 12h.01M9 19h.01M15 5h.01M15 12h.01M15 19h.01',
  check: 'M20 6 9 17l-5-5',
  up: 'M12 19V5M5 12l7-7 7 7',
  down: 'M12 5v14M19 12l-7 7-7-7',
  x: 'M18 6 6 18M6 6l12 12',
  refresh: 'M21 12a9 9 0 1 1-2.6-6.4L21 8M21 3v5h-5',
};
export const Icon = ({ name, size = 16 }) => html`<svg width=${size} height=${size} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d=${P[name]} /></svg>`;
