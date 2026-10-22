import { html, useState, useMemo, api, useAsync, PageHead, Card, Tile, Table, Seg, Field, Badge, LoadError, Meter, fmt, compact, pct, usd, num } from '../lib.js';
import { hasProxy } from '../state.js';

const GROUPS = [['project', 'Project'], ['model', 'Model'], ['user', 'User'], ['action', 'Action'], ['vendor', 'Vendor'], ['none', 'Total only']];
const PERIODS = [['7', '7d'], ['14', '14d'], ['30', '30d'], ['90', '90d']];
const iso = (d) => d.toISOString().slice(0, 10);
const daysAgo = (n) => iso(new Date(Date.now() - n * 86400000));

const VERDICT_HELP = {
  greedy: 'A big slice of all tokens at a high cost per request, or many tokens lost to failures.',
  worthy: 'A solid share, lean per request, reliable.',
};

export function UsagePage() {
  const [range, setRange] = useState({ period: '30', from: daysAgo(29), to: daysAgo(0) });
  const [group, setGroup] = useState('project');
  const [metric, setMetric] = useState('cost');
  const pick = (p) => setRange({ period: p, from: daysAgo(Number(p) - 1), to: daysAgo(0) });
  const rep = useAsync(() => api('GET', '/api/usage?' + new URLSearchParams({ from: range.from, to: range.to, group })), [range.from, range.to, group]);
  const r = rep.data?.report;
  const t = r?.totals;
  const costKnown = !!r?.cost_known;
  const shownMetric = costKnown ? metric : (metric === 'cost' ? 'tokens' : metric);

  const exportCsv = () => {
    if (!r) return;
    const head = ['key', 'tokens_input', 'tokens_output', 'tokens_total', 'requests', 'failed', 'failure_rate', 'avg_latency_ms', 'cost_usd', 'verdict'];
    const rows = [head, ...r.groups.map((g) => head.map((k) => (k === 'key' ? g.key : g[k] ?? '')))];
    // Group keys are client-supplied attributes: a leading = + - @ would run
    // as a formula in a spreadsheet, so such cells get a quote prefix.
    const cell = (v) => { let s = String(v); if (/^[=+\-@\t\r]/.test(s)) s = "'" + s; return `"${s.replace(/"/g, '""')}"`; };
    const csv = rows.map((row) => row.map(cell).join(',')).join('\n');
    const a = document.createElement('a');
    a.href = URL.createObjectURL(new Blob([csv], { type: 'text/csv' }));
    a.download = `ktms-usage-${group}-${range.from}-${range.to}.csv`;
    a.click();
    setTimeout(() => URL.revokeObjectURL(a.href), 1000);
  };

  return html`
    <${PageHead} title="Usage" sub="Who is spending tokens, on what, and whether it pays off.">
      <${Seg} label="Period" dark options=${PERIODS} value=${range.period} onChange=${pick} />
      <${Field} label=${html`<span class="sr-only">From</span>`}><input type="date" value=${range.from} max=${range.to} onChange=${(e) => setRange({ ...range, period: '', from: e.currentTarget.value })} /><//>
      <${Field} label=${html`<span class="sr-only">To</span>`}><input type="date" value=${range.to} min=${range.from} onChange=${(e) => setRange({ ...range, period: '', to: e.currentTarget.value })} /><//>
      <button type="button" onClick=${exportCsv} disabled=${!r?.groups?.length}>Export CSV</button>
    <//>
    <${LoadError} error=${rep.error} retry=${rep.reload} />
    <section class="tiles" aria-label="Totals">
      ${costKnown && html`<${Tile} k="Spend" v=${t ? usd(t.cost_usd) : '…'} s="From model list prices" />`}
      <${Tile} k="Requests" v=${t ? compact(t.requests) : '…'} s=${t?.requests ? `${fmt(t.tokens_per_request)} tokens each` : ''} />
      <${Tile} k="Tokens" v=${t ? compact(t.tokens_total) : '…'} s=${t ? `${compact(t.tokens_input)} in · ${compact(t.tokens_output)} out` : ''} />
      <${Tile} k="Error rate" v=${t ? pct(t.failure_rate) : '…'} s=${t ? `${fmt(t.failed)} failed · ${compact(t.failed_tokens)} tokens lost` : ''} />
      <${Tile} k="Avg latency" v=${t?.requests ? (t.avg_latency_ms / 1000).toFixed(2) + ' s' : '—'} />
    </section>
    <${Card} title=${{ cost: 'Spend by day', tokens: 'Tokens by day', requests: 'Requests by day' }[shownMetric]}
      actions=${html`<${Seg} label="Metric" value=${shownMetric} onChange=${setMetric} options=${[...(costKnown ? [['cost', 'Spend']] : []), ['tokens', 'Tokens'], ['requests', 'Requests']]} />`}>
      <${DailyChart} days=${r?.daily || []} metric=${shownMetric} loading=${rep.loading && !r} />
    <//>
    <div class="row">
      <div style=${{ flex: '3 1 560px' }}>
        <${GroupTable} r=${r} group=${group} setGroup=${setGroup} loading=${rep.loading} costKnown=${costKnown} />
      </div>
      ${hasProxy() && html`<div style=${{ flex: '2 1 320px' }}><${TeamBudgets} /></div>`}
    </div>
    <p class="small muted">Greedy: at least 10% of all tokens at 1.5× the average tokens per request or more, or at least 5% of tokens with 20% of them lost to failed requests. Worthy: at least 1% of tokens, at or below the average per request, at most 5% failures. Needs two groups and 5 requests.</p>
  `;
}

