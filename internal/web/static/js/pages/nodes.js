import { html, useEffect, api, useAsync, openDialog, confirmAction, showSecret, FormDialog, PageHead, Card, Table, Field, Badge, Meter, Icon, LoadError, Tile, when, ago, fmt, num, splitList } from '../lib.js';
import { isAdmin } from '../state.js';

const STATE = {
  NODE_STATE_HEALTHY: ['Healthy', 'ok'], NODE_STATE_SUSPECT: ['Suspect', 'warn'], NODE_STATE_DRAINING: ['Draining', 'warn'],
  NODE_STATE_CIRCUIT_BROKEN: ['Circuit open', 'bad'], NODE_STATE_OFFLINE: ['Offline', ''],
};
const stateOf = (s) => STATE[s] || [String(s || 'unknown').replace('NODE_STATE_', '').toLowerCase(), ''];

export function NodesPage() {
  const st = useAsync(async () => {
    const [nodes, keys] = await Promise.all([api('GET', '/api/proxy/nodes'), api('GET', '/api/proxy/node-keys')]);
    return { nodes: nodes?.nodes || [], keys: keys?.keys || [] };
  }, []);
  // Heartbeats arrive every few seconds; a slow refresh keeps the page live.
  useEffect(() => { const t = setInterval(() => document.visibilityState === 'visible' && st.reload(), 15000); return () => clearInterval(t); }, []);
  const nodes = st.data?.nodes || [];
  const keys = st.data?.keys || [];
  const healthy = nodes.filter((n) => n.state === 'NODE_STATE_HEALTHY');
  const slots = nodes.reduce((a, n) => a + num(n.max_concurrent), 0);
  const busy = nodes.reduce((a, n) => a + num(n.active_requests), 0);
  const tps = healthy.reduce((a, n) => a + num(n.tps_ema), 0);

  const drain = (n) => confirmAction({
    title: `Drain ${n.name || n.node_id}?`, confirm: 'Drain', text: 'It finishes the requests it holds and accepts no new ones until it re-registers.',
    action: () => api('POST', `/api/proxy/nodes/${encodeURIComponent(n.node_id)}/drain`),
  }).then((ok) => ok && st.reload());
  const revoke = (k) => confirmAction({
    title: `Revoke the key of ${k.node_id}?`, confirm: 'Revoke', text: 'The node is refused at its next heartbeat.',
    action: () => api('POST', '/api/proxy/node-keys/revoke', { id: k.id }),
  }).then((ok) => ok && st.reload());
  const mint = () => openDialog(NodeKeyForm).then((ok) => ok && st.reload());

  const nodeRows = nodes.map((n) => {
    const [label, tone] = stateOf(n.state);
    const vram = num(n.vram_total_mb) ? 1 - num(n.vram_free_mb) / num(n.vram_total_mb) : 0;
    return html`<tr key=${n.node_id} class=${n.state === 'NODE_STATE_OFFLINE' ? 'off' : ''}>
      <td><div class="stacked"><span class="bold">${n.name || n.node_id}</span><span class="small mono">${n.node_id}</span></div></td>
      <td><${Badge} tone=${tone}>${label}<//></td>
      <td><div class="cluster">${(n.models || []).map((m) => html`<span class="chip mono" key=${m}>${m}</span>`)}</div></td>
      <td class="num">${fmt(n.active_requests)} / ${fmt(n.max_concurrent)}</td>
      <td style=${{ minWidth: '140px' }}>${num(n.vram_total_mb) ? html`<div class="stack" style=${{ gap: '4px' }}><span class="small tabular">${(num(n.vram_free_mb) / 1024).toFixed(1)} of ${(num(n.vram_total_mb) / 1024).toFixed(1)} GB free</span><${Meter} value=${vram} tone=${vram > 0.92 ? 'bad' : vram > 0.8 ? 'warn' : ''} /></div>` : html`<span class="muted">—</span>`}</td>
      <td class="num">${num(n.tps_ema) ? num(n.tps_ema).toFixed(1) + ' tok/s' : '—'}${num(n.ttft_ema_ms) ? html`<div class="small muted">TTFT ${Math.round(n.ttft_ema_ms)} ms</div>` : ''}</td>
      <td title=${when(n.last_heartbeat)}>${ago(n.last_heartbeat)}</td>
      <td class="actions-cell">${isAdmin() && n.state !== 'NODE_STATE_DRAINING' && n.state !== 'NODE_STATE_OFFLINE' && html`<button type="button" class="sm danger" onClick=${() => drain(n)}>Drain</button>`}</td>
    </tr>`;
  });
  const keyRows = keys.map((k) => html`<tr key=${k.id} class=${k.disabled ? 'off' : ''}>
    <td><span class="mono">${k.name}</span></td><td><span class="mono">${k.node_id}</span></td>
    <td>${(k.allowed_models || []).length ? html`<div class="cluster">${k.allowed_models.map((m) => html`<span class="chip mono" key=${m}>${m}</span>`)}</div>` : html`<span class="muted">any</span>`}</td>
    <td>${(k.allowed_cidrs || []).join(', ') || html`<span class="muted">any</span>`}</td>
    <td>${when(k.created_at)}</td>
    <td>${k.disabled ? html`<${Badge}>Revoked<//>` : html`<${Badge} tone="ok">Active<//>`}</td>
    <td class="actions-cell">${isAdmin() && !k.disabled && html`<button type="button" class="sm danger" onClick=${() => revoke(k)}>Revoke</button>`}</td>
  </tr>`);

  return html`
    <${PageHead} title="Local nodes" sub="GPU machines that register themselves and serve local models next to the cloud vendors.">
      <button type="button" onClick=${st.reload}><${Icon} name="refresh" />Refresh</button>
      ${isAdmin() && html`<button type="button" class="primary" onClick=${mint}>Mint node key</button>`}
    <//>
    <${LoadError} error=${st.error} retry=${st.reload} />
    <section class="tiles" aria-label="Pool">
      <${Tile} k="Healthy nodes" v=${st.data ? `${healthy.length} / ${nodes.length}` : '…'} />
      <${Tile} k="Slots in use" v=${st.data ? `${fmt(busy)} / ${fmt(slots)}` : '…'} s="Requests running across the pool" />
      <${Tile} k="Throughput" v=${st.data ? (tps ? tps.toFixed(0) + ' tok/s' : '—') : '…'} s="Sum of healthy nodes' recent speed" />
    </section>
    <${Card} title="Nodes" flush>
      <${Table} loading=${st.loading && !st.data} rows=${nodeRows} head=${['Node', 'State', 'Models', { label: 'Queue', num: true }, 'VRAM', { label: 'Speed', num: true }, 'Heartbeat', { label: 'Actions', sr: true }]}
        empty=${{ title: 'No nodes connected', children: 'Mint a node key and start ktms-node with NODE_KEY on a GPU machine.' }} />
    <//>
    <${Card} title="Node keys" flush>
      <${Table} loading=${st.loading && !st.data} rows=${keyRows} head=${['Name', 'Node id', 'Models', 'Networks', 'Created', 'Status', { label: 'Actions', sr: true }]}
        empty=${{ title: 'No node keys yet' }} />
    <//>
  `;
}

function NodeKeyForm({ close }) {
  const submit = async (fd) => {
    const res = await api('POST', '/api/proxy/node-keys', {
      name: String(fd.get('name')).trim(), node_id: String(fd.get('node_id')).trim(),
      allowed_cidrs: splitList(fd.get('allowed_cidrs')), allowed_models: splitList(fd.get('allowed_models')),
      max_base_weight: num(fd.get('max_base_weight')) || 1000, max_concurrency: num(fd.get('max_concurrency')) || 16,
    });
    close(true);
    showSecret('Node key created', 'Pass it to the node as NODE_KEY. This is the only time it is shown.', res.plain_key);
    return false;
  };
  return html`<${FormDialog} title="Mint node key" sub="The key binds one node id; the node registers itself with it." submit="Mint" close=${close} onSubmit=${submit}>
    <div class="form-grid">
      <${Field} label="Key name"><input name="name" required placeholder="desktop-rtx4090" /><//>
      <${Field} label="Node id" hint="Only this node may use the key."><input name="node_id" required placeholder="worker-1" /><//>
      <${Field} label="Allowed networks" hint="CIDRs, comma separated; empty = any." class="wide"><input name="allowed_cidrs" placeholder="10.0.0.0/8" /><//>
      <${Field} label="Allowed models" hint="Comma separated; empty = any it reports." class="wide"><input name="allowed_models" placeholder="local-qwen3-32b" /><//>
      <${Field} label="Max base weight"><input name="max_base_weight" type="number" defaultValue="1000" min="1" /><//>
      <${Field} label="Max concurrency"><input name="max_concurrency" type="number" defaultValue="16" min="1" /><//>
    </div>
  <//>`;
}
