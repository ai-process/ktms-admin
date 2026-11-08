import { isViewer } from '../state.js';
import { html, useState, api, useAsync, openDialog, confirmAction, showSecret, FormDialog, PageHead, Card, Table, Field, Badge, LoadError, when } from '../lib.js';

const TONE = { submitted: 'accent', working: 'accent', 'input-required': 'warn', completed: 'ok', failed: 'bad', canceled: '' };
const status = (t) => html`<${Badge} tone=${TONE[t.status] || ''} title=${t.status_reason || ''}>${t.status}<//>`;

// AgentPage is where a person gets a key for their own agent and reads what
// it sent and received; every account has it, the self role only it.
export function AgentPage() {
  const st = useAsync(() => api('GET', '/api/agent'), []);
  const page = st.data || { keys: [], agents: [] };
  const mint = () => openDialog(MintForm, {}).then((res) => res && (showSecret(`Key ${res.name}`,
    'Shown only now. Give it to your agent as its KTMS key; it may talk to other agents and do nothing else.', res.plain_key), st.reload()));
  const revoke = (k) => confirmAction({
    title: `Revoke ${k.name}?`, text: 'Your agent is refused at once and leaves the other agents’ lists. Its conversations stay until they expire.', confirm: 'Revoke',
    action: () => api('DELETE', '/api/agent/keys/' + encodeURIComponent(k.name)),
  }).then((ok) => ok && st.reload());

  const keyRows = page.keys.map((k) => html`<tr key=${k.name}>
    <td><span class="mono">${k.name}</span></td>
    <td>${when(k.created_at)}</td>
    <td class="actions-cell">${!isViewer() && html`<button type="button" class="sm danger" onClick=${() => revoke(k)}>Revoke</button>`}</td>
  </tr>`);

  return html`
    <${PageHead} title="My agent" sub="A key for your own coding agent, so it can send tasks to your colleagues' agents and answer theirs through Keystone.">
      ${!isViewer() && html`<button type="button" class="primary" onClick=${mint}>New agent key</button>`}
    <//>
    <${LoadError} error=${st.error} retry=${st.reload} />
    <${Card} title="Connect it" label="How to connect">
      <div class="card-body stack">
        <p class="muted">Add Keystone to your agent as an MCP server with the key. Its tools are list_agents, send_message, inbox, reply, get_task and outbox; ask it to check its inbox now and then.</p>
        <pre class="mono small">${`claude mcp add --transport http ktms ${page.mcp_url || 'https://<keystone>/mcp/agents'} --header "Authorization: Bearer <key>"`}</pre>
      </div>
    <//>
    <${Card} flush title="Keys" label="Agent keys">
      <${Table} loading=${st.loading} rows=${keyRows} head=${['Key', 'Created', { label: 'Actions', sr: true }]}
        empty=${{ title: 'No agent key yet', children: 'Make one, then connect your agent with it.', action: !isViewer() && html`<button type="button" class="primary" onClick=${mint}>New agent key</button>` }} />
    <//>
    ${page.agents.map((a) => html`<${AgentTasks} key=${a.name} agent=${a} />`)}
  `;
}

function MintForm({ close }) {
  const submit = async (fd) => close(await api('POST', '/api/agent/keys', { label: String(fd.get('label') || '').trim() }));
  return html`<${FormDialog} title="New agent key" submit="Create" onSubmit=${submit} close=${close}>
    <${Field} label="Label (optional)" hint="Agents address each other by name: yours is your address, with _ for @, and this label, such as laptop or ci.">
      <input name="label" maxlength="20" pattern="[A-Za-z0-9 -]*" autocomplete="off" placeholder="agent" />
    <//>
  <//>`;
}

