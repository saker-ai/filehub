import { assetHubURL } from '../basePath'

export type Asset = {
  id: string
  object: 'asset' | 'file'
  filename: string
  content_type?: string
  bytes: number
  purpose: string
  status: string
  source?: string
  checksum?: string
  tags?: string[]
  metadata?: Record<string, unknown>
  created_at: number
  updated_at?: number
  expires_at?: number | null
}

export type AssetList = {
  object: 'list'
  data: Asset[]
  has_more: boolean
  next_cursor?: string
}

export type AssetStats = {
  total: number
  total_bytes: number
  by_purpose: Record<string, number>
  by_content_type: Record<string, number>
  by_source: Record<string, number>
  by_status: Record<string, number>
}

export type AssetFilter = {
  filename?: string
  purpose?: string
  status?: string
  source?: string
  tags?: string
  content_type?: string
  meta_key?: string
  meta_value?: string
  cursor?: string
  limit?: number
  offset?: number
}

export type UploadOpts = {
  purpose: string
  tags?: string
  metadata?: string
  source?: string
}

export type BulkDeleteResult = {
  object: 'bulk_delete'
  data: Array<{ id: string; deleted: boolean; error: string }>
}

const BASE = assetHubURL('/v1')

export function assetHubPath(path: string): string {
  return assetHubURL(path)
}

export function getAPIKey(): string {
  return localStorage.getItem('assethub_api_key') || 'dev-assethub-key'
}

export function setAPIKey(key: string) {
  localStorage.setItem('assethub_api_key', key)
}

export function authHeaders(init?: HeadersInit): Headers {
  const headers = new Headers(init)
  const key = getAPIKey()
  if (key) headers.set('Authorization', `Bearer ${key}`)
  return headers
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
  return response.json() as Promise<T>
}

export async function fetchAssetBlob(path: string): Promise<Blob> {
  const response = await fetch(path, { headers: authHeaders() })
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
  return response.blob()
}

export async function listAssets(params: AssetFilter = {}): Promise<AssetList> {
  const search = new URLSearchParams()
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== '') search.set(key, String(value))
  })
  return request<AssetList>(`${BASE}/assets?${search}`)
}

export async function getAsset(id: string): Promise<Asset> {
  return request<Asset>(`${BASE}/assets/${encodeURIComponent(id)}`)
}

export async function getStats(): Promise<AssetStats> {
  return request<AssetStats>(`${BASE}/assets/stats`)
}

export async function uploadAsset(file: File, opts: UploadOpts, onProgress?: (percent: number) => void): Promise<Asset> {
  const form = new FormData()
  form.set('file', file)
  form.set('purpose', opts.purpose)
  if (opts.tags) form.set('tags', opts.tags)
  if (opts.metadata) form.set('metadata', opts.metadata)
  if (opts.source) form.set('source', opts.source)
  if (onProgress) {
    return uploadWithProgress(`${BASE}/assets?on_duplicate=allow`, form, onProgress)
  }
  return request<Asset>(`${BASE}/assets?on_duplicate=allow`, {
    method: 'POST',
    body: form,
  })
}

function uploadWithProgress(path: string, body: FormData, onProgress: (percent: number) => void): Promise<Asset> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('POST', path)
    const key = getAPIKey()
    if (key) xhr.setRequestHeader('Authorization', `Bearer ${key}`)
    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable) onProgress(Math.round((event.loaded / event.total) * 100))
    }
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        onProgress(100)
        resolve(JSON.parse(xhr.responseText) as Asset)
        return
      }
      try {
        const body = JSON.parse(xhr.responseText)
        reject(new Error(body?.error?.message || `${xhr.status} ${xhr.statusText}`))
      } catch {
        reject(new Error(`${xhr.status} ${xhr.statusText}`))
      }
    }
    xhr.onerror = () => reject(new Error('upload failed'))
    xhr.send(body)
  })
}

export async function updateAsset(id: string, patch: { tags?: string[]; metadata?: Record<string, unknown> }): Promise<Asset> {
  return request<Asset>(`${BASE}/assets/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  })
}

export async function deleteAsset(id: string): Promise<void> {
  await request(`${BASE}/assets/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export async function bulkDelete(ids: string[]): Promise<BulkDeleteResult> {
  return request(`${BASE}/assets/bulk-delete`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ids }),
  })
}

export async function presignAsset(id: string, expiresIn = '168h'): Promise<{ url: string; expires_at: number }> {
  return request(`${BASE}/assets/${encodeURIComponent(id)}/presign`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ expires_in: expiresIn }),
  })
}