function DailyChart({ days, metric, loading }) {
  const val = (d) => (metric === 'cost' ? num(d.cost_usd) : metric === 'tokens' ? num(d.tokens) : num(d.requests));
  const max = Math.max(1e-9, ...days.map(val));
  const label = (d) => `${d.bucket}: ${metric === 'cost' ? usd(d.cost_usd) : fmt(val(d))}${metric === 'cost' ? '' : metric === 'tokens' ? ' tokens' : ' requests'} · ${fmt(d.requests)} requests, ${fmt(d.failed)} failed`;
  if (loading) return html`<div class="skeleton" style=${{ height: '200px' }}></div>`;
  if (!days.length) return html`<p class="muted">No usage recorded in this range.</p>`;
  return html`
    <div class="chart" role="img" aria-label=${`${metric} per day, ${days[0].bucket} to ${days[days.length - 1].bucket}`}>
      ${days.map((d) => {
        const total = val(d) / max;
        const failShare = d.requests ? num(d.failed) / num(d.requests) : 0;
        return html`<div class="col" key=${d.bucket} title=${label(d)}>
          ${failShare > 0 && html`<div style=${{ height: total * failShare * 100 + '%', background: 'var(--bad)' }}></div>`}
          <div style=${{ height: total * (1 - failShare) * 100 + '%', background: 'var(--series-1)' }}></div>
        </div>`;
      })}
    </div>
    <div class="chart-axis"><span>${days[0].bucket}</span>${days.length > 2 && html`<span>${days[Math.floor(days.length / 2)].bucket}</span>`}<span>${days[days.length - 1].bucket}</span></div>
    <div class="legend"><span><i style=${{ background: 'var(--series-1)' }}></i>Succeeded</span><span><i style=${{ background: 'var(--bad)' }}></i>Failed share</span></div>
  `;
}

