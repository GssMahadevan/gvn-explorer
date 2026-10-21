// Typed data shapes returned by the /api/v1 REST endpoints.

export interface Me {
  id: number;
  tenant: string;
  username: string;
  is_admin: boolean;
  is_super: boolean;
}

export interface EffPerms {
  read: boolean;
  write: boolean;
  delete: boolean;
  source?: string;
}

export interface Entry {
  name: string;
  path: string;
  is_dir: boolean;
  size: number;
  mod_time: string;
  mod_time_ms: number; // same instant, unix milliseconds — what the GUI renders
}

export interface EntryView {
  entry: Entry;
  perms: EffPerms;
}

export interface FsList {
  entries: EntryView[];
}

export interface Grant {
  id: number;
  path: string;
  principal_type: 'user' | 'group' | 'tenant' | 'all';
  principal_tenant: string;
  principal_name: string;
  principal_active?: boolean;
  expires_at?: number | null;
  expired?: boolean;
  read: boolean;
  write: boolean;
  delete: boolean;
}

export interface PermsList {
  effective: EffPerms;
  grants: Grant[];
}

// Entity/ACL documents (GET /api/v1/export, POST /api/v1/import). The
// shape is deliberately loose: the server validates every document, the
// GUI only renders and relays it. Grant lines are the PUT /perms input
// shape (principal + optional tenant), not the GET /perms output rows.
export interface DocGrant {
  path?: string; // entity docs place each line; acl/dir docs use the doc's path
  principal_type: 'user' | 'group' | 'tenant' | 'all';
  principal: string;
  tenant?: string; // defaults to the applying account's tenant ("*" same)
  read?: boolean;
  write?: boolean;
  delete?: boolean;
  expires_at?: string;
  expires_days?: number;
}

export interface Doc {
  kind: 'acl' | 'dir' | 'user' | 'group' | 'tenant';
  version: number;
  path?: string; // acl/dir docs
  tenant?: string; // user/group docs
  username?: string; // user docs
  name?: string; // group/tenant docs
  grants: DocGrant[];
  [k: string]: unknown;
}

export interface ImportReport {
  kind: string;
  mode: string;
  path?: string;
  subject?: string;
  created?: boolean;
  device_user?: boolean;
  memberships?: number;
  applied: number;
  removed?: string[];
}

export interface User {
  id: number;
  tenant: string;
  username: string;
  is_admin: boolean;
  is_super: boolean;
  active: boolean;
  description: string;
  expires_at?: number | null;
  expired?: boolean;
  created_at: string;
}

export interface GroupMember {
  id: number;
  username: string;
}

export interface Group {
  id: number;
  tenant: string;
  name: string;
  active: boolean;
  description: string;
  members?: GroupMember[];
}

export interface Tenant {
  id: number;
  name: string;
  active: boolean;
  description: string;
  created_at: string;
}

// Device-pairing invite (GET/POST /api/v1/pairings). status is derived
// server-side: pending | used | revoked | expired. The claim token and
// short code only appear in the create response (shown once, like API
// keys); the QR encodes "<origin>/join#<token>" so the secret never
// reaches the server as a query parameter or access-log line.
export interface Pairing {
  id: number;
  tenant: string;
  description: string;
  username?: string;
  apikey: boolean;
  user_expires_at?: string;
  created_by: string;
  created_at: string;
  expires_at: string;
  used_at?: string;
  used_by?: string;
  revoked_at?: string;
  status: 'pending' | 'used' | 'revoked' | 'expired';
}

export interface PairingCreated {
  pairing: Pairing;
  token: string; // QR payload secret
  code: string; // 7-char typed fallback
}

// POST /api/v1/join: the created account plus its one-time password
// (and API key, when the invite asked for one). The session cookie is
// already set for this browser.
export interface JoinResult {
  user: User;
  password: string;
  apikey?: string;
}

// One row of the append-only audit trail (GET /api/v1/events).
// actor is a "tenant/username" snapshot and empty for unauthenticated
// attempts (failed logins); tenant_id is the subject tenant, 0 = platform.
export interface EventRow {
  id: number;
  ts: number; // unix seconds (floored view — contract unchanged)
  ts_ms: number; // exact instant, unix milliseconds — what the GUI renders
  time: string;
  actor_id?: number;
  actor?: string;
  tenant_id: number;
  action: string; // perm_set, user_create, login_failed, fs_commit, ...
  target_type?: string; // user | group | grant | tenant | session | file ...
  target_id?: number;
  target_name?: string;
  path?: string; // for file/grant actions
  details?: Record<string, unknown>;
  ip?: string;
}
