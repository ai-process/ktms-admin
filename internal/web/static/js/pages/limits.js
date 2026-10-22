import { html, usd, api, useAsync, openDialog, confirmAction, toast, FormDialog, PageHead, Card, Table, Field, LoadError, compact, num } from '../lib.js';
import { isAdmin, loadCfg } from '../state.js';

const burstText = (b) => (num(b) > 0 ? `+${num(b)}` : num(b) < 0 ? 'none' : 'default');

export function LimitsPage() {
  const st = useAsync(async () => {
    const [cfg, res, spend] = await Promise.all([
      loadCfg(true), api('GET', '/api/proxy/user-rate-limits'),
      api('GET', '/api/proxy/spend-limits').then((r) => ({ items: (r.spend_limits || []).filter((l) => l.subject_type !== 'team') }), (error) => ({ items: [], error })),
    ]);
    return { models: cfg.models || [], limits: res?.rate_limits || cfg.user_rate_limits || [], spend };
  }, []);
  const limits = st.data?.limits || [];
  const edit = (l) => openDialog(LimitForm, { limit: l, models: st.data?.models || [] }).then((ok) => ok && st.reload());
  const del = (l) => confirmAction({
    title: 'Delete this limit?', text: `${l.user_id} on ${l.model_id} falls back to the model's own limits.`,
    action: () => api('DELETE', `/api/proxy/user-rate-limits?user_id=${encodeURIComponent(l.user_id)}&model_id=${encodeURIComponent(l.model_id)}`),
  }).then((ok) => ok && st.reload());

  const rows = limits.map((l) => html`<tr key=${l.user_id + '/' + l.model_id}>
    <td><span class="mono">${l.user_id === '*' ? html`<span class="chip">every user</span>` : l.user_id}</span></td>
    <td><span class="mono">${l.model_id === '*' ? html`<span class="chip">every model</span>` : l.model_id}</span></td>
    <td class="num">${num(l.rpm) || '∞'}</td>
    <td class="num">${burstText(l.rpm_burst)}</td>
    <td class="num">${num(l.queue_timeout_ms)} ms</td>
    <td class="num">${num(l.daily_tokens) ? compact(l.daily_tokens) : '∞'}</td>
    <td class="actions-cell">${isAdmin() && html`<div class="cluster">
      <button type="button" class="sm" onClick=${() => edit(l)}>Edit</button>
      <button type="button" class="sm danger" onClick=${() => del(l)}>Delete</button></div>`}</td>
  </tr>`);

  return html`
    <${PageHead} title="Limits" sub="Per-user rate limits and daily token budgets. They are checked before each model's own limits.">
      ${isAdmin() && html`<button type="button" class="primary" onClick=${() => edit(null)}>Add limit</button>`}
    <//>
    <${LoadError} error=${st.error} retry=${st.reload} />
    <${Card} flush label="User limits">
      <${Table} loading=${st.loading} rows=${rows}
        head=${['User', 'Model', { label: 'Requests / min', num: true }, { label: 'Burst', num: true }, { label: 'Queue timeout', num: true }, { label: 'Daily tokens', num: true }, { label: 'Actions', sr: true }]}
        empty=${{ title: 'No user limits', children: 'Every user gets the limits set on each model.' }} />
    <//>
    <${SpendLimits} items=${st.data?.spend.items || []} error=${st.data?.spend.error} loading=${st.loading} reload=${st.reload} />
    <p class="small muted">Team budgets are set on the Tenants page.</p>
  `;
}

