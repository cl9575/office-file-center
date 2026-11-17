import type {
  AdminSettings,
  AuditPage,
  AuthState,
  BackupRun,
  Breadcrumb,
  MeState,
  NodeItem,
  PublicSettings,
  SetupStatus,
  SystemInfo,
  UploadResult,
  User,
  VersionItem,
} from './types'

export class ApiError extends Error {
  status: number
  code: string
  payload: unknown

  constructor(status: number, code: string, message: string, payload?: unknown) {
    super(message)
    this.status = status
    this.code = code
    this.payload = payload
  }
}

function notifyUnauthorized(status: number) {
  if (status === 401 && typeof window !== 'undefined') {
    window.dispatchEvent(new Event('lfp-unauthorized'))
  }
}

async function request<T>(url: string, options: RequestInit = {}): Promise<T> {
  const res = await fetch(url, {
    credentials: 'same-origin',
    ...options,
    headers: {
      ...(options.body ? { 'Content-Type': 'application/json' } : {}),
      ...(options.headers || {}),
    },
  })
  const type = res.headers.get('content-type') || ''
  let payload: unknown = null
  if (type.includes('application/json')) {
    payload = await res.json().catch(() => null)
  }
  if (!res.ok) {
    notifyUnauthorized(res.status)
    const error = (payload as { error?: { code?: string; message?: string } } | null)?.error
    throw new ApiError(res.status, error?.code || 'HTTP_ERROR', error?.message || `请求失败（${res.status}）`, payload)
  }
  return payload as T
}

function query(params: Record<string, string | number | undefined | null>): string {
  const search = new URLSearchParams()
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== null && value !== '') {
      search.set(key, String(value))
    }
  })
  const text = search.toString()
  return text ? `?${text}` : ''
}

