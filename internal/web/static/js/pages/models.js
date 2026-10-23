import { html, useState, api, useAsync, openDialog, confirmAction, toast, FormDialog, PageHead, Card, Table, Field, Check, Badge, LoadError, compact, num, effortName } from '../lib.js';
import { isAdmin, loadCfg } from '../state.js';

export const VENDORS = ['openai', 'anthropic', 'google', 'deepseek', 'groq', 'mistral', 'azure', 'bedrock', 'ollama', 'typesafe', 'local'];
const CAPS = [
  ['google_search', 'Google Search grounding'],
  ['no_structured_output', 'No JSON schema output'],
  ['tts', 'Speech synthesis'],
  ['image', 'Image generation'],
  ['judge', 'Judge'],
  ['prompt_cache', 'Anthropic: cache tools, system prompt and conversation'],
];

const BLANK = {
  id: '', vendor: 'openai', endpoint: '', efforts: ['EFFORT_LOW', 'EFFORT_MEDIUM', 'EFFORT_HIGH'], capabilities: [],
  rpm: 0, rpm_burst: 0, queue_timeout_ms: 0, budget_grace_margin: 0,
  price_in_per_mtok: 0, price_out_per_mtok: 0, price_in_peak_per_mtok: 0, price_out_peak_per_mtok: 0,
  price_in_batch_per_mtok: 0, price_out_batch_per_mtok: 0, price_cache_read_per_mtok: 0, price_cache_write_per_mtok: 0,
  daily_tokens_per_key: 0, daily_tokens_per_user: 0, enabled: true,
};

const rpmText = (m) => {
  if (!num(m.rpm)) return 'unlimited';
  if (num(m.rpm_burst) > 0) return `${m.rpm} (+${m.rpm_burst} burst)`;
  if (num(m.rpm_burst) < 0) return `${m.rpm}, no burst`;
  return String(m.rpm);
};

