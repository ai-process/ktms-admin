// Session-wide state: who is signed in, which backends exist, and the gateway
// config, cached because nearly every page reads it.
import { api } from './lib.js';

export const session = { me: null, health: null };

const role = () => session.me?.user?.role;
export const isAdmin = () => role() === 'admin';
export const isTeamLead = () => role() === 'teamlead';
export const isViewer = () => role() === 'viewer';
export const isMember = () => role() === 'member';
// Viewers read everything an admin reads; team users only their own team.
export const canReadGlobal = () => isAdmin() || isViewer();
export const inTeam = () => (isTeamLead() || isMember()) && !!session.me?.user?.team_id;
export const hasProxy = () => !!session.me?.features?.proxy;
export const hasUsage = () => !!session.me?.features?.usage;

let cfg = null;
let cfgAt = 0;
let inflight = null;

// loadCfg returns the cached gateway config, refetching when forced or after
// 30 s so edits made elsewhere show up without a reload.
export async function loadCfg(force) {
  if (!force && cfg && Date.now() - cfgAt < 30000) return cfg;
  if (!force && inflight) return inflight;
  inflight = api('GET', '/api/proxy/config').then((c) => { cfg = c; cfgAt = Date.now(); return c; }).finally(() => { inflight = null; });
  return inflight;
}

export const dropCfg = () => { cfg = null; };
