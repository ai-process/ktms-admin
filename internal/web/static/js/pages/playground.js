import { html, useState, useRef, api, apiStream, useAsync, toast, PageHead, Card, Field, Check, LoadError, parseAttrs, cx } from '../lib.js';
import { loadCfg } from '../state.js';

const SAMPLE_TOOLS = [{
  type: 'function',
  function: {
    name: 'get_weather', description: 'Get the current weather for a city',
    parameters: { type: 'object', properties: { location: { type: 'string', description: 'City name, e.g. Paris' }, unit: { type: 'string', description: 'celsius or fahrenheit' } }, required: ['location'] },
  },
}];

// readSSE yields {event, data} records from the console's stream endpoint.
async function* readSSE(res) {
  const reader = res.body.getReader();
  const dec = new TextDecoder();
  let buf = '';
  for (;;) {
    const { done, value } = await reader.read();
    if (done) return;
    buf += dec.decode(value, { stream: true });
    const parts = buf.split('\n\n');
    buf = parts.pop();
    for (const part of parts) {
      let event = 'chunk', data = '';
      for (const line of part.split('\n')) {
        if (line.startsWith('event: ')) event = line.slice(7).trim();
        else if (line.startsWith('data: ')) data = line.slice(6).trim();
      }
      if (data) yield { event, data };
    }
  }
}

const metaLine = (m, ttft) => m && `${m.resolved_model || '…'} · ${m.resolved_vendor || '…'} · rule ${m.matched_rule || '(override)'}${ttft != null ? ` · first token ${ttft} ms` : ''}${m.usage ? ` · ${m.usage.input ?? 0} in / ${m.usage.output ?? 0} out tokens` : ''}`;