export function ModelsPage() {
  const st = useAsync(() => loadCfg(true), []);
  const models = st.data?.models || [];
  const edit = (m) => openDialog(ModelForm, { model: m, wide: true }).then((ok) => ok && st.reload());
  const del = (m) => confirmAction({
    title: `Delete ${m.id}?`, text: 'Rules that route to it must be changed first; the gateway refuses the delete otherwise.',
    action: () => api('DELETE', '/api/proxy/models/' + encodeURIComponent(m.id)),
  }).then((ok) => ok && st.reload());

  const rows = models.map((m) => html`<tr key=${m.id} class=${m.enabled ? '' : 'off'}>
    <td><div class="stacked"><span class="mono">${m.id}</span><span class="small">${m.vendor}${m.endpoint ? ' · custom endpoint' : ''}</span></div></td>
    <td><div class="cluster">${(m.efforts || []).map((e) => html`<span class="chip" key=${e}>${effortName(e)}</span>`)}</div></td>
    <td><div class="cluster">${(m.capabilities || []).map((c) => html`<span class="chip" key=${c}>${c}</span>`)}</div></td>
    <td class="num">$${num(m.price_in_per_mtok)} / $${num(m.price_out_per_mtok)}</td>
    <td class="num">${rpmText(m)}</td>
    <td class="num">${num(m.daily_tokens_per_key) ? compact(m.daily_tokens_per_key) : '∞'}</td>
    <td>${m.enabled ? html`<${Badge} tone="ok">Enabled<//>` : html`<${Badge}>Disabled<//>`}</td>
    <td class="actions-cell">${isAdmin() && html`<div class="cluster">
      <button type="button" class="sm" onClick=${() => edit(m)}>Edit</button>
      <button type="button" class="sm danger" onClick=${() => del(m)}>Delete</button>
    </div>`}</td>
  </tr>`);

  return html`
    <${PageHead} title="Models" sub="The registry rules route to. List prices feed spend limits and cost estimates.">
      ${isAdmin() && html`<button type="button" class="primary" onClick=${() => edit(null)}>Add model</button>`}
    <//>
    <${LoadError} error=${st.error} retry=${st.reload} />
    <${Card} flush label="Models">
      <${Table} loading=${st.loading} rows=${rows}
        head=${['Model', 'Efforts', 'Capabilities', { label: 'In / out per Mtok', num: true }, { label: 'Requests / min', num: true }, { label: 'Daily tokens / key', num: true }, 'Status', { label: 'Actions', sr: true }]}
        empty=${{ title: 'No models yet', children: 'Add one, then a rule that routes to it.', action: isAdmin() && html`<button type="button" class="primary" onClick=${() => edit(null)}>Add model</button>` }} />
    <//>
  `;
}

function ModelForm({ model, close }) {
  const isNew = !model;
  const m = { ...BLANK, ...(model || {}) };
  const [caps, setCaps] = useState(m.capabilities || []);
  const known = new Set(CAPS.map(([c]) => c));
  const extra = caps.filter((c) => !known.has(c));
  const toggle = (c) => setCaps(caps.includes(c) ? caps.filter((x) => x !== c) : [...caps, c]);
  const n = (name, label, hint, props = {}) => html`<${Field} label=${label} hint=${hint}><input name=${name} type="number" step="any" defaultValue=${num(m[name])} ...${props} /><//>`;

  const submit = async (fd) => {
    const id = isNew ? String(fd.get('id')).trim() : m.id;
    if (!/^[A-Za-z0-9._:/-]+$/.test(id)) throw new Error('The model id may contain letters, digits and . _ : / - only.');
    const efforts = ['LOW', 'MEDIUM', 'HIGH'].filter((e) => fd.get('effort_' + e)).map((e) => 'EFFORT_' + e);
    const spec = { ...m, id, vendor: fd.get('vendor'), endpoint: String(fd.get('endpoint')).trim(), efforts, capabilities: caps, enabled: !!fd.get('enabled') };
    for (const k of ['rpm', 'rpm_burst', 'queue_timeout_ms', 'budget_grace_margin', 'price_in_per_mtok', 'price_out_per_mtok', 'price_in_peak_per_mtok', 'price_out_peak_per_mtok', 'price_in_batch_per_mtok', 'price_out_batch_per_mtok', 'price_cache_read_per_mtok', 'price_cache_write_per_mtok', 'daily_tokens_per_key', 'daily_tokens_per_user']) spec[k] = num(fd.get(k));
    await api('PUT', '/api/proxy/models/' + encodeURIComponent(id), spec);
    toast('Model saved');
  };

  return html`<${FormDialog} title=${isNew ? 'Add model' : 'Edit ' + m.id} sub="Changes apply to new requests within seconds." close=${close} onSubmit=${submit}>
    <div class="form-grid">
      <${Field} label="Model id" hint="Sent to the vendor and named by rules."><input name="id" defaultValue=${m.id} required disabled=${!isNew} autocomplete="off" /><//>
      <${Field} label="Vendor"><select name="vendor">${VENDORS.map((v) => html`<option value=${v} defaultSelected=${v === m.vendor}>${v}</option>`)}</select><//>
      <${Field} label="Endpoint override" hint="Optional base URL, e.g. an Azure deployment or a local server." class="wide"><input name="endpoint" defaultValue=${m.endpoint} placeholder="https://…" /><//>
    </div>
    <fieldset><legend>Efforts it may serve</legend><div class="cluster">
      ${['LOW', 'MEDIUM', 'HIGH'].map((e) => html`<${Check} key=${e} name=${'effort_' + e} label=${e.toLowerCase()} defaultChecked=${m.efforts.includes('EFFORT_' + e)} />`)}
    </div></fieldset>
    <fieldset><legend>Capabilities</legend><div class="cluster">
      ${CAPS.map(([c, l]) => html`<button type="button" key=${c} class="toggle-chip" aria-pressed=${caps.includes(c) ? 'true' : 'false'} title=${l} onClick=${() => toggle(c)}>${c}</button>`)}
      ${extra.map((c) => html`<button type="button" key=${c} class="toggle-chip" aria-pressed="true" onClick=${() => toggle(c)}>${c}</button>`)}
    </div></fieldset>
    <h3>Prices, USD per million tokens</h3>
    <div class="form-grid">
      ${n('price_in_per_mtok', 'Input', '', { min: 0 })}${n('price_out_per_mtok', 'Output', '', { min: 0 })}
      ${n('price_in_peak_per_mtok', 'Peak input', '0 = one price all day', { min: 0 })}${n('price_out_peak_per_mtok', 'Peak output', '', { min: 0 })}
      ${n('price_in_batch_per_mtok', 'Batch input', '0 = half the input price', { min: 0 })}${n('price_out_batch_per_mtok', 'Batch output', '', { min: 0 })}
      ${n('price_cache_read_per_mtok', 'Cached input (read)', '0 = vendor default (Anthropic 0.1x input)', { min: 0 })}${n('price_cache_write_per_mtok', 'Cache write', '0 = vendor default (Anthropic 1.25x input)', { min: 0 })}
    </div>
    <h3>Limits</h3>
    <div class="form-grid">
      ${n('rpm', 'Requests / min per key', '0 = unlimited', { min: 0, step: 1 })}
      ${n('rpm_burst', 'Burst', '0 = rpm − 1, below 0 = none', { step: 1 })}
      ${n('queue_timeout_ms', 'Queue timeout, ms', '0 = fall back at once', { min: 0, step: 1 })}
      ${n('budget_grace_margin', 'Budget grace margin', '0 = default 0.05, below 0 = off')}
      ${n('daily_tokens_per_key', 'Daily tokens per key', '0 = unlimited', { min: 0, step: 1 })}
      ${n('daily_tokens_per_user', 'Daily tokens per user', '0 = unlimited', { min: 0, step: 1 })}
    </div>
    <${Check} name="enabled" label="Enabled: rules may route to it" defaultChecked=${m.enabled} />
  <//>`;
}
