// KTMS admin console: shell, navigation and hash routing.
import { html, render, useState, useEffect, api, Icon, Toasts, cx } from './lib.js';
import { session, isAdmin, canReadGlobal, inTeam, hasProxy, hasUsage } from './state.js';
import { UsagePage } from './pages/usage.js';
import { ModelsPage } from './pages/models.js';
import { RulesPage } from './pages/rules.js';
import { LimitsPage } from './pages/limits.js';
import { KeysPage } from './pages/keys.js';
import { NodesPage } from './pages/nodes.js';
import { TenantsPage } from './pages/tenants.js';
import { CabinetPage } from './pages/cabinet.js';
import { PlaygroundPage } from './pages/playground.js';
import { JudgePage } from './pages/judge.js';

// Each page declares who may open it; the server enforces the same split.
const PAGES = {
  usage: { title: 'Usage', icon: 'usage', comp: UsagePage, ok: () => hasUsage() && canReadGlobal() },
  cabinet: { title: 'Team', icon: 'cabinet', comp: CabinetPage, ok: () => hasProxy() && (inTeam() || canReadGlobal()) },
  tenants: { title: 'Tenants', icon: 'tenants', comp: TenantsPage, ok: () => hasProxy() && canReadGlobal() },
  models: { title: 'Models', icon: 'models', comp: ModelsPage, ok: () => hasProxy() && canReadGlobal() },
  rules: { title: 'Rules', icon: 'rules', comp: RulesPage, ok: () => hasProxy() && canReadGlobal() },
  limits: { title: 'Limits', icon: 'limits', comp: LimitsPage, ok: () => hasProxy() && canReadGlobal() },
  keys: { title: 'Keys', icon: 'keys', comp: KeysPage, ok: () => hasProxy() },
  nodes: { title: 'Nodes', icon: 'nodes', comp: NodesPage, ok: () => hasProxy() && canReadGlobal() },
  // Run with the console's own key, outside any team's models and budget.
  playground: { title: 'Playground', icon: 'playground', comp: PlaygroundPage, ok: () => hasProxy() && isAdmin() },
  judge: { title: 'Judge', icon: 'judge', comp: JudgePage, ok: () => hasProxy() && isAdmin() },
};

const GROUPS = () => inTeam()
  ? [['My team', ['cabinet', 'keys']]]
  : [['Spend', ['usage']], ['Tenancy', ['tenants']], ['Gateway', ['models', 'rules', 'limits', 'keys', 'nodes']], ['Tools', ['playground', 'judge']]];

function defaultPage() {
  const order = inTeam() ? ['cabinet', 'keys'] : ['usage', 'models', 'keys', 'cabinet'];
  return order.find((n) => PAGES[n].ok()) || null;
}

