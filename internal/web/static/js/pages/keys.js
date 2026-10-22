import { html, useState, useMemo, api, useAsync, openDialog, confirmAction, toast, FormDialog, PageHead, Card, Table, Field, Badge, LoadError, when, usd, num, cx } from '../lib.js';
import { isAdmin, isTeamLead, canReadGlobal, session, loadCfg } from '../state.js';
import { VENDORS } from './models.js';

const SCOPES = [
  ['generate', 'Call models', 'Text, streaming, speech, images, judge and embeddings, through the rules.'],
  ['admin', 'Manage the gateway', 'Models, rules, keys and limits. Give it to people and consoles, not services.'],
];
const nanoUSD = (v) => num(v) / 1e9;

export function KeysPage({ params }) {
  const st = useAsync(async () => {
    const [keys, teams, limits, cfg] = await Promise.all([
      api('GET', '/api/proxy/keys'),
      api('GET', '/api/proxy/teams').catch(() => ({ teams: [] })),
      canReadGlobal() ? api('GET', '/api/proxy/spend-limits?subject_type=key').catch(() => ({ spend_limits: [] })) : { spend_limits: [] },
      canReadGlobal() ? loadCfg(true) : null,
    ]);
    return { keys: keys.keys || [], teams: teams.teams || [], limits: limits.spend_limits || [], vendorKeys: cfg?.vendor_keys || [] };
  }, []);
  const teamName = useMemo(() => Object.fromEntries((st.data?.teams || []).map((t) => [t.id, t.name])), [st.data]);
  const caps = useMemo(() => {
    const m = {};
    for (const l of st.data?.limits || []) (m[l.subject_id] ||= []).push(l);
    return m;
  }, [st.data]);
  const canMint = isAdmin() || isTeamLead();

  const revoke = (k) => confirmAction({
    title: `Revoke ${k.name}?`, text: 'Clients using it are refused at once. This cannot be undone; mint a new key instead.', confirm: 'Revoke',
    action: () => api('DELETE', '/api/proxy/keys/' + encodeURIComponent(k.name)),
  }).then((ok) => ok && st.reload());

  const keys = st.data?.keys || [];
  const rows = keys.map((k) => html`<tr key=${k.name} class=${k.disabled ? 'off' : ''}>
    <td><div class="stacked"><span class="mono">${k.name}</span><span class="small">${k.team_id ? 'team ' + (teamName[k.team_id] || k.team_id.slice(0, 8) + '…') : 'no team'}</span></div></td>
    <td><div class="cluster">${(k.scopes || []).map((s) => html`<span class="chip" key=${s}>${Object.fromEntries(SCOPES.map(([id, t]) => [id, t]))[s] || s}</span>`)}</div></td>
    ${canReadGlobal() && html`<td>${(caps[k.name] || []).length ? html`<div class="cluster">${caps[k.name].map((l) => html`<span class="chip" key=${l.period}>${usd(nanoUSD(l.limit_nanousd))} / ${l.period}</span>`)}</div>` : html`<span class="muted">—</span>`}</td>`}
    <td>${when(k.created_at)}</td>
    <td>${k.disabled ? html`<${Badge}>Revoked<//>` : html`<${Badge} tone="ok">Active<//>`}</td>
    <td class="actions-cell">${canMint && !k.disabled && html`<button type="button" class="sm danger" onClick=${() => revoke(k)}>Revoke</button>`}</td>
  </tr>`);

  return html`
    <${PageHead} title="API keys" sub=${canReadGlobal() ? 'Services call KTMS with these keys. Vendor keys stay on the server and are never shown to them.' : 'Keys for your team. Each is bound to the team’s models and monthly budget.'} />
    <${LoadError} error=${st.error} retry=${st.reload} />
    <div class="row">
      <div style=${{ flex: '3 1 560px' }}>
        <${Card} flush label="API keys">
          <${Table} loading=${st.loading} rows=${rows}
            head=${['Name', 'Access', ...(canReadGlobal() ? ['Spend caps'] : []), 'Created', 'Status', { label: 'Actions', sr: true }]}
            empty=${{ title: 'No API keys yet', children: canMint ? 'Create one with the form.' : 'Ask a team lead for one.' }} />
        <//>
      </div>
      ${canMint && html`<div style=${{ flex: '2 1 360px' }}><${Wizard} teams=${st.data?.teams || []} team=${params.get('team_id') || ''} onDone=${st.reload} /></div>`}
    </div>
    ${canReadGlobal() && html`<${VendorKeys} vendorKeys=${st.data?.vendorKeys || []} keys=${keys} loading=${st.loading} reload=${st.reload} />`}
  `;
}

function Wizard({ teams, team, onDone }) {
  const lead = !isAdmin();
  const labels = lead ? ['Name', 'Done'] : ['Name', 'Access', 'Limits', 'Done'];
  const fresh = () => ({ step: 0, name: '', team: lead ? session.me.user.team_id : team, scopes: ['generate'], daily: '', monthly: '', key: '', warn: '', copied: false, err: '', busy: false });
  const [s, setS] = useState(fresh);
  const set = (p) => setS((x) => ({ ...x, ...p }));
  const last = labels.length - 1;
  const atCreate = s.step === last - 1;

  const validate = () => {
    if (s.step === 0 && !s.name.trim()) return 'Give the key a name.';
    if (labels[s.step] === 'Access' && !s.scopes.length) return 'Pick at least one kind of access.';
    if (labels[s.step] === 'Limits' && [s.daily, s.monthly].some((v) => v !== '' && !(num(v) > 0))) return 'A cap must be above $0, or left empty.';
    return '';
  };

  const create = async () => {
    const team = teams.find((t) => t.id === s.team);
    const body = { name: s.name.trim(), scopes: lead ? ['generate'] : s.scopes };
    if (team) { body.team_id = team.id; body.org_id = team.org_id; }
    const res = await api('POST', '/api/proxy/keys', body);
    // The key exists now; a cap that fails to save must not hide it.
    const failed = [];
    for (const [period, v] of [['day', s.daily], ['month', s.monthly]]) {
      if (v === '') continue;
      try { await api('PUT', '/api/proxy/spend-limits', { subject_type: 'key', subject_id: body.name, period, limit_nanousd: String(Math.round(num(v) * 1e9)) }); } catch (e) { failed.push(`${period}: ${e.message}`); }
    }
    return { key: res.plain_key, warn: failed.length ? 'The key was created, but a spend cap was not saved (' + failed.join('; ') + '). Set it on the Limits page.' : '' };
  };

  const next = async () => {
    if (s.step === last) { setS(fresh()); return; }
    const err = validate();
    if (err) { set({ err }); return; }
    if (!atCreate) { set({ step: s.step + 1, err: '' }); return; }
    set({ busy: true, err: '' });
    try { const r = await create(); set({ ...r, step: last, busy: false }); onDone(); toast('Key created'); } catch (e) { set({ err: e.message, busy: false }); }
  };
  const copy = async () => { try { await navigator.clipboard.writeText(s.key); set({ copied: true }); } catch { toast('Copy failed: select the key and copy it by hand', true); } };
  const at = labels[s.step];

  return html`<section class="card" aria-label="Create a key">
    <div class="card-head" style=${{ flexDirection: 'column', alignItems: 'stretch' }}>
      <h2>Create a key</h2>
      <ol class="steps" aria-label="Steps">${labels.map((l, i) => html`<li key=${l} class=${cx(i < s.step && 'done', i === s.step && 'on')} aria-current=${i === s.step ? 'step' : undefined}><div></div><span>${l}</span></li>`)}</ol>
    </div>
    <div class="card-body">
      ${at === 'Name' && html`
        <${Field} label="Name" hint="Shown in usage reports and limits; one key per service."><input value=${s.name} onInput=${(e) => set({ name: e.currentTarget.value })} placeholder="search-service" autocomplete="off" aria-invalid=${s.err && !s.name.trim() ? 'true' : undefined} /><//>
        ${lead
          ? html`<p class="small muted">The key is bound to your team, its allowed models and its monthly budget.</p>`
          : html`<${Field} label="Team" hint="A team key may call only the team's models, within its monthly budget."><select value=${s.team} onChange=${(e) => set({ team: e.currentTarget.value })}>
              <option value="">No team</option>${teams.map((t) => html`<option value=${t.id}>${t.name}</option>`)}</select><//>`}`}
      ${at === 'Access' && html`<fieldset><legend>What may this key do?</legend><div class="stack">
        ${SCOPES.map(([id, title, text]) => html`<button type="button" key=${id} class="choice" aria-pressed=${s.scopes.includes(id) ? 'true' : 'false'}
          onClick=${() => set({ scopes: s.scopes.includes(id) ? s.scopes.filter((x) => x !== id) : [...s.scopes, id] })}><b>${title}</b><span>${text}</span></button>`)}
        <p class="small muted">Local GPU nodes use node keys, minted on the Nodes page.</p>
      </div></fieldset>`}
      ${at === 'Limits' && html`
        <${Field} label="Daily spend cap (optional)" hint="Calls are refused once the key has spent this much since 00:00 UTC."><div class="prefix"><span>$</span><input type="number" min="0" step="any" value=${s.daily} placeholder="No cap" onInput=${(e) => set({ daily: e.currentTarget.value })} /></div><//>
        <${Field} label="Monthly spend cap (optional)" hint="Counted per UTC calendar month."><div class="prefix"><span>$</span><input type="number" min="0" step="any" value=${s.monthly} placeholder="No cap" onInput=${(e) => set({ monthly: e.currentTarget.value })} /></div><//>`}
      ${at === 'Done' && html`
        <div class="notice ok"><b>Key created.</b> This is the only time it is shown. Store it in your secret manager now; KTMS keeps only a hash.</div>
        <div class="secret"><code>${s.key}</code><button type="button" class="sm" onClick=${copy}>${s.copied ? 'Copied' : 'Copy'}</button></div>
        ${s.warn && html`<div class="notice warn">${s.warn}</div>`}`}
      ${s.err && html`<div class="notice bad" role="alert">${s.err}</div>`}
    </div>
    <div class="card-foot">
      <button type="button" onClick=${() => (s.step === 0 || s.step === last ? setS(fresh()) : set({ step: s.step - 1, err: '' }))} disabled=${s.busy}>${s.step === 0 ? 'Clear' : s.step === last ? 'Create another' : 'Back'}</button>
      ${s.step !== last && html`<button type="button" class="primary" onClick=${next} disabled=${s.busy}>${s.busy ? 'Creating…' : atCreate ? 'Create key' : 'Continue'}</button>`}
    </div>
  </section>`;
}

function VendorKeys({ vendorKeys, keys, loading, reload }) {
  const active = keys.filter((k) => !k.disabled).map((k) => k.name);
  const setKey = () => openDialog(VendorForm, { names: active }).then((ok) => ok && reload());
  const rotate = () => confirmAction({
    title: 'Re-encrypt vendor keys?', confirm: 'Re-encrypt', danger: false,
    text: 'Re-encrypts every stored vendor credential with the newest key in LLMPROXY_ENCRYPTION_KEYS. Do this after adding a key version.',
    action: async () => { const r = await api('POST', '/api/proxy/rotate'); toast(`Re-encrypted ${r?.reencrypted ?? 0} credentials`); },
  });
  const remove = (v) => confirmAction({
    title: `Remove the ${v.vendor} credential?`, text: `${v.api_key_name} can no longer call ${v.vendor} models unless another credential covers them.`, confirm: 'Remove',
    action: () => api('DELETE', `/api/proxy/vendor-keys?api_key_name=${encodeURIComponent(v.api_key_name)}&vendor=${encodeURIComponent(v.vendor)}`),
  }).then((ok) => ok && reload());
  const rows = vendorKeys.map((v) => html`<tr key=${v.api_key_name + '/' + v.vendor}>
    <td><span class="mono">${v.api_key_name}</span></td><td>${v.vendor}</td><td class="num">v${v.key_version}</td><td>${when(v.created_at)}</td>
    <td class="actions-cell">${isAdmin() && html`<button type="button" class="sm danger" onClick=${() => remove(v)}>Remove</button>`}</td>
  </tr>`);
  return html`<${Card} title="Vendor credentials" sub="Which client key may use which vendor account. Stored encrypted; never shown again." flush
    actions=${isAdmin() && html`<button type="button" onClick=${rotate}>Re-encrypt all</button><button type="button" class="primary" onClick=${setKey} disabled=${!active.length}>Add credential</button>`}>
    <${Table} loading=${loading} rows=${rows} head=${['API key', 'Vendor', { label: 'Key version', num: true }, 'Set', { label: 'Actions', sr: true }]}
      empty=${{ title: 'No vendor credentials', children: 'Local models need none; cloud models need one per client key.' }} />
  <//>`;
}

function VendorForm({ names, close }) {
  const submit = async (fd) => {
    await api('PUT', '/api/proxy/vendor-keys', { api_key_name: fd.get('api_key_name'), vendor: fd.get('vendor'), plain_key: fd.get('plain_key') });
    toast('Credential saved');
  };
  return html`<${FormDialog} title="Add vendor credential" sub="The client key uses this vendor account for every model of that vendor." close=${close} onSubmit=${submit}>
    <${Field} label="Client API key"><select name="api_key_name">${names.map((n) => html`<option value=${n}>${n}</option>`)}</select><//>
    <${Field} label="Vendor"><select name="vendor">${VENDORS.filter((v) => v !== 'local').map((v) => html`<option value=${v}>${v}</option>`)}</select><//>
    <${Field} label="Vendor API key" hint="Stored encrypted. It is never shown again."><input name="plain_key" type="password" required autocomplete="off" /><//>
  <//>`;
}