function LimitForm({ limit, models, close }) {
  const isNew = !limit;
  const l = limit || { user_id: '', model_id: '*', rpm: 0, rpm_burst: 0, queue_timeout_ms: 0, daily_tokens: 0 };
  const submit = async (fd) => {
    const spec = {
      user_id: isNew ? String(fd.get('user_id')).trim() : l.user_id,
      model_id: isNew ? String(fd.get('model_id')).trim() : l.model_id,
      rpm: num(fd.get('rpm')), rpm_burst: num(fd.get('rpm_burst')),
      queue_timeout_ms: num(fd.get('queue_timeout_ms')), daily_tokens: num(fd.get('daily_tokens')),
    };
    if (!spec.user_id || !spec.model_id) throw new Error('User and model are required; use * for every one.');
    await api('PUT', '/api/proxy/user-rate-limits', spec);
    toast('Limit saved');
  };
  return html`<${FormDialog} title=${isNew ? 'Add user limit' : `Limit for ${l.user_id}`} sub=${isNew ? '' : 'on ' + (l.model_id === '*' ? 'every model' : l.model_id)} close=${close} onSubmit=${submit}>
    <div class="form-grid">
      <${Field} label="User" hint="The request's user_id attribute, or * for everyone."><input name="user_id" defaultValue=${l.user_id} required disabled=${!isNew} placeholder="alice@corp.com" /><//>
      <${Field} label="Model" hint="* covers every model."><input name="model_id" defaultValue=${l.model_id} required disabled=${!isNew} list="limit-models" /><//>
      <datalist id="limit-models"><option value="*" />${models.map((m) => html`<option value=${m.id} />`)}</datalist>
      <${Field} label="Requests / min" hint="0 = unlimited"><input name="rpm" type="number" min="0" defaultValue=${num(l.rpm)} /><//>
      <${Field} label="Burst" hint="0 = rpm − 1, below 0 = none"><input name="rpm_burst" type="number" defaultValue=${num(l.rpm_burst)} /><//>
      <${Field} label="Queue timeout, ms" hint="0 = fall back at once"><input name="queue_timeout_ms" type="number" min="0" defaultValue=${num(l.queue_timeout_ms)} /><//>
      <${Field} label="Daily tokens" hint="0 = unlimited"><input name="daily_tokens" type="number" min="0" defaultValue=${num(l.daily_tokens)} /><//>
    </div>
  <//>`;
}

const subjectText = (l) => (l.subject_id === '*' ? `every ${l.subject_type}` : l.subject_id);

// Spend limits cap what a key or a user may spend per UTC day or month, at
// list prices; the gateway refuses calls past the cap.
function SpendLimits({ items, error, loading, reload }) {
  const add = () => openDialog(SpendForm).then((ok) => ok && reload());
  const del = (l) => confirmAction({
    title: 'Remove this spend limit?', text: `${subjectText(l)} is no longer capped per ${l.period}.`, confirm: 'Remove',
    action: () => api('DELETE', '/api/proxy/spend-limits?' + new URLSearchParams({ subject_type: l.subject_type, subject_id: l.subject_id, period: l.period })),
  }).then((ok) => ok && reload());
  const rows = items.map((l) => html`<tr key=${l.subject_type + l.subject_id + l.period}>
    <td>${l.subject_type}</td><td><span class="mono">${subjectText(l)}</span></td><td>per ${l.period}</td>
    <td class="num">${usd(num(l.limit_nanousd) / 1e9)}</td>
    <td class="actions-cell">${isAdmin() && html`<button type="button" class="sm danger" onClick=${() => del(l)}>Remove</button>`}</td>
  </tr>`);
  return html`<${Card} title="Spend limits" sub="Caps in USD at list prices, per UTC day or month." flush
    actions=${isAdmin() && html`<button type="button" onClick=${add}>Add spend limit</button>`}>
    ${error && html`<div class="card-body"><${LoadError} error=${error} retry=${reload} /></div>`}
    ${!error && html`<${Table} loading=${loading} rows=${rows} head=${['Subject', 'Who', 'Period', { label: 'Cap', num: true }, { label: 'Actions', sr: true }]}
      empty=${{ title: 'No spend limits', children: 'Keys and users spend without a cap.' }} />`}
  <//>`;
}

function SpendForm({ close }) {
  const submit = async (fd) => {
    const usdv = num(fd.get('usd'));
    if (!(usdv > 0)) throw new Error('The cap must be above $0.');
    const id = String(fd.get('subject_id')).trim();
    if (!id) throw new Error('Name the key or user, or use * for the default.');
    await api('PUT', '/api/proxy/spend-limits', { subject_type: fd.get('subject_type'), subject_id: id, period: fd.get('period'), limit_nanousd: String(Math.round(usdv * 1e9)) });
    toast('Spend limit saved');
  };
  return html`<${FormDialog} title="Add spend limit" close=${close} onSubmit=${submit}>
    <div class="form-grid">
      <${Field} label="Applies to"><select name="subject_type"><option value="key">API key</option><option value="user">User</option></select><//>
      <${Field} label="Key name or user id" hint="* = the default for every key or user."><input name="subject_id" required /><//>
      <${Field} label="Period"><select name="period"><option value="day">UTC day</option><option value="month">UTC month</option></select><//>
      <${Field} label="Cap"><div class="prefix"><span>$</span><input name="usd" type="number" min="0" step="any" required /></div><//>
    </div>
  <//>`;
}