export const api = {
  setupStatus: () => request<SetupStatus>('/api/setup/status'),
  setupAdmin: (data: { username: string; display_name: string; password: string }) =>
    request<AuthState>('/api/setup/admin', { method: 'POST', body: JSON.stringify(data) }),
  login: (username: string, password: string) =>
    request<AuthState>('/api/auth/login', { method: 'POST', body: JSON.stringify({ username, password }) }),
  logout: () => request<{ ok: boolean }>('/api/auth/logout', { method: 'POST' }),
  me: () => request<MeState>('/api/auth/me'),
  changePassword: (oldPassword: string, newPassword: string) =>
    request<{ ok: boolean }>('/api/auth/change-password', {
      method: 'POST',
      body: JSON.stringify({ old_password: oldPassword, new_password: newPassword }),
    }),

  listNodes: async (parent: string, q = '') =>
    (await request<{ items: NodeItem[] }>(`/api/nodes${query({ parent, q })}`)).items,
  folderTree: async () => (await request<{ items: NodeItem[] }>('/api/folders/tree')).items,
  breadcrumbs: async (id: string) =>
    (await request<{ items: Breadcrumb[] }>(`/api/nodes/${encodeURIComponent(id)}/breadcrumbs`)).items,
  node: (id: string) => request<NodeItem>(`/api/nodes/${encodeURIComponent(id)}`),
  createFolder: (parent_id: string, name: string) =>
    request<NodeItem>('/api/nodes', { method: 'POST', body: JSON.stringify({ parent_id, name }) }),
  patchNode: (id: string, data: { name?: string; parent_id?: string }) =>
    request<NodeItem>(`/api/nodes/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(data) }),
  deleteNode: (id: string) =>
    request<{ deleted: number }>(`/api/nodes/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  resolve: (parent: string, name: string) =>
    request<{ exists: boolean; node: NodeItem | null }>(`/api/nodes/resolve${query({ parent, name })}`),

  versions: async (id: string) =>
    (await request<{ items: VersionItem[] }>(`/api/nodes/${encodeURIComponent(id)}/versions`)).items,
  nodeAudit: async (id: string) =>
    (await request<{ items: import('./types').AuditRow[] }>(`/api/nodes/${encodeURIComponent(id)}/audit`)).items,
  restoreVersion: (id: string, versionId: string, baseVersionId: string) =>
    request<UploadResult>(`/api/nodes/${encodeURIComponent(id)}/restore-version`, {
      method: 'POST',
      body: JSON.stringify({ version_id: versionId, base_version_id: baseVersionId }),
    }),
  purgeVersion: (id: string, versionId: string) =>
    request<{ ok: boolean }>(`/api/nodes/${encodeURIComponent(id)}/versions/${encodeURIComponent(versionId)}`, {
      method: 'DELETE',
    }),

  upload: (options: UploadOptions) => uploadFile(options),
  uploadStatus: (id: string) =>
    request<{ upload_id: string; status: string; node_id: string; version_id: string; error: string }>(
      `/api/upload/${encodeURIComponent(id)}`,
    ),

  recycle: async () => (await request<{ items: NodeItem[] }>('/api/recycle')).items,
  restoreRecycle: (batch: string) =>
    request<{ restored: number }>(`/api/recycle/${encodeURIComponent(batch)}/restore`, { method: 'POST' }),
  purgeRecycle: (batch: string) =>
    request<{ purged: number }>(`/api/recycle/${encodeURIComponent(batch)}`, { method: 'DELETE' }),
  purgeAllRecycle: () => request<{ purged: number }>('/api/recycle', { method: 'DELETE' }),

  users: async () => (await request<{ items: User[] }>('/api/admin/users')).items,
  createUser: (data: { username: string; display_name: string; password: string; role: string }) =>
    request<User>('/api/admin/users', { method: 'POST', body: JSON.stringify(data) }),
  updateUser: (id: string, data: { display_name: string; role: string; disabled: boolean }) =>
    request<User>(`/api/admin/users/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(data) }),
  resetPassword: (id: string, password: string) =>
    request<{ ok: boolean }>(`/api/admin/users/${encodeURIComponent(id)}/reset-password`, {
      method: 'POST',
      body: JSON.stringify({ password }),
    }),
  deleteUser: (id: string) =>
    request<{ ok: boolean }>(`/api/admin/users/${encodeURIComponent(id)}`, { method: 'DELETE' }),

  audit: (params: AuditParams) => request<AuditPage>(`/api/admin/audit${query(params)}`),
  auditCsvUrl: (params: AuditParams) => `/api/admin/audit.csv${query(params)}`,
  adminSettings: () => request<AdminSettings>('/api/admin/settings'),
  updateSettings: (data: Partial<AdminSettings>) =>
    request<AdminSettings>('/api/admin/settings', { method: 'PUT', body: JSON.stringify(data) }),
  system: () => request<SystemInfo>('/api/admin/system'),
  backups: async () =>
    request<{ items: BackupRun[]; running: boolean; last_success: BackupRun | null }>('/api/admin/backups'),
  runBackup: () => request<{ message: string }>('/api/admin/backups/run', { method: 'POST' }),
}

export type AuditParams = {
  actor?: string
  action?: string
  result?: string
  q?: string
  from?: string
  to?: string
  page?: number
  page_size?: number
}

export interface UploadOptions {
  file: File
  uploadId: string
  parentId: string
  name: string
  note?: string
  nodeId?: string
  baseVersionId?: string
  onProgress?: (percent: number) => void
}

export interface UploadHandle {
  promise: Promise<UploadResult>
  abort: () => void
}

function uploadFile(options: UploadOptions): UploadHandle {
  const xhr = new XMLHttpRequest()
  const params = new URLSearchParams({
    upload_id: options.uploadId,
    name: options.name,
  })
  if (options.parentId) params.set('parent', options.parentId)
  if (options.note) params.set('note', options.note)
  if (options.nodeId) params.set('node', options.nodeId)
  if (options.baseVersionId) params.set('base', options.baseVersionId)

  const promise = new Promise<UploadResult>((resolve, reject) => {
    xhr.open('PUT', `/api/upload?${params.toString()}`)
    xhr.withCredentials = true
    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable && options.onProgress) {
        options.onProgress(Math.round((event.loaded / event.total) * 100))
      }
    }
    xhr.onload = () => {
      let payload: unknown = null
      try {
        payload = JSON.parse(xhr.responseText)
      } catch {
        payload = null
      }
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve(payload as UploadResult)
        return
      }
      notifyUnauthorized(xhr.status)
      const error = (payload as { error?: { code?: string; message?: string } } | null)?.error
      reject(new ApiError(xhr.status, error?.code || 'UPLOAD_FAILED', error?.message || '上传失败', payload))
    }
    xhr.onerror = () => reject(new ApiError(0, 'NETWORK', '网络连接中断，上传未完成'))
    xhr.onabort = () => reject(new ApiError(0, 'ABORTED', '已取消上传'))
    xhr.send(options.file)
  })

  return { promise, abort: () => xhr.abort() }
}

export function formatBytes(value: number): string {
  if (!value || value < 0) return '—'
  if (value < 1024) return `${value} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let size = value / 1024
  let index = 0
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024
    index += 1
  }
  return `${size >= 10 ? size.toFixed(0) : size.toFixed(1)} ${units[index]}`
}

export function newUploadId(): string {
  if (crypto.randomUUID) {
    return crypto.randomUUID().replace(/-/g, '')
  }
  return Array.from(crypto.getRandomValues(new Uint8Array(16)))
    .map((v) => v.toString(16).padStart(2, '0'))
    .join('')
}

export function downloadUrl(nodeId: string, versionId?: string): string {
  return `/api/nodes/${encodeURIComponent(nodeId)}/download${versionId ? `?version=${encodeURIComponent(versionId)}` : ''}`
}

export function folderDownloadUrl(nodeId: string): string {
  return `/api/nodes/${encodeURIComponent(nodeId)}/download-folder`
}