function AgentTasks({ agent }) {
  const st = useAsync(() => api('GET', '/api/agent/tasks?agent=' + encodeURIComponent(agent.name)), [agent.name]);
  const open = (t) => openDialog(TaskDialog, { id: t.task_id, agent: agent.name, wide: true }).then(() => st.reload());
  const rows = (st.data?.tasks || []).map((t) => html`<tr key=${t.task_id}>
    <td>${t.from === agent.name ? html`<span class="muted">to</span> <span class="mono">${t.to}</span>` : html`<span class="muted">from</span> <span class="mono">${t.from}</span>`}</td>
    <td>${status(t)}${t.reason_code === 'needs_owner' && t.to === agent.name && html` <${Badge} tone="warn">asks you<//>`}</td>
    <td>${when(t.updated_at)}</td>
    <td class="actions-cell"><button type="button" class="sm" onClick=${() => open(t)}>Open</button></td>
  </tr>`);
  return html`<${Card} flush title=${'Conversations of ' + agent.name + (agent.revoked ? ' (key revoked)' : '')} sub=${agent.description || ''} label=${'Conversations of ' + agent.name}>
    <${LoadError} error=${st.error} retry=${st.reload} />
    <${Table} loading=${st.loading} rows=${rows} head=${['With', 'Status', 'Updated', { label: 'Actions', sr: true }]}
      empty=${{ title: 'No conversations yet', children: 'Tasks your agent sends or receives show up here.' }} />
  <//>`;
}

const TERMINAL = ['completed', 'failed', 'canceled'];

// What the person may do for their agent: on the recipient's side answer,
// hand back, ask or decline, and correct an ended answer; on the sender's
// side answer a question or cancel.
function choices(t, agent) {
  const recipient = t.to === agent;
  if (TERMINAL.includes(t.status)) return recipient && t.status !== 'canceled' ? [['correction', 'Correct the answer']] : [];
  if (recipient) return [['completed', 'Answer and close the task'], ['working', 'Hand it back to my agent with this answer'],
    ['input-required', 'Ask the sender a question'], ['failed', 'Decline']];
  const out = [];
  if (t.status === 'input-required' && t.reason_code === 'question') out.push(['working', 'Answer the question']);
  return [...out, ['canceled', 'Cancel the task']];
}

function TaskDialog({ id, agent, close }) {
  const st = useAsync(() => api('GET', '/api/agent/tasks/' + encodeURIComponent(id)), [id]);
  const t = st.data;
  const [sent, setSent] = useState(0); // remounts the answer box empty after each send
  const opts = t && !isViewer() ? choices(t, agent) : [];
  const submit = opts.length ? async (fd) => {
    const how = String(fd.get('how'));
    const body = { agent, text: String(fd.get('text') || '').trim(), status: how === 'correction' ? '' : how, correction: how === 'correction' };
    await api('POST', '/api/agent/tasks/' + encodeURIComponent(id) + '/reply', body);
    setSent((n) => n + 1);
    st.reload();
    return false;
  } : undefined;
  const who = (m) => m.author === 'owner' ? `${m.from}'s owner` : m.from;
  return html`<${FormDialog} title=${t ? `${t.from} → ${t.to}` : 'Task'} sub=${t ? `context ${t.context_id}` : ''} close=${close}
      cancel="Close" submit="Send" onSubmit=${submit}>
    <${LoadError} error=${st.error} retry=${st.reload} />
    ${t && html`<div class="stack">
      <div class="cluster">${status(t)}${t.status_reason && html`<span class="small">${t.status_reason}</span>`}
        ${t.answered_by === 'owner' && html`<${Badge}>answered by a person<//>`}</div>
      ${t.reason_code === 'needs_owner' && t.to === agent && html`<div class="notice warn">Your agent asks you before it answers. Hand it back with your answer, or answer the sender yourself.</div>`}
      ${(t.messages || []).map((m, i) => html`<div key=${i} class="stack">
        <div class="small muted">${who(m)} · ${when(m.created_at)}${m.correction && html` · <${Badge} tone="warn">correction<//>`}</div>
        <pre class="small">${m.text}</pre>
      </div>`)}
      ${opts.length > 0 && html`<${Field} label="Your answer" hint="Sent as you, the owner of ${agent}; the other side sees it was a person.">
          <textarea key=${sent} name="text" rows="4" required=${opts[0][0] !== 'canceled'}></textarea><//>
        <${Field} label="Then"><select name="how">${opts.map(([v, l]) => html`<option value=${v}>${l}</option>`)}</select><//>`}
    </div>`}
  <//>`;
}
