import { html, useState, useEffect, useMemo, api, useAsync, confirmAction, toast, PageHead, Card, Seg, Field, Check, Badge, Icon, LoadError, Empty, EFFORTS, effortName, num, cx } from '../lib.js';
import { isAdmin, loadCfg } from '../state.js';

const LEVELS = ['EFFORT_LOW', 'EFFORT_MEDIUM', 'EFFORT_HIGH'];
const ATTR_HINTS = ['project', 'service', 'user_id', 'team_id', 'org_id', 'domain', 'language', 'subject', 'task', 'speed', 'quality', 'data', 'modality', 'canary_key'];
const RESERVED = new Set(['shadow', 'is_shadow', 'shadow_eval']);
const SWATCH = ['var(--series-1)', 'var(--series-3)', 'var(--series-2)', 'var(--series-4)', 'var(--series-5)'];

const blankRule = (rules) => ({
  name: '', priority: (rules.reduce((m, r) => Math.max(m, num(r.priority)), 0) || 0) + 10,
  effort: 'EFFORT_UNSPECIFIED', attributes: {}, use: [], weights: [], enabled: true,
  shadow_model: '', shadow_percentage: 0, shadow_eval: false,
});

const toDraft = (r) => ({
  ...r,
  effort: r.effort || 'EFFORT_UNSPECIFIED',
  conds: Object.entries(r.attributes || {}).map(([key, value]) => ({ key, value })),
  chain: (r.use || []).map((id, i) => ({ id, weight: r.weights?.length === r.use.length ? num(r.weights[i]) : 0 })),
  split: (r.weights || []).length > 0 && (r.use || []).length > 1,
  shadow_model: r.shadow_model || '', shadow_percentage: num(r.shadow_percentage), shadow_eval: !!r.shadow_eval,
});

const fromDraft = (d) => {
  const attributes = {};
  for (const c of d.conds) if (c.key.trim()) attributes[c.key.trim()] = c.value.trim();
  const use = d.chain.map((m) => m.id);
  return {
    name: d.name.trim(), priority: num(d.priority), effort: d.effort, attributes, use,
    weights: d.split && use.length > 1 ? d.chain.map((m) => Math.round(num(m.weight))) : [],
    enabled: d.enabled, shadow_model: d.shadow_model, shadow_percentage: d.shadow_model ? num(d.shadow_percentage) : 0,
    shadow_eval: !!d.shadow_model && d.shadow_eval,
  };
};

const efforts = (r) => (!r.effort || r.effort === 'EFFORT_UNSPECIFIED' ? LEVELS : [r.effort]);
const serves = (m, e) => (m?.efforts || []).includes(e);

// problems mirrors the gateway's own checks so the editor can say what is
// wrong before a save is refused.
function problems(rules, byId) {
  const out = [];
  const names = new Set(), prios = new Set();
  const catchAll = new Set();
  for (const r of rules) {
    if (!r.name) out.push('Every rule needs a name.');
    else if (names.has(r.name)) out.push(`Two rules are named ${r.name}.`);
    names.add(r.name);
    if (prios.has(r.priority)) out.push(`Two rules share priority ${r.priority}.`);
    prios.add(r.priority);
    if (!r.use.length) { out.push(`${r.name || 'This rule'} sends requests nowhere: add a model.`); continue; }
    const unknown = r.use.filter((id) => !byId[id]);
    if (unknown.length) out.push(`${r.name}: unknown model ${unknown.join(', ')}.`);
    if (r.weights.length && r.weights.some((w) => !(w > 0))) out.push(`${r.name}: every split weight must be above 0.`);
    if (r.shadow_percentage < 0 || r.shadow_percentage > 100) out.push(`${r.name}: shadow share must be 0–100%.`);
    for (const k of Object.keys(r.attributes)) if (RESERVED.has(k)) out.push(`${r.name}: attribute ${k} is reserved.`);
    if (!r.enabled) continue;
    const live = r.use.map((id) => byId[id]).filter((m) => m?.enabled);
    if (!live.length) { out.push(`${r.name}: every model in the chain is disabled.`); continue; }
    for (const e of efforts(r)) {
      if (!live.some((m) => serves(m, e))) out.push(`${r.name}: no enabled model serves ${effortName(e)} effort.`);
      if (!Object.keys(r.attributes).length) catchAll.add(e);
    }
  }
  if (rules.length) for (const e of LEVELS) if (!catchAll.has(e)) out.push(`No catch-all rule (one without conditions) covers ${effortName(e)} effort.`);
  return [...new Set(out)];
}

