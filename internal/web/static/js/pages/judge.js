import { html, useState, api, useAsync, toast, PageHead, Card, Field, Seg, Badge, Meter, LoadError, Icon, parseAttrs } from '../lib.js';
import { loadCfg } from '../state.js';

const SAMPLE_STATE = {
  conversation: [
    { role: 'user', text: 'Can I cancel my subscription and get a refund?' },
    { role: 'agent', text: 'I have cancelled your subscription and initiated a full refund of $49 to your card.' },
  ],
  customer_tier: 'premium', days_active: 14,
};
const SAMPLE_QUESTIONS = [
  { id: 'refund_confirmed', type: 'NOUL', instructions: 'Did the agent explicitly confirm that a refund was initiated?', true_criteria: 'The agent confirmed initiating the refund.', false_criteria: 'The agent did not confirm, or refused.', options: [], levels: [] },
  { id: 'customer_sentiment', type: 'CHOICE', instructions: 'What is the customer sentiment after this interaction?', options: [['satisfied', 'Got an immediate resolution and refund'], ['neutral', 'A routine exchange without emotion'], ['frustrated', 'Problem unsolved or slow']], levels: [] },
  { id: 'resolution_quality', type: 'SCORE', instructions: 'How complete and helpful is the agent response?', options: [], levels: ['Unhelpful or inaccurate', 'Partial help, missing details', 'Adequate and clear', 'Exemplary, with next steps'] },
];
const TYPES = [['NOUL', 'Yes / no'], ['CHOICE', 'Choice'], ['SCORE', 'Score']];
const typeOf = (t) => ({ 1: 'NOUL', 2: 'CHOICE', 3: 'SCORE', JUDGE_TYPE_NOUL: 'NOUL', JUDGE_TYPE_CHOICE: 'CHOICE', JUDGE_TYPE_SCORE: 'SCORE' }[t] || 'DECISION');
const p100 = (x) => (x * 100).toFixed(1) + '%';