function parseHash() {
  const raw = location.hash.replace(/^#\/?/, '');
  const [path, query] = raw.split('?');
  return { name: path.split('/')[0], params: new URLSearchParams(query || '') };
}

function useRoute() {
  const [r, setR] = useState(parseHash);
  useEffect(() => {
    const on = () => setR(parseHash());
    addEventListener('hashchange', on);
    return () => removeEventListener('hashchange', on);
  }, []);
  return r;
}

function ThemeToggle() {
  const read = () => { try { return localStorage.getItem('ktms-theme') || ''; } catch { return ''; } };
  const [t, setT] = useState(read);
  useEffect(() => {
    if (t) document.documentElement.dataset.theme = t; else delete document.documentElement.dataset.theme;
    try { if (t) localStorage.setItem('ktms-theme', t); else localStorage.removeItem('ktms-theme'); } catch { /* private mode */ }
  }, [t]);
  const dark = t ? t === 'dark' : matchMedia('(prefers-color-scheme: dark)').matches;
  return html`<button type="button" class="ghost icon" title=${dark ? 'Light theme' : 'Dark theme'} aria-label=${dark ? 'Switch to light theme' : 'Switch to dark theme'} onClick=${() => setT(dark ? 'light' : 'dark')}><${Icon} name=${dark ? 'sun' : 'moon'} /></button>`;
}

function Health() {
  const [h, setH] = useState(session.health);
  useEffect(() => { api('GET', '/api/health').then((x) => { session.health = x; setH(x); }).catch(() => {}); }, []);
  const row = (label, st) => st?.enabled && html`<div title=${st.error || 'reachable'}><span class=${cx('dot', st.ok ? 'ok' : 'bad')}></span>${label}${!st.ok && html`<span class="small err-text">down</span>`}</div>`;
  if (!h || (!h.proxy?.enabled && !h.usage?.enabled)) return null;
  return html`<div class="health" aria-label="Backends">${row('KTMS gateway', h.proxy)}${row('Tokenmeter', h.usage)}</div>`;
}

function RoleBadge({ role }) {
  const tone = { admin: 'accent', teamlead: 'ok', viewer: '', member: '' }[role] || '';
  return html`<span class=${cx('badge', tone)}>${role || 'member'}</span>`;
}

function Shell() {
  const route = useRoute();
  const [open, setOpen] = useState(false);
  const fallback = defaultPage();
  const name = PAGES[route.name]?.ok() ? route.name : fallback;
  useEffect(() => { setOpen(false); }, [route.name]);
  useEffect(() => { document.title = (name ? PAGES[name].title + ' · ' : '') + 'KTMS admin'; }, [name]);
  const u = session.me.user;
  const Page = name && PAGES[name].comp;
  const signOut = async () => { await fetch('/auth/logout', { method: 'POST', headers: { 'X-Requested-With': 'llm-proxy-admin' } }); location.href = '/'; };

  return html`<div class="shell">
    <nav class=${cx('side', open && 'open')} aria-label="Main">
      <div class="brand"><div class="logo"><${Icon} name="logo" /></div><div><b>KTMS</b><span>Admin console</span></div></div>
      <button type="button" class="ghost icon menu-btn" aria-expanded=${open ? 'true' : 'false'} aria-label="Menu" onClick=${() => setOpen(!open)}><${Icon} name="menu" /></button>
      <div class="collapsible stack" style=${{ gap: '22px' }}>
        ${GROUPS().map(([label, items]) => {
          const links = items.filter((n) => PAGES[n].ok());
          return links.length > 0 && html`<div class="navgrp" key=${label}><div class="label">${label}</div>
            ${links.map((n) => html`<a key=${n} href=${'#/' + n} aria-current=${n === name ? 'page' : undefined}><${Icon} name=${PAGES[n].icon} />${n === 'cabinet' && canReadGlobal() ? 'Team cabinet' : PAGES[n].title}</a>`)}
          </div>`;
        })}
      </div>
      <div class="grow"></div>
      <div class="collapsible stack">
        <${Health} />
        <div class="who">
          ${u.picture ? html`<img src=${u.picture} alt="" referrerpolicy="no-referrer" onError=${(e) => { e.currentTarget.hidden = true; }} />` : html`<span class="avatar">${(u.name || u.email || '?').slice(0, 1).toUpperCase()}</span>`}
          <div class="id"><b title=${u.email}>${u.name || u.email}</b><${RoleBadge} role=${u.role} /></div>
          <${ThemeToggle} />
        </div>
        ${!u.proxied && html`<button type="button" class="sm" onClick=${signOut}>Sign out</button>`}
      </div>
    </nav>
    <main class="main" id="main">
      ${Page ? html`<${Page} key=${name + location.hash} params=${route.params} />` : html`<div class="card"><div class="card-body"><b>Nothing to show</b><p class="muted">No backend this account can use is configured.</p></div></div>`}
    </main>
  </div>`;
}

function App() { return html`<${Shell} /><${Toasts} />`; }

async function boot() {
  const root = document.getElementById('app');
  try {
    session.me = await api('GET', '/api/me');
    root.textContent = '';
    render(html`<${App} />`, root);
  } catch (e) {
    if (e.status !== 401) root.textContent = 'Cannot load the console: ' + e.message;
  }
}
boot();