export function RulesPage() {
  const st = useAsync(() => loadCfg(true), []);
  const models = st.data?.models || [];
  const byId = useMemo(() => Object.fromEntries(models.map((m) => [m.id, m])), [models]);
  const rules = useMemo(() => [...(st.data?.rules || [])].sort((a, b) => b.priority - a.priority), [st.data]);
  const [sel, setSel] = useState(null);
  const [draft, setDraft] = useState(null);
  const [dirty, setDirty] = useState(false);
  const editable = isAdmin();

  useEffect(() => {
    if (!st.data) return;
    const keep = sel === '__new' ? null : rules.find((r) => r.name === sel) || rules[0];
    if (sel !== '__new' || !draft) { setSel(keep ? keep.name : '__new'); setDraft(toDraft(keep || blankRule(rules))); setDirty(false); }
  }, [st.data]);

  const keepEdits = async () => dirty && !(await confirmAction({ title: 'Discard changes?', text: 'This rule has unsaved edits.', confirm: 'Discard', action: async () => {} }));
  const choose = async (name) => {
    if (name === sel) return;
    if (await keepEdits()) return;
    const r = rules.find((x) => x.name === name);
    setSel(name); setDraft(toDraft(r || blankRule(rules))); setDirty(false);
  };
  const newRule = async () => {
    if (await keepEdits()) return;
    setSel('__new'); setDraft(toDraft(blankRule(rules))); setDirty(true);
  };
  const patch = (p) => { setDraft((d) => ({ ...d, ...p })); setDirty(true); };

  const save = async (next, msg, keepName) => {
    await api('PUT', '/api/proxy/rules', { rules: next });
    toast(msg);
    setDirty(false);
    if (keepName !== undefined) setSel(keepName);
    st.reload();
  };

  // Dragging a card swaps priorities only: the set of numbers stays, so the
  // order changes without renumbering rules nobody touched.
  const reorder = async (from, to) => {
    if (from === to) return;
    const order = rules.map((r) => r.name);
    const [moved] = order.splice(from, 1);
    order.splice(to, 0, moved);
    const prios = rules.map((r) => r.priority);
    const next = order.map((n, i) => ({ ...rules.find((r) => r.name === n), priority: prios[i] }));
    try { await save(next, 'Order saved', sel); } catch (e) { toast(e.message, true); }
  };

  return html`
    <${PageHead} title="Rules" sub="Requests are matched top to bottom; the first rule that fits decides where they go.">
      ${editable && html`<button type="button" class="primary" onClick=${newRule}>New rule</button>`}
    <//>
    <${LoadError} error=${st.error} retry=${st.reload} />
    ${st.loading && !st.data && html`<div class="rules-layout"><div class="rule-list">${[1, 2, 3].map((i) => html`<div key=${i} class="skeleton" style=${{ height: '92px', borderRadius: '12px' }}></div>`)}</div><div class="rule-editor skeleton" style=${{ height: '520px', borderRadius: '14px' }}></div></div>`}
    ${st.data && html`<div class="rules-layout">
      <${RuleList} rules=${rules} sel=${sel} choose=${choose} reorder=${editable && !dirty && reorder} canReorder=${editable} byId=${byId} draftNew=${sel === '__new' && draft} />
      ${draft && html`<${Editor} key=${sel} draft=${draft} patch=${patch} isNew=${sel === '__new'} rules=${rules} models=${models} byId=${byId}
        dirty=${dirty} editable=${editable} save=${save} discard=${() => { const r = rules.find((x) => x.name === sel); setDraft(toDraft(r || blankRule(rules))); setDirty(false); }} />`}
    </div>`}
    <p class="small muted">Speech, image and judgment calls route at low effort with the reserved attribute <code>modality</code> = tts, image or judge.</p>
  `;
}