export function PlaygroundPage({ params }) {
  const cfg = useAsync(() => loadCfg(), []);
  const [opts, setOpts] = useState({
    model: params.get('model') || 'auto', effort: params.get('effort') || 'medium', attrs: params.get('attrs') || '',
    system: '', stream: true, tools: false, toolsJson: JSON.stringify(SAMPLE_TOOLS, null, 2),
  });
  const [prompt, setPrompt] = useState('');
  const [conv, setConv] = useState([]);
  const [live, setLive] = useState(null);
  const [busy, setBusy] = useState(false);
  const abort = useRef(null);
  const set = (p) => setOpts((o) => ({ ...o, ...p }));

  const tools = () => {
    if (!opts.tools || !opts.toolsJson.trim()) return undefined;
    const parsed = JSON.parse(opts.toolsJson);
    return Array.isArray(parsed) ? parsed : [parsed];
  };

  const turn = async (history) => {
    let toolDefs;
    try { toolDefs = tools(); } catch (e) { toast('Tools JSON is not valid: ' + e.message, true); return; }
    const payload = {
      model: opts.model, effort: opts.effort, system: opts.system, attributes: parseAttrs(opts.attrs), tools: toolDefs,
      messages: history.map((m) => ({ role: m.role, text: m.text || '', tool_call_id: m.tool_call_id, tool_calls: m.tool_calls })),
    };
    setBusy(true);
    abort.current = new AbortController();
    if (!opts.stream) {
      try {
        const r = await api('POST', '/api/proxy/playground', payload, abort.current.signal);
        setConv([...history, { role: 'assistant', text: (r.choices || []).join('\n---\n'), tool_calls: r.details?.[0]?.tool_calls, meta: r }]);
      } catch (e) { if (e.name !== 'AbortError' && e.status !== 401) toast(e.message, true); }
      setBusy(false);
      return;
    }
    const t0 = performance.now();
    let text = '', ttft = null, meta = null;
    const calls = {};
    setLive({ text: '', meta: null });
    try {
      const res = await apiStream('/api/proxy/playground/stream', payload, abort.current.signal);
      for await (const { event, data } of readSSE(res)) {
        const c = JSON.parse(data);
        if (event === 'error') throw new Error(c?.error?.message || 'Stream error');
        if (ttft == null) ttft = Math.round(performance.now() - t0);
        if (c.delta) text += c.delta;
        for (const tc of c.tool_call_chunks || []) {
          const i = tc.index || 0;
          calls[i] ||= { id: '', type: tc.type || 'function', function: { name: '', arguments: '' } };
          if (tc.id) calls[i].id = tc.id;
          if (tc.name) calls[i].function.name = tc.name;
          if (tc.arguments_delta) calls[i].function.arguments += tc.arguments_delta;
        }
        if (c.usage || c.resolved_model) meta = { ...meta, ...c };
        setLive({ text, meta, ttft });
      }
      setConv([...history, { role: 'assistant', text, tool_calls: Object.values(calls).length ? Object.values(calls) : undefined, meta, ttft }]);
    } catch (e) {
      if (e.name === 'AbortError') { if (text) setConv([...history, { role: 'assistant', text: text + ' [stopped]', meta, ttft }]); }
      else if (e.status !== 401) toast(e.message, true);
    } finally { setLive(null); setBusy(false); }
  };

  const send = async (e) => {
    e?.preventDefault();
    const text = prompt.trim();
    if (!text || busy) return;
    setPrompt('');
    const next = [...conv, { role: 'user', text }];
    setConv(next);
    await turn(next);
  };
  const reply = async (id, text) => {
    const next = [...conv, { role: 'tool', tool_call_id: id, text }];
    setConv(next);
    await turn(next);
  };
  const models = (cfg.data?.models || []).filter((m) => m.enabled);

  return html`
    <${PageHead} title="Playground" sub="Chat through the real rules: streaming, multi-turn tool calls, and the route each answer took." />
    <${LoadError} error=${cfg.error} retry=${cfg.reload} />
    <div class="row">
      <div style=${{ flex: '1 1 300px' }}>
        <${Card} title="Request">
          <${Field} label="Model"><select value=${opts.model} onChange=${(e) => set({ model: e.currentTarget.value })}>
            <option value="auto">auto: let the rules choose</option>${models.map((m) => html`<option value=${m.id}>${m.id}</option>`)}</select><//>
          <${Field} label="Effort" hint="Rules match on it when the model is auto."><select value=${opts.effort} onChange=${(e) => set({ effort: e.currentTarget.value })}>${['low', 'medium', 'high'].map((x) => html`<option value=${x}>${x}</option>`)}</select><//>
          <${Field} label="Attributes" hint="key=value, comma separated: what rules match on."><input value=${opts.attrs} placeholder="service=support-bot" onInput=${(e) => set({ attrs: e.currentTarget.value })} /><//>
          <${Field} label="System instruction"><textarea rows="3" value=${opts.system} onInput=${(e) => set({ system: e.currentTarget.value })} placeholder="Optional" /><//>
          <${Check} label="Stream the answer" checked=${opts.stream} onChange=${(e) => set({ stream: e.currentTarget.checked })} />
          <${Check} label="Offer tools" checked=${opts.tools} onChange=${(e) => set({ tools: e.currentTarget.checked })} />
          ${opts.tools && html`<${Field} label="Tool definitions (JSON)"><textarea class="code" rows="10" value=${opts.toolsJson} onInput=${(e) => set({ toolsJson: e.currentTarget.value })} /><//>
            <button type="button" class="sm" style=${{ alignSelf: 'flex-start' }} onClick=${() => set({ toolsJson: JSON.stringify(SAMPLE_TOOLS, null, 2) })}>Reset to the sample tool</button>`}
        <//>
      </div>
      <div style=${{ flex: '3 1 520px' }} class="stack">
        <${Card} title="Conversation" actions=${conv.length > 0 && html`<button type="button" class="sm" onClick=${() => setConv([])} disabled=${busy}>Clear</button>`}>
          <div class="chat" aria-live="polite">
            ${!conv.length && !live && html`<p class="muted">Send a message to see which rule and model answer it.</p>`}
            ${conv.map((m, i) => html`<${Message} key=${i} m=${m} answered=${(id) => conv.slice(i + 1).some((x) => x.role === 'tool' && x.tool_call_id === id)} reply=${reply} busy=${busy} />`)}
            ${live && html`<div class="msg"><span class="role">Assistant</span><div class="text">${live.text}<span class="cursor"></span></div><span class="meta">${metaLine(live.meta, live.ttft) || 'Connecting…'}</span></div>`}
          </div>
          <form class="stack" onSubmit=${send}>
            <textarea rows="3" value=${prompt} placeholder="Ask something… (Ctrl+Enter sends)" aria-label="Message" onInput=${(e) => setPrompt(e.currentTarget.value)}
              onKeyDown=${(e) => { if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) send(e); }} />
            <div class="cluster">
              <button type="submit" class="primary" disabled=${busy || !prompt.trim()}>Send</button>
              ${busy && html`<button type="button" class="danger" onClick=${() => abort.current?.abort()}>Stop</button>`}
            </div>
          </form>
        <//>
      </div>
    </div>
  `;
}

function Message({ m, answered, reply, busy }) {
  return html`<div class=${cx('msg', m.role)}>
    <span class="role">${m.role === 'tool' ? 'Tool output' : m.role}</span>
    ${m.text && html`<div class="text">${m.text}</div>`}
    ${(m.tool_calls || []).map((tc) => html`<${ToolCall} key=${tc.id} tc=${tc} done=${answered(tc.id)} reply=${reply} busy=${busy} />`)}
    ${m.meta && html`<span class="meta">${metaLine(m.meta, m.ttft)}</span>`}
  </div>`;
}

function ToolCall({ tc, done, reply, busy }) {
  const fn = tc.function || {};
  const [out, setOut] = useState(fn.name === 'get_weather' ? '{"temperature": 21, "condition": "sunny"}' : '');
  let args = fn.arguments || '';
  try { args = JSON.stringify(JSON.parse(args), null, 2); } catch { /* keep raw */ }
  return html`<div class="tool-call">
    <div class="cluster"><span>Calls <code>${fn.name || 'unknown'}</code></span>${tc.id && html`<span class="small muted mono">${tc.id}</span>`}</div>
    <pre>${args}</pre>
    ${!done && html`<div class="stack">
      <textarea class="code" rows="2" value=${out} aria-label="Tool response" placeholder="Tool response (JSON or text)" onInput=${(e) => setOut(e.currentTarget.value)} />
      <button type="button" class="sm primary" style=${{ alignSelf: 'flex-start' }} disabled=${busy || !out.trim()} onClick=${() => reply(tc.id, out.trim())}>Send tool output</button>
    </div>`}
  </div>`;
}
