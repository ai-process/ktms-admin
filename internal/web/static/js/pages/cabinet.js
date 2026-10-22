import { html, api, useAsync, confirmAction, PageHead, Card, Table, Tile, Badge, Meter, LoadError, Empty, when, usd, pct, num } from '../lib.js';
import { session, isAdmin, isTeamLead, canReadGlobal } from '../state.js';

export function CabinetPage({ params }) {
  const teamId = params.get('team_id') || session.me?.user?.team_id;
  const st = useAsync(() => (teamId ? api('GET', `/api/proxy/teams/${encodeURIComponent(teamId)}/cabinet`) : null), [teamId]);
  if (!teamId) {
    return html`<${PageHead} title="Team" />
      <div class="card"><${Empty} title="No team selected">${canReadGlobal() ? html`Pick a team on the <a href="#/tenants">Tenants</a> page.` : 'Your account is not in a team yet: ask an administrator.'}<//></div>`;
  }
  const c = st.data;
  const t = c?.team || {};
  const keys = c?.keys || [];
  // Spend is the gateway's own counter; when it is unknown the page says so
  // rather than showing the whole budget as available.
  const known = c?.metered === true;
  const budget = num(t.monthly_budget_usd);
  const spend = num(c?.current_spend_usd);
  const capped = known && c?.remaining_usd !== undefined;
  const share = known && budget > 0 ? spend / budget : 0;
  const exhausted = capped && num(c.remaining_usd) <= 0;
  const unknown = c?.spend_error ? 'Spend counters are unavailable right now.' : budget > 0 ? 'Not metered: the gateway runs without Redis.' : 'Counted only while a budget is set.';
  const canManage = (isTeamLead() && session.me.user.team_id === teamId) || isAdmin();

  const revoke = (k) => confirmAction({
    title: `Revoke ${k.name}?`, confirm: 'Revoke', text: 'Clients using it are refused at once.',
    action: () => api('DELETE', '/api/proxy/keys/' + encodeURIComponent(k.name)),
  }).then((ok) => ok && st.reload());
  const rows = keys.map((k) => html`<tr key=${k.name} class=${k.disabled ? 'off' : ''}>
    <td><span class="mono">${k.name}</span></td>
    <td><div class="cluster">${(k.scopes || []).map((s) => html`<span class="chip" key=${s}>${s}</span>`)}</div></td>
    <td>${when(k.created_at)}</td>
    <td>${k.disabled ? html`<${Badge}>Revoked<//>` : html`<${Badge} tone="ok">Active<//>`}</td>
    <td class="actions-cell">${canManage && !k.disabled && html`<button type="button" class="sm danger" onClick=${() => revoke(k)}>Revoke</button>`}</td>
  </tr>`);

  return html`
    <${PageHead} title=${t.name || 'Team'} sub=${t.id ? html`Team <span class="mono">${t.id}</span>` : ''}>
      ${canManage && html`<a class="btn primary" href=${'#/keys?team_id=' + encodeURIComponent(teamId)}>Create a key</a>`}
    <//>
    <${LoadError} error=${st.error} retry=${st.reload} />
    <section class="tiles" aria-label="Budget">
      <${Tile} k="Monthly budget" v=${c ? (budget > 0 ? usd(budget) : 'No cap') : '…'} s=${budget > 0 ? 'All team keys together' : 'Spend is not limited'} />
      <${Tile} k="Spent this month" v=${c ? (known ? usd(spend) : '—') : '…'} s=${c && (known ? (budget > 0 ? pct(share) + ' of the budget' : '') : unknown)} />
      <${Tile} k="Remaining" v=${c ? (budget > 0 ? (capped ? usd(c.remaining_usd) : '—') : 'Unlimited') : '…'} s=${c && (exhausted ? 'Budget used up: calls are refused until the month resets.' : capped ? 'Resets ' + new Date(c.resets_at).toLocaleDateString() : '')} />
      <${Tile} k="Active keys" v=${c ? keys.filter((k) => !k.disabled).length : '…'} s=${c ? `${keys.length} minted in total` : ''} />
    </section>
    ${budget > 0 && known && html`<${Card} title="Budget used"><${Meter} value=${share} tone=${exhausted ? 'bad' : share > 0.8 ? 'warn' : ''} /><//>`}
    <${Card} title="Allowed models" sub="Rules skip any model outside this list for the team's keys.">
      ${(t.allowed_models || []).length
        ? html`<div class="cluster">${t.allowed_models.map((m) => html`<span class="chip mono" key=${m}>${m}</span>`)}</div>`
        : html`<p class="muted">Every model in the catalog.</p>`}
    <//>
    <${Card} title="Team keys" flush>
      <${Table} loading=${st.loading} rows=${rows} head=${['Name', 'Access', 'Created', 'Status', { label: 'Actions', sr: true }]}
        empty=${{ title: 'No keys yet', children: canManage ? 'Create one on the Keys page.' : 'Ask your team lead for one.' }} />
    <//>
  `;
}
