import { authHeaders, fileHubPath } from './client'

export type Workspace = {
  id: string
  object: 'workspace'
  name: string
  description?: string
  sequence: number
  created_at: number
  updated_at: number
  deleted_at?: number | null
}

export type WorkspaceList = {
  object: 'list'
  data: Workspace[]
  has_more: boolean
  next_cursor?: string
}

export type WorkspaceRevision = {
  id: string
  kind: 'put' | 'delete'
  asset_id?: string
  bytes?: number
  checksum?: string
  mode?: number
  actor_id?: string
  device_id?: string
  session_id?: string
  note?: string
  created_at: number
}

export type WorkspaceEntry = {
  path: string
  revision: WorkspaceRevision
}

export type WorkspaceTreeList = {
  object: 'list'
  data: WorkspaceEntry[]
  has_more: boolean
  next_cursor?: string
}

export type WorkspaceHistoryList = {
  object: 'list'
  data: WorkspaceRevision[]
  has_more: boolean
  next_cursor?: string
}

export type WorkspaceChange = {
  sequence: number
  path: string
  kind: 'put' | 'delete' | 'conflict'
  revision: WorkspaceRevision
}

export type WorkspaceChangeList = {
  object: 'list'
  data: WorkspaceChange[]
  has_more: boolean
  next_sequence?: number
}

export type CommitOperationResult = {
  path: string
  kind: string
  resolution: string
  revision?: WorkspaceRevision
  final_path?: string
  kept_revision_id?: string
}

export type WorkspaceShare = {
  object: 'share'
  id: string
  token_hint?: string
  path: string
  creator_id?: string
  expires_at?: number | null
  revoked_at?: number | null
  created_at: number
}

export type WorkspaceShareList = {
  object: 'list'
  data: WorkspaceShare[]
  has_more: boolean
}

export type ReadStat = {
  path: string
  day: string
  kind: string
  count: number
}

export type ReadStatsResponse = {
  object: 'list'
  data: ReadStat[]
}

function workspaceURL(workspaceID: string, suffix = ''): string {
  return fileHubPath(`/v1/workspaces/${encodeURIComponent(workspaceID)}${suffix}`)
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = authHeaders(init.headers)
  const response = await fetch(path, { ...init, headers })
  if (!response.ok) {
    let message = `${response.status} ${response.statusText}`
    try {
      const body = await response.json()
      message = body?.error?.message || message
    } catch {
      message = await response.text()
    }
    throw new Error(message)
  }
  if (response.status === 204) {
    return undefined as T
  }
  return response.json() as Promise<T>
}

function withParams(path: string, params: Record<string, string | number | undefined>): string {
  const search = new URLSearchParams()
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== '') search.set(key, String(value))
  })
  const query = search.toString()
  return query ? `${path}?${query}` : path
}

export function createWorkspace(input: { name: string; description?: string }): Promise<Workspace> {
  return request<Workspace>(fileHubPath('/v1/workspaces'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  })
}

export function listWorkspaces(params: { limit?: number; cursor?: string } = {}): Promise<WorkspaceList> {
  return request<WorkspaceList>(withParams(fileHubPath('/v1/workspaces'), params))
}

export function getWorkspace(id: string): Promise<Workspace> {
  return request<Workspace>(workspaceURL(id))
}

export function updateWorkspace(id: string, patch: { name?: string; description?: string }): Promise<Workspace> {
  return request<Workspace>(workspaceURL(id), {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  })
}

export function deleteWorkspace(id: string): Promise<void> {
  return request<void>(workspaceURL(id), { method: 'DELETE' })
}

export function listWorkspaceTree(id: string, params: { prefix?: string; cursor?: string; limit?: number } = {}): Promise<WorkspaceTreeList> {
  return request<WorkspaceTreeList>(withParams(workspaceURL(id, '/tree'), params))
}

export function getWorkspaceEntry(id: string, path: string): Promise<WorkspaceEntry> {
  return request<WorkspaceEntry>(withParams(workspaceURL(id, '/entries'), { path }))
}

export function listWorkspaceHistory(id: string, params: { path: string; cursor?: string; limit?: number }): Promise<WorkspaceHistoryList> {
  return request<WorkspaceHistoryList>(withParams(workspaceURL(id, '/history'), params))
}

export function listWorkspaceChanges(id: string, params: { after?: number; limit?: number } = {}): Promise<WorkspaceChangeList> {
  return request<WorkspaceChangeList>(withParams(workspaceURL(id, '/changes'), params))
}

export function restoreWorkspaceRevision(id: string, input: { path: string; revision_id: string; note?: string }): Promise<{ revision: WorkspaceRevision }> {
  return request<{ revision: WorkspaceRevision }>(workspaceURL(id, '/restore'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  })
}

export function createWorkspaceShare(id: string, input: { path: string; expires_in?: string }): Promise<WorkspaceShare & { token?: string; url?: string }> {
  return request<WorkspaceShare & { token?: string; url?: string }>(workspaceURL(id, '/shares'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  })
}

export function listWorkspaceShares(id: string): Promise<WorkspaceShareList> {
  return request<WorkspaceShareList>(workspaceURL(id, '/shares'))
}

export function revokeWorkspaceShare(id: string, shareID: string): Promise<void> {
  return request<void>(workspaceURL(id, `/shares/${encodeURIComponent(shareID)}`), { method: 'DELETE' })
}

export function workspaceShareURL(token: string, opts: { inline?: boolean } = {}): string {
  const suffix = opts.inline ? '?inline=1' : ''
  return fileHubPath(`/s/${encodeURIComponent(token)}${suffix}`)
}

export function readWorkspaceStats(id: string, params: { prefix?: string; days?: number } = {}): Promise<ReadStatsResponse> {
  return request<ReadStatsResponse>(withParams(workspaceURL(id, '/read-stats'), params))
}