function chainText(r) {
  if (!r.use?.length) return 'Nowhere yet';
  const w = r.weights?.length === r.use.length ? r.weights : null;
  if (!w) return r.use.join(' → ');
  const total = w.reduce((a, b) => a + num(b), 0) || 1;
  return r.use.map((id, i) => `${id} ${Math.round((num(w[i]) / total) * 100)}%`).join(' · ');
}

function RuleList({ rules, sel, choose, reorder, canReorder, byId, draftNew }) {
  const [drag, setDrag] = useState(null);
  const [over, setOver] = useState(null);
  const card = (r, i, isNew) => {
    const conds = Object.entries(r.attributes || {});
    const off = r.use?.some((id) => !byId[id]?.enabled);
    return html`<button type="button" key=${isNew ? '__new' : r.name} class=${cx('rule-card', drag === i && 'dragging', over === i && drag !== null && drag !== i && 'drop-before')}
      aria-current=${(isNew ? sel === '__new' : r.name === sel) ? 'true' : 'false'} onClick=${() => choose(isNew ? '__new' : r.name)}
      draggable=${!!reorder && !isNew} onDragStart=${() => setDrag(i)} onDragEnd=${() => { setDrag(null); setOver(null); }}
      onDragOver=${(e) => { if (drag !== null && !isNew) { e.preventDefault(); setOver(i); } }}
      onDrop=${(e) => { e.preventDefault(); if (drag !== null && reorder && !isNew) reorder(drag, i); setDrag(null); setOver(null); }}>
      <div class="top"><span class="mono bold">${r.name || 'new rule'}</span><span class="small muted">${isNew ? 'unsaved' : 'priority ' + r.priority}</span></div>
      <div class="cluster">
        <span class="chip">${r.effort && r.effort !== 'EFFORT_UNSPECIFIED' ? 'effort: ' + effortName(r.effort) : 'any effort'}</span>
        ${conds.length ? conds.map(([k, v]) => html`<span class="chip mono" key=${k}>${k}=${v}</span>`) : html`<span class="chip">catch-all</span>`}
        ${!r.enabled && html`<${Badge}>disabled<//>`}
        ${off && r.enabled && html`<${Badge} tone="warn">disabled model<//>`}
      </div>
      <div class="chain-text">${chainText(r)}</div>
    </button>`;
  };
  return html`<section class="rule-list" aria-label="Rules in priority order">
    ${draftNew && card(fromDraft(draftNew), -1, true)}
    ${rules.map((r, i) => card(r, i, false))}
    ${!rules.length && !draftNew && html`<div class="card"><${Empty} title="No rules yet">Start with a catch-all rule: no conditions, any effort.<//></div>`}
    ${canReorder && rules.length > 1 && html`<p class="small muted">${reorder ? 'Drag a card to change its priority. The editor\u2019s Move buttons do the same from the keyboard.' : 'Save or discard the open edits to reorder rules.'}</p>`}
  </section>`;
}

function Editor({ draft: d, patch, isNew, rules, models, byId, dirty, editable, save, discard }) {
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);
  const [dragI, setDragI] = useState(null);
  const ro = !editable;
  const rule = fromDraft(d);
  const others = rules.filter((r) => isNew || r.name !== d.name);
  const next = [...others, rule].sort((a, b) => b.priority - a.priority);
  const issues = problems(next, byId);
  const weightSum = d.chain.reduce((a, m) => a + num(m.weight), 0);
  const idx = rules.findIndex((r) => r.name === d.name);

  const setCond = (i, p) => patch({ conds: d.conds.map((c, k) => (k === i ? { ...c, ...p } : c)) });
  const setChain = (chain) => patch({ chain });
  const move = (i, j) => { if (j < 0 || j >= d.chain.length) return; const c = d.chain.slice(); const [x] = c.splice(i, 1); c.splice(j, 0, x); setChain(c); };
  const weigh = (i, w) => setChain(d.chain.map((m, k) => (k === i ? { ...m, weight: Math.max(0, Math.min(1000, Math.round(num(w)))) } : m)));
  const toggleSplit = () => {
    const on = !d.split;
    const even = Math.floor(100 / Math.max(1, d.chain.length));
    patch({ split: on, chain: on ? d.chain.map((m, i) => ({ ...m, weight: m.weight > 0 ? m.weight : i === 0 ? 100 - even * (d.chain.length - 1) : even })) : d.chain });
  };
  const add = (id) => setChain([...d.chain, { id, weight: d.split ? 10 : 0 }]);
  const inChain = new Set(d.chain.map((m) => m.id));
  const addable = models.filter((m) => !inChain.has(m.id));

  const doSave = async () => {
    setErr('');
    if (issues.length) { setErr(issues[0]); return; }
    setBusy(true);
    try { await save(next, isNew ? 'Rule created' : 'Rule saved', rule.name); } catch (e) { setErr(e.message); } finally { setBusy(false); }
  };
  const doDelete = () => confirmAction({
    title: `Delete ${d.name}?`, text: 'The remaining rules must still cover every effort with a catch-all.',
    action: () => save(rules.filter((r) => r.name !== d.name), 'Rule deleted', null),
  });
  const moveRule = async (dir) => {
    const j = idx + dir;
    if (j < 0 || j >= rules.length) return;
    const a = rules[idx], b = rules[j];
    try { await save(rules.map((r) => (r === a ? { ...a, priority: b.priority } : r === b ? { ...b, priority: a.priority } : r)), 'Order saved', d.name); } catch (e) { setErr(e.message); }
  };

  // The preview reads the draft as the router would: split first, then
  // fallbacks in order; a split only picks who goes first.
  const preview = [];
  const live = d.chain.filter((m) => byId[m.id]?.enabled !== false);
  if (d.split && live.length > 1) {
    preview.push('Split by weight: ' + live.map((m) => `${m.id} ${Math.round((num(m.weight) / (weightSum || 1)) * 100)}%`).join(', ') + '.');
    preview.push('Whichever is picked goes first; if it fails or is paused, the others follow in the listed order.');
  } else if (live.length) {
    preview.push(`First to ${live[0].id}.`);
    live.slice(1).forEach((m) => preview.push(`If it fails or is paused, to ${m.id}.`));
  }
  if (!preview.length) preview.push('Nowhere yet: add a model.');
  if (d.shadow_model) preview.push(`${num(d.shadow_percentage)}% of requests are also mirrored to ${d.shadow_model}${d.shadow_eval ? ', and a judge compares the answers' : ''}; callers never see the shadow answer.`);
  const condText = rule.attributes && Object.keys(rule.attributes).length ? ' and ' + Object.entries(rule.attributes).map(([k, v]) => `${k}=${v}`).join(', ') : '';
  const pgHref = '#/playground?' + new URLSearchParams({ effort: d.effort === 'EFFORT_UNSPECIFIED' ? 'medium' : effortName(d.effort), attrs: Object.entries(rule.attributes).map(([k, v]) => `${k}=${v}`).join(',') });

  return html`<section class="card rule-editor" aria-label="Edit rule">
    <div class="card-head">
      <div class="stack" style=${{ gap: '4px' }}>
        <span class="small muted">${isNew ? 'New rule' : 'Editing rule'}</span>
        ${isNew
          ? html`<input aria-label="Rule name" class="mono" value=${d.name} placeholder="support-chat" onInput=${(e) => patch({ name: e.currentTarget.value })} />`
          : html`<span class="mono bold" style=${{ fontSize: '16px' }}>${d.name}</span>`}
      </div>
      <div class="cluster">
        <label class="field inline small">Priority <input type="number" style=${{ width: '90px' }} value=${d.priority} disabled=${ro} onInput=${(e) => patch({ priority: num(e.currentTarget.value) })} /></label>
        ${!isNew && editable && html`<button type="button" class="icon sm" aria-label="Move up" title="Move up" disabled=${idx <= 0 || dirty} onClick=${() => moveRule(-1)}><${Icon} name="up" /></button>
          <button type="button" class="icon sm" aria-label="Move down" title="Move down" disabled=${idx >= rules.length - 1 || dirty} onClick=${() => moveRule(1)}><${Icon} name="down" /></button>`}
        <${Check} label="Enabled" checked=${d.enabled} disabled=${ro} onChange=${(e) => patch({ enabled: e.currentTarget.checked })} />
      </div>
    </div>
    <div class="card-body" style=${{ gap: '26px' }}>
      <div class="stack">
        <div class="step-title"><span class="n">1</span><h2>When a request matches</h2></div>
        <div class="cluster"><span class="small" style=${{ width: '64px' }}>Effort</span>
          <${Seg} label="Effort" value=${d.effort} options=${EFFORTS} onChange=${(v) => !ro && patch({ effort: v })} /></div>
        <datalist id="attr-keys">${ATTR_HINTS.map((k) => html`<option value=${k} />`)}</datalist>
        ${d.conds.map((c, i) => html`<div class="cond-row" key=${i}>
          <span class="joiner">${i === 0 ? 'and' : 'and also'}</span>
          <${Field} label="Attribute" error=${RESERVED.has(c.key.trim()) ? 'Reserved for shadow routing' : ''}><input list="attr-keys" value=${c.key} disabled=${ro} style=${{ width: '160px' }} aria-invalid=${RESERVED.has(c.key.trim()) ? 'true' : undefined} onInput=${(e) => setCond(i, { key: e.currentTarget.value })} /><//>
          <span class="is">is</span>
          <${Field} label="Value"><input value=${c.value} disabled=${ro} style=${{ width: '180px' }} onInput=${(e) => setCond(i, { value: e.currentTarget.value })} /><//>
          ${!ro && html`<button type="button" class="icon" aria-label="Remove condition" onClick=${() => patch({ conds: d.conds.filter((_, k) => k !== i) })}><${Icon} name="x" /></button>`}
        </div>`)}
        ${!ro && html`<button type="button" class="dashed sm" style=${{ alignSelf: 'flex-start' }} onClick=${() => patch({ conds: [...d.conds, { key: d.conds.length ? '' : 'project', value: '' }] })}>+ Add condition</button>`}
        ${!d.conds.length && html`<p class="small muted">No conditions: this rule catches every request at ${d.effort === 'EFFORT_UNSPECIFIED' ? 'any effort' : effortName(d.effort) + ' effort'}.</p>`}
      </div>

      <div class="stack">
        <div class="step-title"><span class="n">2</span><h2>Send it to</h2>
          ${d.chain.length > 1 && html`<span class="right"><${Check} label="Split traffic (canary)" checked=${d.split} disabled=${ro} onChange=${toggleSplit} /></span>`}</div>
        ${d.split && d.chain.length > 1 && html`<div class="split-bar" aria-label="Traffic split">${d.chain.map((m, i) => html`<div key=${m.id} style=${{ width: (weightSum ? (num(m.weight) / weightSum) * 100 : 0) + '%', background: SWATCH[i % SWATCH.length] }}></div>`)}</div>`}
        <ol class="chain">
          ${d.chain.map((m, i) => {
            const mm = byId[m.id];
            const warn = !mm ? 'unknown model' : !mm.enabled ? 'disabled' : efforts(rule).some((e) => !serves(mm, e)) ? 'does not serve ' + efforts(rule).filter((e) => !serves(mm, e)).map(effortName).join(', ') : '';
            return html`<li key=${m.id} class=${cx(dragI === i && 'dragging')} draggable=${!ro}
              onDragStart=${() => setDragI(i)} onDragEnd=${() => setDragI(null)} onDragOver=${(e) => dragI !== null && e.preventDefault()}
              onDrop=${(e) => { e.preventDefault(); if (dragI !== null) move(dragI, i); setDragI(null); }}>
              ${!ro && html`<span class="handle" aria-hidden="true"><${Icon} name="grip" /></span>`}
              <span class="swatch" style=${{ background: SWATCH[i % SWATCH.length] }}></span>
              <span class="role">${d.split ? 'Split' : i === 0 ? 'First' : 'Fallback'}</span>
              <div class="model"><span class="mono">${m.id}</span><span class="small muted">${mm ? `${mm.vendor} · $${num(mm.price_in_per_mtok)} / $${num(mm.price_out_per_mtok)} per Mtok` : ''}${warn && html` · <span class="err-text">${warn}</span>`}</span></div>
              ${d.split && html`<div class="weight">
                <button type="button" class="icon" aria-label=${'Less traffic to ' + m.id} disabled=${ro} onClick=${() => weigh(i, num(m.weight) - 5)}>−</button>
                <input type="number" min="1" aria-label=${'Weight of ' + m.id} value=${m.weight} disabled=${ro} onInput=${(e) => weigh(i, e.currentTarget.value)} />
                <button type="button" class="icon" aria-label=${'More traffic to ' + m.id} disabled=${ro} onClick=${() => weigh(i, num(m.weight) + 5)}>+</button>
                <span class="small muted tabular" style=${{ width: '40px' }}>${weightSum ? Math.round((num(m.weight) / weightSum) * 100) : 0}%</span>
              </div>`}
              ${!ro && html`<div class="cluster" style=${{ gap: '4px' }}>
                <button type="button" class="icon" aria-label=${'Move ' + m.id + ' up'} disabled=${i === 0} onClick=${() => move(i, i - 1)}><${Icon} name="up" /></button>
                <button type="button" class="icon" aria-label=${'Move ' + m.id + ' down'} disabled=${i === d.chain.length - 1} onClick=${() => move(i, i + 1)}><${Icon} name="down" /></button>
                <button type="button" class="icon" aria-label=${'Remove ' + m.id} onClick=${() => setChain(d.chain.filter((_, k) => k !== i))}><${Icon} name="x" /></button>
              </div>`}
            </li>`;
          })}
        </ol>
        ${!d.chain.length && html`<p class="small muted">Pick the models below, cheapest first: later ones take over when earlier ones fail or are rate limited.</p>`}
        ${!ro && addable.length > 0 && html`<div class="cluster">${addable.map((m) => html`<button type="button" key=${m.id} class="dashed sm pill" title=${m.enabled ? m.vendor : m.vendor + ' (disabled)'} onClick=${() => add(m.id)}>+ ${m.id}</button>`)}</div>`}
        ${d.split && d.chain.some((m) => !(num(m.weight) > 0)) && html`<p class="notice warn">Every model in a split needs a weight above 0.</p>`}
        <details>
          <summary class="small" style=${{ cursor: 'pointer' }}>Shadow traffic${d.shadow_model ? `: ${d.shadow_model}, ${num(d.shadow_percentage)}%` : ''}</summary>
          <div class="form-grid" style=${{ marginTop: '12px' }}>
            <${Field} label="Mirror to" hint="Gets a copy of the request; its answer is only measured."><select value=${d.shadow_model} disabled=${ro} onChange=${(e) => patch({ shadow_model: e.currentTarget.value })}>
              <option value="">No shadow</option>${models.map((m) => html`<option value=${m.id}>${m.id}</option>`)}</select><//>
            <${Field} label="Share of requests, %"><input type="number" min="0" max="100" step="any" value=${d.shadow_percentage} disabled=${ro || !d.shadow_model} onInput=${(e) => patch({ shadow_percentage: num(e.currentTarget.value) })} /><//>
            <${Check} label="Judge the shadow against the primary" checked=${d.shadow_eval} disabled=${ro || !d.shadow_model} onChange=${(e) => patch({ shadow_eval: e.currentTarget.checked })} />
          </div>
        </details>
      </div>

      <div class="stack">
        <div class="step-title"><span class="n">3</span><h2>Check it</h2></div>
        <div class="preview">
          <div class="small">A request with effort <strong>${effortName(d.effort)}</strong>${condText} goes:</div>
          <ol>${preview.map((p, i) => html`<li key=${i}>${p}</li>`)}</ol>
          ${isAdmin() && !isNew && !dirty && html`<a class="btn sm" href=${pgHref} style=${{ alignSelf: 'flex-start' }}>Send a test prompt in Playground</a>`}
          ${isAdmin() && (isNew || dirty) && html`<span class="small muted">Save the rule to test it in Playground.</span>`}
        </div>
      </div>
    </div>
    <div class="card-foot">
      <span class=${cx('small', issues.length ? 'err-text' : 'ok-text')} role="status">${err || (issues.length ? issues[0] : 'Every effort is still covered by a catch-all rule.')}</span>
      ${editable && html`<div class="cluster">
        ${!isNew && html`<button type="button" class="danger" onClick=${doDelete}>Delete</button>`}
        <button type="button" onClick=${discard} disabled=${!dirty}>Discard</button>
        <button type="button" class="primary" onClick=${doSave} disabled=${busy || !dirty}>${busy ? 'Saving…' : isNew ? 'Create rule' : 'Save rule'}</button>
      </div>`}
    </div>
  </section>`;
}