export function JudgePage() {
  const cfg = useAsync(() => loadCfg(), []);
  const [model, setModel] = useState('auto');
  const [attrs, setAttrs] = useState('');
  const [state, setState] = useState(JSON.stringify(SAMPLE_STATE, null, 2));
  const [qs, setQs] = useState(SAMPLE_QUESTIONS);
  const [res, setRes] = useState(null);
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);
  const judges = (cfg.data?.models || []).filter((m) => m.enabled && (m.capabilities || []).includes('judge'));
  const setQ = (i, p) => setQs(qs.map((q, k) => (k === i ? { ...q, ...p } : q)));

  const evaluate = async () => {
    let parsed;
    try { parsed = JSON.parse(state); } catch (e) { setErr('State must be valid JSON: ' + e.message); return; }
    const questions = {};
    for (const q of qs) {
      const id = q.id.trim();
      if (!id) continue;
      questions[id] = {
        type: q.type, instructions: q.instructions,
        options: q.type === 'CHOICE' ? Object.fromEntries(q.options.filter(([k]) => k.trim()).map(([k, v]) => [k.trim(), v])) : undefined,
        levels: q.type === 'SCORE' ? q.levels.filter((l) => l.trim()) : undefined,
        true_criteria: q.type === 'NOUL' ? q.true_criteria : undefined,
        false_criteria: q.type === 'NOUL' ? q.false_criteria : undefined,
      };
    }
    if (!Object.keys(questions).length) { setErr('Add at least one criterion with an id.'); return; }
    setErr(''); setBusy(true);
    try { setRes(await api('POST', '/api/proxy/judge', { state_json: parsed, model_override: model === 'auto' ? '' : model, attributes: parseAttrs(attrs), questions })); }
    catch (e) { if (e.status !== 401) setErr(e.message); }
    finally { setBusy(false); }
  };
  const format = () => { try { setState(JSON.stringify(JSON.parse(state), null, 2)); } catch (e) { toast('Not valid JSON: ' + e.message, true); } };

  return html`
    <${PageHead} title="Judge" sub="Score a state against calibrated criteria: yes/no probabilities, choices and ordered scores." />
    <${LoadError} error=${cfg.error} retry=${cfg.reload} />
    <div class="row">
      <div style=${{ flex: '3 1 520px' }} class="stack" >
        <${Card} title="State" actions=${html`<button type="button" class="sm" onClick=${format}>Format</button><button type="button" class="sm" onClick=${() => setState(JSON.stringify(SAMPLE_STATE, null, 2))}>Load sample</button>`}>
          <div class="form-grid">
            <${Field} label="Model"><select value=${model} onChange=${(e) => setModel(e.currentTarget.value)}>
              <option value="auto">auto: low effort, modality=judge</option>${judges.map((m) => html`<option value=${m.id}>${m.id} (${m.vendor})</option>`)}</select><//>
            <${Field} label="Attributes" hint="key=value, comma separated"><input value=${attrs} placeholder="env=prod" onInput=${(e) => setAttrs(e.currentTarget.value)} /><//>
          </div>
          <${Field} label="State (JSON)"><textarea class="code" rows="10" value=${state} onInput=${(e) => setState(e.currentTarget.value)} /><//>
        <//>
        <${Card} title="Criteria" actions=${html`<button type="button" class="sm dashed" onClick=${() => setQs([...qs, { id: 'criterion_' + (qs.length + 1), type: 'NOUL', instructions: '', true_criteria: '', false_criteria: '', options: [], levels: [] }])}>+ Add criterion</button>`}>
          ${qs.map((q, i) => html`<${Criterion} key=${i} q=${q} n=${i + 1} set=${(p) => setQ(i, p)} remove=${() => setQs(qs.filter((_, k) => k !== i))} />`)}
          ${!qs.length && html`<p class="muted">No criteria yet.</p>`}
        <//>
        <div class="cluster"><button type="button" class="primary" onClick=${evaluate} disabled=${busy}>${busy ? 'Evaluating…' : 'Evaluate'}</button>${err && html`<span class="err-text small" role="alert">${err}</span>`}</div>
      </div>
      <div style=${{ flex: '2 1 340px' }} class="stack" aria-live="polite">
        ${!res && html`<${Card} title="Results"><p class="muted">Results appear here, one card per criterion.</p><//>`}
        ${res && Object.entries(res.answers || {}).map(([id, a]) => html`<${Answer} key=${id} id=${id} a=${a} />`)}
        ${res && html`<p class="small muted">Judged by ${res.resolved_model} · ${res.resolved_vendor} · rule ${res.matched_rule || '(override)'} · ${res.usage?.input ?? 0} in / ${res.usage?.output ?? 0} out tokens</p>`}
      </div>
    </div>
  `;
}

function Criterion({ q, n, set, remove }) {
  const setOpt = (i, k, v) => set({ options: q.options.map((o, j) => (j === i ? [k, v] : o)) });
  const setLvl = (i, v) => set({ levels: q.levels.map((l, j) => (j === i ? v : l)) });
  return html`<div class="tool-call">
    <div class="cluster" style=${{ justifyContent: 'space-between' }}>
      <div class="cluster"><b>#${n}</b><input aria-label="Criterion id" class="mono" value=${q.id} onInput=${(e) => set({ id: e.currentTarget.value })} /><${Seg} label="Type" value=${q.type} options=${TYPES} onChange=${(type) => set({ type })} /></div>
      <button type="button" class="icon" aria-label=${'Remove criterion ' + n} onClick=${remove}><${Icon} name="x" /></button>
    </div>
    <${Field} label="Instructions"><textarea rows="2" value=${q.instructions} onInput=${(e) => set({ instructions: e.currentTarget.value })} /><//>
    ${q.type === 'NOUL' && html`<div class="form-grid">
      <${Field} label="A yes means (optional)"><input value=${q.true_criteria} onInput=${(e) => set({ true_criteria: e.currentTarget.value })} /><//>
      <${Field} label="A no means (optional)"><input value=${q.false_criteria} onInput=${(e) => set({ false_criteria: e.currentTarget.value })} /><//>
    </div>`}
    ${q.type === 'CHOICE' && html`<fieldset><legend>Options</legend><div class="stack" style=${{ gap: '6px' }}>
      ${q.options.map(([k, v], i) => html`<div class="cluster" key=${i} style=${{ flexWrap: 'nowrap' }}>
        <input aria-label="Option key" class="mono" style=${{ width: '140px' }} value=${k} onInput=${(e) => setOpt(i, e.currentTarget.value, v)} />
        <input aria-label="Option meaning" style=${{ flex: 1 }} value=${v} onInput=${(e) => setOpt(i, k, e.currentTarget.value)} />
        <button type="button" class="icon" aria-label="Remove option" onClick=${() => set({ options: q.options.filter((_, j) => j !== i) })}><${Icon} name="x" /></button></div>`)}
      <button type="button" class="sm dashed" style=${{ alignSelf: 'flex-start' }} onClick=${() => set({ options: [...q.options, ['option_' + (q.options.length + 1), '']] })}>+ Add option</button>
    </div></fieldset>`}
    ${q.type === 'SCORE' && html`<fieldset><legend>Levels, lowest first</legend><div class="stack" style=${{ gap: '6px' }}>
      ${q.levels.map((l, i) => html`<div class="cluster" key=${i} style=${{ flexWrap: 'nowrap' }}><span class="mono small">${i + 1}.</span>
        <input aria-label=${'Level ' + (i + 1)} style=${{ flex: 1 }} value=${l} onInput=${(e) => setLvl(i, e.currentTarget.value)} />
        <button type="button" class="icon" aria-label="Remove level" onClick=${() => set({ levels: q.levels.filter((_, j) => j !== i) })}><${Icon} name="x" /></button></div>`)}
      <button type="button" class="sm dashed" style=${{ alignSelf: 'flex-start' }} onClick=${() => set({ levels: [...q.levels, ''] })}>+ Add level</button>
    </div></fieldset>`}
  </div>`;
}

function Answer({ id, a }) {
  const t = typeOf(a.type);
  const conf = a.confidence ? html`<span class="small muted">confidence ${p100(a.confidence)}</span>` : null;
  const dist = (entries, pick, label) => html`<div class="dist">${entries.map(([k, p]) => html`
    <span class=${'label' + (k === pick ? ' bold mono' : '')} title=${label(k)}>${label(k)}</span><${Meter} value=${p} tone=${k === pick ? 'ok' : ''} /><span class="small tabular">${p100(p)}</span>`)}</div>`;
  return html`<${Card} title=${id} actions=${html`<${Badge}>${Object.fromEntries(TYPES)[t] || t}<//>`}>
    ${t === 'NOUL' && html`<div class="big-val">${p100(a.noul ?? 0)}<${Badge} tone=${(a.noul ?? 0) >= 0.5 ? 'ok' : 'warn'}>${(a.noul ?? 0) >= 0.5 ? 'Yes' : 'No'}<//>${conf}</div>
      <${Meter} value=${a.noul ?? 0} tone=${(a.noul ?? 0) >= 0.5 ? 'ok' : (a.noul ?? 0) >= 0.2 ? 'warn' : 'bad'} />`}
    ${t === 'CHOICE' && html`<div class="big-val"><${Badge} tone="ok">${a.choice || 'none'}<//>${conf}</div>
      ${a.probabilities && dist(Object.entries(a.probabilities), a.choice, (k) => k)}`}
    ${t === 'SCORE' && html`<div class="big-val">${(a.score ?? 0).toFixed(2)}<span class="small muted">score</span>${conf}</div>
      ${a.probabilities && dist(Object.entries(a.probabilities), null, (k) => a.legend?.[k] || 'Level ' + k)}`}
  <//>`;
}
