import { html, useState, useEffect, api, useAsync, openDialog, confirmAction, toast, FormDialog, PageHead, Card, Table, Field, Check, Badge, LoadError, num } from '../lib.js';
import { isAdmin, loadCfg } from '../state.js';

const BLANK = { name: '', url: '', transport: 'streamable_http', allow_private: false, timeout_ms: 0, max_response_bytes: 0, tool_rpm: 0, enabled: true };
const path = (name, rest = '') => '/api/proxy/mcp-servers/' + encodeURIComponent(name) + rest;

export function MCPPage() {
  const st = useAsync(() => loadCfg(true), []);
  const servers = st.data?.mcp_servers || [];
  const creds = Object.fromEntries((st.data?.mcp_credentials || []).map((c) => [c.server, c]));
  const edit = (s) => openDialog(ServerForm, { server: s }).then((ok) => ok && st.reload());
  const credential = (s) => openDialog(CredentialForm, { server: s, current: creds[s.name] }).then((ok) => ok && st.reload());
  const tools = (s) => openDialog(ToolsDialog, { server: s, wide: true });
  const del = (s) => confirmAction({
    title: `Remove ${s.name}?`, text: 'Its credential goes with it, and its tools disappear from /mcp at once. Keys that name it in allowed_mcp keep the entry.',
    confirm: 'Remove', action: () => api('DELETE', path(s.name)),
  }).then((ok) => ok && st.reload());

  const rows = servers.map((s) => {
    const c = creds[s.name];
    return html`<tr key=${s.name} class=${s.enabled ? '' : 'off'}>
      <td><div class="stacked"><span class="mono">${s.name}</span><span class="small">${s.transport === 'sse' ? 'SSE' : 'Streamable HTTP'}${s.allow_private ? ' · private address' : ''}</span></div></td>
      <td><span class="mono small">${s.url}</span></td>
      <td>${c ? html`<span class="chip" title=${'fingerprint ' + (c.fingerprint || 'unreadable')}>${c.header}</span>` : html`<span class="muted">none</span>`}</td>
      <td class="num">${num(s.tool_rpm) ? s.tool_rpm : '∞'}</td>
      <td>${s.enabled ? html`<${Badge} tone="ok">Enabled<//>` : html`<${Badge}>Disabled<//>`}</td>
      <td class="actions-cell"><div class="cluster">
        <button type="button" class="sm" onClick=${() => tools(s)} disabled=${!s.enabled}>Tools</button>
        ${isAdmin() && html`<button type="button" class="sm" onClick=${() => edit(s)}>Edit</button>
          <button type="button" class="sm" onClick=${() => credential(s)}>Credential</button>
          <button type="button" class="sm danger" onClick=${() => del(s)}>Remove</button>`}
      </div></td>
    </tr>`;
  });

  return html`
    <${PageHead} title="MCP servers" sub="Clients with a key of the mcp scope reach these at /mcp (tools named server__tool) or /mcp/{server}. Credentials stay in KTMS.">
      ${isAdmin() && html`<button type="button" class="primary" onClick=${() => edit(null)}>Add server</button>`}
    <//>
    <${LoadError} error=${st.error} retry=${st.reload} />
    <${Card} flush label="MCP servers">
      <${Table} loading=${st.loading} rows=${rows}
        head=${['Server', 'URL', 'Credential', { label: 'Calls / min per key and tool', num: true }, 'Status', { label: 'Actions', sr: true }]}
        empty=${{ title: 'No MCP servers yet', children: 'Register one, give it a credential, then a key with the mcp scope.', action: isAdmin() && html`<button type="button" class="primary" onClick=${() => edit(null)}>Add server</button>` }} />
    <//>
  `;
}

function ServerForm({ server, close }) {
  const isNew = !server;
  const s = { ...BLANK, ...(server || {}) };
  const submit = async (fd) => {
    const name = isNew ? String(fd.get('name')).trim() : s.name;
    if (!/^[a-z][a-z0-9-]{0,31}$/.test(name)) throw new Error('The name is 1-32 of a-z, 0-9 and -, starting with a letter.');
    const spec = {
      url: String(fd.get('url')).trim(), transport: fd.get('transport'), allow_private: !!fd.get('allow_private'), enabled: !!fd.get('enabled'),
      timeout_ms: num(fd.get('timeout_ms')), max_response_bytes: String(num(fd.get('max_response_bytes'))), tool_rpm: num(fd.get('tool_rpm')),
    };
    await api('PUT', path(name), spec);
    toast('MCP server saved');
  };
  return html`<${FormDialog} title=${isNew ? 'Add MCP server' : 'Edit ' + s.name} sub="KTMS checks the address now and on every connection." close=${close} onSubmit=${submit}>
    <div class="form-grid">
      <${Field} label="Name" hint="Prefixes its tools on /mcp: name__tool."><input name="name" defaultValue=${s.name} required disabled=${!isNew} autocomplete="off" placeholder="github" /><//>
      <${Field} label="Transport"><select name="transport">
        <option value="streamable_http" defaultSelected=${s.transport !== 'sse'}>Streamable HTTP</option>
        <option value="sse" defaultSelected=${s.transport === 'sse'}>SSE (older servers)</option></select><//>
      <${Field} label="URL" hint="https; http only for a server on a private network." class="wide"><input name="url" defaultValue=${s.url} required placeholder="https://api.example.com/mcp" /><//>
      <${Field} label="Calls per minute" hint="Per client key and tool; 0 = no limit. Needs Redis."><input name="tool_rpm" type="number" min="0" step="1" defaultValue=${num(s.tool_rpm)} /><//>
      <${Field} label="Timeout, ms" hint="One tool call; 0 = 60 000."><input name="timeout_ms" type="number" min="0" step="1" defaultValue=${num(s.timeout_ms)} /><//>
      <${Field} label="Max result, bytes" hint="One tool result; 0 = 4 MiB."><input name="max_response_bytes" type="number" min="0" step="1" defaultValue=${num(s.max_response_bytes)} /><//>
    </div>
    <${Check} name="allow_private" label="Private address allowed (a server inside your network)" defaultChecked=${s.allow_private} />
    <${Check} name="enabled" label="Enabled: its tools are offered" defaultChecked=${s.enabled} />
  <//>`;
}

function CredentialForm({ server, current, close }) {
  const submit = async (fd) => {
    await api('PUT', path(server.name, '/credential'), { header: String(fd.get('header')).trim(), plain_value: fd.get('value') });
    toast('Credential saved');
  };
  const remove = () => confirmAction({
    title: `Remove the credential of ${server.name}?`, text: 'The gateway then calls the server without it.', confirm: 'Remove',
    action: () => api('DELETE', path(server.name, '/credential')),
  }).then((ok) => ok && close(true));
  return html`<${FormDialog} title=${'Credential of ' + server.name} sub="Sent by the gateway on every request to the server. Stored encrypted; never shown again, nor passed to clients." close=${close} onSubmit=${submit}
    foot=${current && isAdmin() && html`<button type="button" class="danger" onClick=${remove}>Remove</button>`}>
    ${current && html`<p class="small muted">Set: ${current.header}, fingerprint <span class="mono">${current.fingerprint || 'unreadable'}</span>. Saving replaces it.</p>`}
    <${Field} label="Header"><input name="header" defaultValue=${current?.header || 'Authorization'} required autocomplete="off" /><//>
    <${Field} label="Value" hint="For a token, usually Bearer followed by the token."><input name="value" type="password" required autocomplete="off" placeholder="Bearer …" /><//>
  <//>`;
}

function ToolsDialog({ server, close }) {
  const [st, setSt] = useState({ loading: true });
  useEffect(() => {
    api('GET', path(server.name, '/tools')).then((r) => setSt({ tools: r.tools || [] })).catch((error) => setSt({ error }));
  }, []);
  const rows = (st.tools || []).map((t) => html`<tr key=${t.name}>
    <td><span class="mono">${server.name}/${t.name}</span></td><td><span class="mono small">${server.name}__${t.name}</span></td><td class="small">${t.description}</td>
  </tr>`);
  return html`<${FormDialog} title=${'Tools of ' + server.name} sub="Asked through the gateway, with the server's credential. Name them in a key's or team's allowed MCP as in the first column." close=${close}>
    ${st.error ? html`<${LoadError} error=${st.error} />` : html`<${Table} loading=${st.loading} rows=${rows} head=${['Allowed MCP entry', 'On /mcp', 'Description']}
      empty=${{ title: 'No tools', children: 'The server lists none.' }} />`}
  <//>`;
}
