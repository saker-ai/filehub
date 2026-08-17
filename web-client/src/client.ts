import { appBasePath, appURL } from '@saker/web-shared/base-path'
import type {
  AIReview,
  AIReviewList,
  Asset,
  AssetFilter,
  AssetList,
  AssetReview,
  AssetReviewList,
  AssetStats,
  BulkDeleteResult,
  UploadOpts
} from './types'

export * from './types'

const ASSETS_PATH = '/v1/assets'

function fileHubURL(path: string): string {
  return appURL(appBasePath(import.meta.env.BASE_URL), path)
}

function baseURL(): string {
  return fileHubURL('/v1')
}

export function fileHubPath(path: string): string {
  return fileHubURL(path)
}

export function assetContentURL(assetID: string, opts: { download?: boolean } = {}): string {
  const search = new URLSearchParams()
  if (opts.download) search.set('download', 'true')
  const query = search.toString()
  return fileHubURL(`${ASSETS_PATH}/${encodeURIComponent(assetID)}/content${query ? `?${query}` : ''}`)
}

// assetPreviewURL returns the server-rendered preview (currently a PDF
// converted from office documents). It 404s when preview is unavailable, so
// callers can fall back to download.
export function assetPreviewURL(assetID: string): string {
  return fileHubURL(`${ASSETS_PATH}/${encodeURIComponent(assetID)}/preview`)
}

export function assetThumbnailURL(assetID: string, opts: { width?: number; height?: number; format?: string } = {}): string {
  const search = new URLSearchParams()
  if (opts.width) search.set('width', String(opts.width))
  if (opts.height) search.set('height', String(opts.height))
  if (opts.format) search.set('format', opts.format)
  const query = search.toString()
  return fileHubURL(`${ASSETS_PATH}/${encodeURIComponent(assetID)}/thumbnail${query ? `?${query}` : ''}`)
}

export function getAPIKey(): string {
  return localStorage.getItem('filehub_api_key') || 'dev-filehub-key'
}

export function setAPIKey(key: string) {
  localStorage.setItem('filehub_api_key', key)
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

export async function fetchAssetContentBlob(assetID: string, opts: { download?: boolean } = {}): Promise<Blob> {
  return fetchAssetBlob(assetContentURL(assetID, opts))
}

export async function fetchAssetText(assetID: string): Promise<string> {
  const response = await fetch(assetContentURL(assetID), { headers: authHeaders() })
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
  return response.text()
}

export async function listAssets(params: AssetFilter = {}): Promise<AssetList> {
  const search = new URLSearchParams()
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== '') search.set(key, String(value))
  })
  return request<AssetList>(`${baseURL()}/assets?${search}`)
}

export async function getAsset(id: string): Promise<Asset> {
  return request<Asset>(`${baseURL()}/assets/${encodeURIComponent(id)}`)
}

export async function getStats(): Promise<AssetStats> {
  return request<AssetStats>(`${baseURL()}/assets/stats`)
}

export async function uploadAsset(file: File, opts: UploadOpts, onProgress?: (percent: number) => void): Promise<Asset> {
  const form = new FormData()
  form.set('file', file)
  form.set('purpose', opts.purpose)
  if (opts.tags) form.set('tags', opts.tags)
  if (opts.metadata) form.set('metadata', opts.metadata)
  if (opts.source) form.set('source', opts.source)
  if (onProgress) {
    return uploadWithProgress(`${baseURL()}/assets?on_duplicate=allow`, form, onProgress)
  }
  return request<Asset>(`${baseURL()}/assets?on_duplicate=allow`, {
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
  return request<Asset>(`${baseURL()}/assets/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  })
}

export async function deleteAsset(id: string): Promise<void> {
  await request(`${baseURL()}/assets/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export async function bulkDelete(ids: string[]): Promise<BulkDeleteResult> {
  return request(`${baseURL()}/assets/bulk-delete`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ ids }),
  })
}

export async function presignAsset(id: string, expiresIn = '168h'): Promise<{ url: string; expires_at: number }> {
  return request(`${baseURL()}/assets/${encodeURIComponent(id)}/presign`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ expires_in: expiresIn }),
  })
}

export async function listAssetAIReviews(assetID: string, limit = 20): Promise<AIReviewList> {
  const search = new URLSearchParams({ limit: String(limit) })
  return request<AIReviewList>(`${baseURL()}/assets/${encodeURIComponent(assetID)}/ai-reviews?${search}`)
}

export async function createAssetAIReview(assetID: string, review: { model?: string; verdict: string; score?: number | null; summary?: string; rubric?: string; confidence?: number | null; prompt_version?: string; review_job_id?: string; raw_response_id?: string; metadata?: Record<string, unknown> }): Promise<AIReview> {
  return request<AIReview>(`${baseURL()}/assets/${encodeURIComponent(assetID)}/ai-reviews`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(review),
  })
}

export async function listAssetReviews(params: { status?: string; reviewer?: string; source?: string; limit?: number; offset?: number } = {}): Promise<AssetReviewList> {
  const search = new URLSearchParams()
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== '') search.set(key, String(value))
  })
  return request<AssetReviewList>(`${baseURL()}/reviews?${search}`)
}

export async function createAssetReview(review: { title?: string; status?: string; reference_asset_id?: string; selected_asset_id?: string; reviewer?: string; source?: string; trace_id?: string; metadata?: Record<string, unknown>; asset_ids: string[] }): Promise<AssetReview> {
  return request<AssetReview>(`${baseURL()}/reviews`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(review),
  })
}

export async function getAssetReview(id: string): Promise<AssetReview> {
  return request<AssetReview>(`${baseURL()}/reviews/${encodeURIComponent(id)}`)
}

export async function updateAssetReview(id: string, patch: { title?: string; status?: string; reference_asset_id?: string; selected_asset_id?: string; reviewer?: string; source?: string; trace_id?: string; metadata?: Record<string, unknown> }): Promise<AssetReview> {
  return request<AssetReview>(`${baseURL()}/reviews/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  })
}

export async function updateAssetReviewItem(reviewID: string, assetID: string, patch: { decision: string; note?: string; score?: number | null; metadata?: Record<string, unknown> }): Promise<AssetReview> {
  return request<AssetReview>(`${baseURL()}/reviews/${encodeURIComponent(reviewID)}/items/${encodeURIComponent(assetID)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  })
}
