import { html, useState, useMemo, api, useAsync, openDialog, confirmAction, toast, FormDialog, PageHead, Card, Table, Field, Badge, LoadError, when, usd, num, cx, compact } from '../lib.js';
import { isAdmin, isTeamLead, canReadGlobal, session, loadCfg } from '../state.js';
import { VENDORS } from './models.js';

const SCOPES = [
  ['generate', 'Call models', 'Text, streaming, speech, images, judge and embeddings, through the rules.'],
  ['admin', 'Manage the gateway', 'Models, rules, keys and limits. Give it to people and consoles, not services.'],
];
const nanoUSD = (v) => num(v) / 1e9;

export function KeysPage({ params }) {
  const st = useAsync(async () => {
    const [keys, teams, spend, cfg] = await Promise.all([
      api('GET', '/api/proxy/keys'),
      api('GET', '/api/proxy/teams').catch(() => ({ teams: [] })),
      api('GET', '/api/proxy/key-spend').catch((error) => ({ error })),
      canReadGlobal() ? loadCfg(true).catch((error) => ({ error })) : null,
    ]);
    // Side panels fail on their own: the key list still shows, and an
    // unreadable panel says so instead of looking empty.
    return { keys: keys.keys || [], teams: teams.teams || [], spend: spend.keys || {}, spendError: spend.error,
      vendorKeys: cfg?.vendor_keys || [], models: (cfg?.models || []).map((m) => m.id), cfgError: cfg?.error };
  }, []);
  const teamName = useMemo(() => Object.fromEntries((st.data?.teams || []).map((t) => [t.id, t.name])), [st.data]);
  const canMint = isAdmin() || isTeamLead();

  const revoke = (k) => confirmAction({
    title: `Revoke ${k.name}?`, text: 'Clients using it are refused at once. This cannot be undone; mint a new key instead.', confirm: 'Revoke',
    action: () => api('DELETE', '/api/proxy/keys/' + encodeURIComponent(k.name)),
  }).then((ok) => ok && st.reload());

  const editPolicy = (k) => openDialog(PolicyForm, { k, models: modelChoices(st.data, k) }).then((ok) => ok && st.reload());
  const keys = st.data?.keys || [];
  const rows = keys.map((k) => html`<tr key=${k.name} class=${k.disabled ? 'off' : ''}>
    <td><div class="stacked"><span class="mono">${k.name}</span><span class="small">${k.team_id ? 'team ' + (teamName[k.team_id] || k.team_id.slice(0, 8) + '…') : 'no team'}</span></div></td>
    <td><div class="cluster">${(k.scopes || []).map((s) => html`<span class="chip" key=${s}>${Object.fromEntries(SCOPES.map(([id, t]) => [id, t]))[s] || s}</span>`)}</div></td>
    <td><${PolicyChips} policy=${k.policy} /></td>
    <td><${SpendChips} spend=${k.disabled ? {} : st.data?.spend?.[k.name]} failed=${!!st.data?.spendError} /></td>
    <td>${when(k.created_at)}</td>
    <td>${k.disabled ? html`<${Badge}>Revoked<//>` : html`<${Badge} tone="ok">Active<//>`}</td>
    <td class="actions-cell">${canMint && !k.disabled && html`<button type="button" class="sm" onClick=${() => editPolicy(k)}>Policy</button><button type="button" class="sm danger" onClick=${() => revoke(k)}>Revoke</button>`}</td>
  </tr>`);

  return html`
    <${PageHead} title="API keys" sub=${canReadGlobal() ? 'Services call KTMS with these keys. Vendor keys stay on the server and are never shown to them.' : 'Keys for your team. Each is bound to the team’s models and monthly budget.'} />
    <${LoadError} error=${st.error} retry=${st.reload} />
    <div class="row">
      <div style=${{ flex: '3 1 560px' }}>
        <${Card} flush label="API keys">
          <${Table} loading=${st.loading} rows=${rows}
            head=${['Name', 'Access', 'Policy', 'Spend against caps', 'Created', 'Status', { label: 'Actions', sr: true }]}
            empty=${{ title: 'No API keys yet', children: canMint ? 'Create one with the form.' : 'Ask a team lead for one.' }} />
        <//>
      </div>
      ${canMint && html`<div style=${{ flex: '2 1 360px' }}><${Wizard} teams=${st.data?.teams || []} team=${params.get('team_id') || ''} onDone=${st.reload} /></div>`}
    </div>
    ${st.data?.spendError && html`<div class="notice warn">Key spend could not be read: ${st.data.spendError.message}</div>`}
    ${canReadGlobal() && html`<${VendorKeys} vendorKeys=${st.data?.vendorKeys || []} error=${st.data?.cfgError} keys=${keys} loading=${st.loading} reload=${st.reload} />`}
  `;
}

function Wizard({ teams, team, onDone }) {
  const lead = !isAdmin();
  const labels = lead ? ['Name', 'Done'] : ['Name', 'Access', 'Limits', 'Done'];
  const fresh = () => ({ step: 0, name: '', team: lead ? session.me.user.team_id : team, scopes: ['generate'], daily: '', monthly: '', rpm: '', tpm: '', key: '', warn: '', copied: false, err: '', busy: false });
  const [s, setS] = useState(fresh);
  const set = (p) => setS((x) => ({ ...x, ...p }));
  const last = labels.length - 1;
  const atCreate = s.step === last - 1;

  const validate = () => {
    if (s.step === 0 && !s.name.trim()) return 'Give the key a name.';
    if (labels[s.step] === 'Access' && !s.scopes.length) return 'Pick at least one kind of access.';
    if (labels[s.step] === 'Limits' && [s.daily, s.monthly].some((v) => v !== '' && !(num(v) > 0))) return 'A cap must be above $0, or left empty.';
    if (labels[s.step] === 'Limits' && [s.rpm, s.tpm].some((v) => v !== '' && !(Number.isInteger(num(v)) && num(v) > 0))) return 'A rate must be a whole number above 0, or left empty.';
    return '';
  };

  const create = async () => {
    const team = teams.find((t) => t.id === s.team);
    const body = { name: s.name.trim(), scopes: lead ? ['generate'] : s.scopes };
    if (team) { body.team_id = team.id; body.org_id = team.org_id; }
    if (s.rpm !== '' || s.tpm !== '') body.policy = { rpm_limit: num(s.rpm) || 0, tpm_limit: String(num(s.tpm) || 0) };
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
        <${Field} label="Monthly spend cap (optional)" hint="Counted per UTC calendar month."><div class="prefix"><span>$</span><input type="number" min="0" step="any" value=${s.monthly} placeholder="No cap" onInput=${(e) => set({ monthly: e.currentTarget.value })} /></div><//>
        <${Field} label="Requests per minute (optional)" hint="Over all models. Allowed models are set with Policy after the key exists."><input type="number" min="1" step="1" value=${s.rpm} placeholder="No limit" onInput=${(e) => set({ rpm: e.currentTarget.value })} /><//>
        <${Field} label="Tokens per minute (optional)" hint="Input and output over all models."><input type="number" min="1" step="1" value=${s.tpm} placeholder="No limit" onInput=${(e) => set({ tpm: e.currentTarget.value })} /><//>`}
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

// SpendChips shows each capped period as KTMS counts it: spent of the cap, or
// only the cap when nothing is counted (no Redis), or unknown when unreadable.
function SpendChips({ spend, failed }) {
  if (failed || spend?.error) return html`<span class="muted">unknown</span>`;
  const capped = (spend?.windows || []).filter((w) => num(w.limit_nanousd) > 0);
  if (!capped.length) return html`<span class="muted">—</span>`;
  return html`<div class="cluster">${capped.map((w) => {
    const cap = `${usd(nanoUSD(w.limit_nanousd))} / ${w.period}`;
    return html`<span class="chip" key=${w.period}>${w.metered ? `${usd(nanoUSD(w.spent_nanousd))} of ${cap}` : cap}</span>`;
  })}</div>`;
}

function PolicyChips({ policy }) {
  const p = policy || {};
  const chips = [];
  if ((p.allowed_models || []).length) chips.push(html`<span class="chip" key="m" title=${p.allowed_models.join(', ')}>${p.allowed_models.length === 1 ? p.allowed_models[0] : p.allowed_models.length + ' models'}</span>`);
  if (num(p.rpm_limit) > 0) chips.push(html`<span class="chip" key="r">${compact(num(p.rpm_limit))} rpm</span>`);
  if (num(p.tpm_limit) > 0) chips.push(html`<span class="chip" key="t">${compact(num(p.tpm_limit))} tpm</span>`);
  return chips.length ? html`<div class="cluster">${chips}</div>` : html`<span class="muted">—</span>`;
}

// modelChoices lists the models a key's allowlist can usefully name: its team's
// allowed models when the team has a list (KTMS intersects the two), else the
// gateway's, plus whatever it names now. Empty means type the ids.
function modelChoices(data, k) {
  const team = (data?.teams || []).find((t) => t.id === k.team_id);
  const base = team?.allowed_models?.length ? team.allowed_models : (data?.models || []);
  return [...new Set([...base, ...(k.policy?.allowed_models || [])])].sort();
}

function PolicyForm({ k, models, close }) {
  const [picked, setPicked] = useState(k.policy?.allowed_models || []);
  const toggle = (m) => setPicked((p) => (p.includes(m) ? p.filter((x) => x !== m) : [...p, m]));
  const submit = async (fd) => {
    const rpm = num(fd.get('rpm')), tpm = num(fd.get('tpm'));
    if (![rpm, tpm].every((v) => Number.isInteger(v) && v >= 0)) throw new Error('Rates are whole numbers, 0 for no limit.');
    const allowed = models.length ? picked : String(fd.get('models') || '').split(',').map((m) => m.trim()).filter(Boolean);
    await api('PUT', `/api/proxy/keys/${encodeURIComponent(k.name)}/policy`, { allowed_models: allowed, rpm_limit: rpm, tpm_limit: String(tpm) });
    toast('Policy saved');
  };
  return html`<${FormDialog} title=${'Policy of ' + k.name} sub="Applies from the key's next call, on top of its team's models and each model's limits." close=${close} onSubmit=${submit}>
    <fieldset><legend>Allowed models</legend>
      ${models.length
        ? html`<div class="cluster">${models.map((m) => html`<button type="button" key=${m} class="toggle-chip" aria-pressed=${picked.includes(m) ? 'true' : 'false'} onClick=${() => toggle(m)}>${m}</button>`)}</div>`
        : html`<${Field} label="Model ids" hint="Comma-separated; empty allows any model the team allows."><input name="models" defaultValue=${picked.join(', ')} autocomplete="off" /><//>`}
      ${models.length > 0 && html`<p class="small muted">${picked.length ? 'Only these, and only where the team allows them too.' : 'None picked: any model the team allows.'}</p>`}
    </fieldset>
    <div class="form-grid">
      <${Field} label="Requests per minute" hint="Over all models; 0 = no limit."><input name="rpm" type="number" min="0" step="1" defaultValue=${num(k.policy?.rpm_limit)} /><//>
      <${Field} label="Tokens per minute" hint="Input and output over all models; 0 = no limit."><input name="tpm" type="number" min="0" step="1" defaultValue=${num(k.policy?.tpm_limit)} /><//>
    </div>
  <//>`;
}

function VendorKeys({ vendorKeys, error, keys, loading, reload }) {
  const active = keys.filter((k) => !k.disabled).map((k) => k.name);
  const setKey = () => openDialog(VendorForm, { names: active }).then((ok) => ok && reload());
  const rotate = () => confirmAction({
    title: 'Re-encrypt vendor keys?', confirm: 'Re-encrypt', danger: false,
    text: 'Re-encrypts every stored vendor credential with the newest key in KTMS_ENCRYPTION_KEYS. Do this after adding a key version.',
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
    ${error ? html`<div class="card-body"><${LoadError} error=${error} retry=${reload} /></div>` : html`<${Table} loading=${loading} rows=${rows} head=${['API key', 'Vendor', { label: 'Key version', num: true }, 'Set', { label: 'Actions', sr: true }]}
      empty=${{ title: 'No vendor credentials', children: 'Local models need none; cloud models need one per client key.' }} />`}
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
