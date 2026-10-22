import { html, useState, api, useAsync, openDialog, confirmAction, toast, FormDialog, PageHead, Card, Table, Field, Tile, LoadError, when, usd, fmt, num } from '../lib.js';
import { isAdmin, loadCfg } from '../state.js';

export function TenantsPage() {
  const st = useAsync(async () => {
    const [orgs, teams, cfg] = await Promise.all([api('GET', '/api/proxy/organizations'), api('GET', '/api/proxy/teams'), loadCfg().catch(() => null)]);
    return { orgs: orgs.organizations || [], teams: teams.teams || [], models: (cfg?.models || []).map((m) => m.id) };
  }, []);
  const orgs = st.data?.orgs || [];
  const teams = st.data?.teams || [];
  const orgName = Object.fromEntries(orgs.map((o) => [o.id, o.name]));
  const budget = teams.reduce((a, t) => a + num(t.monthly_budget_usd), 0);

  const addOrg = () => openDialog(OrgForm).then((ok) => ok && st.reload());
  const editTeam = (t) => (orgs.length ? openDialog(TeamForm, { team: t, orgs, models: st.data.models }).then((ok) => ok && st.reload()) : toast('Create an organization first', true));
  const delTeam = (t) => confirmAction({
    title: `Delete team ${t.name}?`, text: 'Revoked keys are detached; the gateway refuses while active keys remain, so revoke or move them first.',
    action: () => api('DELETE', '/api/proxy/teams/' + encodeURIComponent(t.id)),
  }).then((ok) => ok && st.reload());
  const delOrg = (o) => confirmAction({
    title: `Delete ${o.name}?`, text: 'Its teams go with it. The gateway refuses while any of its keys is still active.',
    action: () => api('DELETE', '/api/proxy/organizations/' + encodeURIComponent(o.id)),
  }).then((ok) => ok && st.reload());

  const teamRows = teams.map((t) => html`<tr key=${t.id}>
    <td><div class="stacked"><a href=${'#/cabinet?team_id=' + encodeURIComponent(t.id)} class="bold">${t.name}</a><span class="small mono">${t.id}</span></div></td>
    <td>${orgName[t.org_id] || html`<span class="mono">${t.org_id}</span>`}</td>
    <td class="num">${num(t.monthly_budget_usd) > 0 ? usd(t.monthly_budget_usd) : html`<span class="muted">no cap</span>`}</td>
    <td>${(t.allowed_models || []).length ? html`<div class="cluster">${t.allowed_models.map((m) => html`<span class="chip mono" key=${m}>${m}</span>`)}</div>` : html`<span class="muted">every model</span>`}</td>
    <td>${when(t.created_at)}</td>
    <td class="actions-cell"><div class="cluster">
      <a class="btn sm" href=${'#/cabinet?team_id=' + encodeURIComponent(t.id)}>Open</a>
      ${isAdmin() && html`<button type="button" class="sm" onClick=${() => editTeam(t)}>Edit</button><button type="button" class="sm danger" onClick=${() => delTeam(t)}>Delete</button>`}
    </div></td>
  </tr>`);
  const orgRows = orgs.map((o) => {
    const own = teams.filter((t) => t.org_id === o.id);
    return html`<tr key=${o.id}>
      <td><div class="stacked"><span class="bold">${o.name}</span><span class="small mono">${o.id}</span></div></td>
      <td class="num">${fmt(own.length)}</td>
      <td class="num">${usd(own.reduce((a, t) => a + num(t.monthly_budget_usd), 0))}</td>
      <td>${when(o.created_at)}</td>
      <td class="actions-cell">${isAdmin() && html`<button type="button" class="sm danger" onClick=${() => delOrg(o)}>Delete</button>`}</td>
    </tr>`;
  });

  return html`
    <${PageHead} title="Tenants" sub="Organizations group teams; a team's keys share its model allowlist and monthly budget.">
      ${isAdmin() && html`<button type="button" onClick=${addOrg}>Add organization</button><button type="button" class="primary" onClick=${() => editTeam(null)}>Add team</button>`}
    <//>
    <${LoadError} error=${st.error} retry=${st.reload} />
    <section class="tiles" aria-label="Totals">
      <${Tile} k="Organizations" v=${st.data ? fmt(orgs.length) : '…'} />
      <${Tile} k="Teams" v=${st.data ? fmt(teams.length) : '…'} />
      <${Tile} k="Monthly budgets" v=${st.data ? usd(budget) : '…'} s="Sum of team caps" />
    </section>
    <${Card} title="Teams" flush>
      <${Table} loading=${st.loading} rows=${teamRows} head=${['Team', 'Organization', { label: 'Monthly budget', num: true }, 'Allowed models', 'Created', { label: 'Actions', sr: true }]}
        empty=${{ title: 'No teams yet', children: orgs.length ? 'Add a team to give a group of keys its own models and budget.' : 'Create an organization, then add teams to it.' }} />
    <//>
    <${Card} title="Organizations" flush>
      <${Table} loading=${st.loading} rows=${orgRows} head=${['Organization', { label: 'Teams', num: true }, { label: 'Budgets', num: true }, 'Created', { label: 'Actions', sr: true }]}
        empty=${{ title: 'No organizations yet' }} />
    <//>
  `;
}