function GroupTable({ r, group, setGroup, loading, costKnown }) {
  const label = Object.fromEntries(GROUPS)[group];
  const max = Math.max(1, ...(r?.groups || []).map((g) => g.tokens_total));
  const head = [label, { label: 'Requests', num: true }, { label: 'Tokens', num: true }, { label: 'Share', num: true }, ...(costKnown ? [{ label: 'Spend', num: true }] : []), { label: 'Failed', num: true }, { label: 'Latency', num: true }, 'Verdict'];
  const rows = (r?.groups || []).map((g) => html`<tr key=${g.key}>
    <td><span class="mono">${g.key || '(none)'}</span></td>
    <td class="num">${fmt(g.requests)}</td>
    <td class="num">${fmt(g.tokens_total)}<span class="inline-bar" style=${{ width: Math.max(2, (g.tokens_total / max) * 100) + '%' }}></span></td>
    <td class="num">${pct(g.token_share)}</td>
    ${costKnown && html`<td class="num bold">${usd(g.cost_usd)}</td>`}
    <td class="num">${g.failure_rate > 0.05 ? html`<span class="err-text">${pct(g.failure_rate)}</span>` : pct(g.failure_rate)}</td>
    <td class="num">${(g.avg_latency_ms / 1000).toFixed(2)} s</td>
    <td>${g.verdict ? html`<${Badge} tone=${g.verdict === 'worthy' ? 'ok' : 'warn'} title=${VERDICT_HELP[g.verdict]}>${g.verdict}<//>` : html`<span class="muted">—</span>`}</td>
  </tr>`);
  return html`<${Card} title=${'By ' + label.toLowerCase()} flush
    actions=${html`<label class="field inline small">Group by <select value=${group} onChange=${(e) => setGroup(e.currentTarget.value)}>${GROUPS.map(([v, l]) => html`<option value=${v}>${l}</option>`)}</select></label>`}>
    ${group === 'none'
      ? html`<div class="card-body"><p class="muted">Totals only: pick a grouping to break them down.</p></div>`
      : html`<${Table} head=${head} rows=${rows} loading=${loading && !r} empty=${{ title: 'No usage in this range' }} />`}
  <//>`;
}

// Team budgets read the gateway's enforced counters. Every capped team is
// read so the ones nearest their cap are the ones shown.
function TeamBudgets() {
  const st = useAsync(async () => {
    const teams = (await api('GET', '/api/proxy/teams')).teams || [];
    const capped = teams.filter((t) => num(t.monthly_budget_usd) > 0);
    return Promise.all(capped.map((t) => api('GET', `/api/proxy/teams/${encodeURIComponent(t.id)}/spend`).then((c) => ({ t, c }), () => ({ t, c: null }))));
  }, []);
  const all = useMemo(() => [...(st.data || [])].sort((a, b) => share(b) - share(a)), [st.data]);
  const items = all.slice(0, 8);
  return html`<${Card} title="Team budgets" sub=${all.length > items.length ? `The ${items.length} closest to their cap, of ${all.length}` : ''} actions=${html`<a href="#/tenants" class="small">Manage</a>`}>
    ${st.loading && html`<div class="skeleton" style=${{ height: '80px' }}></div>`}
    ${!st.loading && !items.length && html`<p class="muted">No team has a monthly budget yet.</p>`}
    ${items.map(({ t, c }) => {
      const s = share({ t, c });
      const known = c?.metered;
      const tone = s >= 0.95 ? 'bad' : s >= 0.8 ? 'warn' : '';
      return html`<div class="budget" key=${t.id}>
        <div class="line"><a href=${'#/cabinet?team_id=' + encodeURIComponent(t.id)} class="bold">${t.name}</a><span class="muted tabular">${known ? usd(c.current_spend_usd) : '—'} of ${usd(t.monthly_budget_usd)}</span></div>
        <${Meter} value=${known ? s : 0} tone=${tone} />
        <span class="small muted">${!c ? 'Spend unavailable' : !known ? 'Not metered' : s >= 1 ? 'Calls refused until the month resets' : 'Resets ' + new Date(c.resets_at).toLocaleDateString()}</span>
      </div>`;
    })}
  <//>`;
}

const share = ({ t, c }) => (c?.metered && num(t.monthly_budget_usd) > 0 ? num(c.current_spend_usd) / num(t.monthly_budget_usd) : 0);
