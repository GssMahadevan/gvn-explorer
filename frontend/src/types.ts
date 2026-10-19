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
  principal_type: 'user' | 'group';
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

export interface User {
  id: number;
  tenant: string;
  username: string;
  is_admin: boolean;
  is_super: boolean;
  active: boolean;
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
  members?: GroupMember[];
}

export interface Tenant {
  id: number;
  name: string;
  created_at: string;
}