function OrgForm({ close }) {
  const submit = async (fd) => {
    const name = String(fd.get('name')).trim();
    if (!name) throw new Error('Give the organization a name.');
    await api('POST', '/api/proxy/organizations', { name });
    toast('Organization created');
  };
  return html`<${FormDialog} title="Add organization" submit="Create" close=${close} onSubmit=${submit}>
    <${Field} label="Name"><input name="name" required placeholder="Acme Corp" /><//>
  <//>`;
}

function TeamForm({ team, orgs, models, close }) {
  const isNew = !team;
  const t = team || { org_id: orgs[0]?.id || '', name: '', monthly_budget_usd: 0, allowed_models: [] };
  // Models outside the loaded catalog stay listed so saving never drops them.
  const all = [...new Set([...models, ...(t.allowed_models || [])])];
  const [picked, setPicked] = useState(t.allowed_models || []);
  const toggle = (m) => setPicked(picked.includes(m) ? picked.filter((x) => x !== m) : [...picked, m]);
  const submit = async (fd) => {
    const body = { name: String(fd.get('name')).trim(), monthly_budget_usd: num(fd.get('monthly_budget_usd')), allowed_models: all.filter((m) => picked.includes(m)) };
    if (!body.name) throw new Error('Give the team a name.');
    if (isNew) await api('POST', '/api/proxy/teams', { ...body, org_id: fd.get('org_id') });
    else await api('PUT', '/api/proxy/teams/' + encodeURIComponent(t.id), body);
    toast(isNew ? 'Team created' : 'Team saved');
  };
  return html`<${FormDialog} title=${isNew ? 'Add team' : 'Edit ' + t.name} submit=${isNew ? 'Create team' : 'Save'} close=${close} onSubmit=${submit}>
    ${isNew && html`<${Field} label="Organization"><select name="org_id">${orgs.map((o) => html`<option value=${o.id} defaultSelected=${o.id === t.org_id}>${o.name}</option>`)}</select><//>`}
    <${Field} label="Name"><input name="name" defaultValue=${t.name} required placeholder="Search platform" /><//>
    <${Field} label="Monthly budget" hint="Spend of all the team's keys together per UTC month; empty or 0 = no cap. The gateway counts spend from the moment a cap is set.">
      <div class="prefix"><span>$</span><input name="monthly_budget_usd" type="number" min="0" step="any" defaultValue=${num(t.monthly_budget_usd) || ''} placeholder="No cap" /></div><//>
    <fieldset><legend>Allowed models</legend>
      <div class="cluster">${all.map((m) => html`<button type="button" key=${m} class="toggle-chip" aria-pressed=${picked.includes(m) ? 'true' : 'false'} onClick=${() => toggle(m)}>${m}</button>`)}</div>
      <p class="small muted" style=${{ marginTop: '8px' }}>${picked.length ? `Only these ${picked.length} may be called; rules skip the rest for this team.` : 'None selected: the team may call every model.'}</p>
    </fieldset>
  <//>`;
}
