export type Role = 'admin' | 'operator' | 'editor' | 'viewer'
export type NodeKind = 'file' | 'folder'
export type NodeSpace = 'main' | 'secure'

export interface User {
  id: string
  username: string
  display_name: string
  role: Role
  is_owner: boolean
  must_change_password: boolean
  suggest_password_change?: boolean
  disabled: boolean
  created_at: string
}

export interface PublicSettings {
  site_name: string
  max_file_size: number
  recycle_retention_days: number
  backup_enabled: boolean
  backup_time: string
  backup_keep: number
  backup_dir: string
}

export interface AdminSettings extends PublicSettings {
  free_space_reserve: number
}

export interface AuthState {
  user: User
  settings: PublicSettings
}

export interface SetupStatus {
  needs_setup: boolean
  local: boolean
  site_name: string
  version: string
  tls_enabled: boolean
  cert_fingerprint: string
  cert_not_after: string
}

export interface NodeItem {
  id: string
  parent_id: string
  name: string
  kind: NodeKind
  current_version_id?: string
  revision: number
  size: number
  sha256?: string
  version_no: number
  created_at: string
  created_by: string
  updated_at: string
  updated_by: string
  deleted_at?: string
  deleted_by?: string
  delete_batch?: string
  path?: string
  space: NodeSpace
  visibility_mode?: 'default' | 'custom'
  is_zone_root?: boolean
  can_manage?: boolean
  can_delete?: boolean
  can_manage_visibility?: boolean
}

export interface VisibilityUser {
  id: string
  display_name: string
  role: Role
}

export interface VisibilityInfo {
  mode: 'default' | 'custom'
  roles: Role[]
  users: VisibilityUser[]
}

export interface VisibilityOptions {
  roles: Role[]
  users: VisibilityUser[]
}

export interface VisibilityChoice {
  mode: 'default' | 'custom'
  roles: Role[]
  users: string[]
}

export interface VersionItem {
  id: string
  node_id: string
  version_no: number
  object_id: string
  size: number
  sha256: string
  note: string
  created_at: string
  created_by: string
  created_by_name: string
  is_current: boolean
}

export interface Breadcrumb {
  id: string
  name: string
  space: NodeSpace
}

export interface UploadResult {
  upload_id?: string
  node: NodeItem
  version: VersionItem
  duplicate?: boolean
}

export interface AuditRow {
  seq: number
  id: string
  actor_id: string
  actor_name: string
  action: string
  target_type: string
  target_id: string
  target_name: string
  target_path: string
  detail: string
  result: 'success' | 'failure' | 'partial'
  ip: string
  created_at: string
  secure?: boolean
}

export interface AuditPage {
  items: AuditRow[]
  total: number
  page: number
  page_size: number
}

export interface BackupRun {
  id: string
  started_at: string
  finished_at: string
  status: 'running' | 'success' | 'failed'
  message: string
  path: string
  bytes: number
  objects: number
}

export interface CertInfo {
  fingerprint: string
  not_after: string
  subject: string
  dns_names: string[]
  ip_addresses: string[]
  self_signed: boolean
}

export interface SystemInfo {
  stats: {
    files: number
    folders: number
    versions: number
    object_bytes: number
    free_space: number
  }
  users: number
  active_sessions: number
  backup_running: boolean
  last_backup: BackupRun | null
  recent_backups: BackupRun[]
  cert: CertInfo
  tls_enabled: boolean
  data_dir: string
  version: string
  started_at: string
}

export interface MeState {
  user: User
  settings: PublicSettings
  version: string
  tls_enabled: boolean
}
