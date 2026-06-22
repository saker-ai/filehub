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

export type AIReview = {
  id: string
  object: 'ai_review'
  asset_id: string
  model?: string
  verdict: string
  score?: number | null
  summary?: string
  rubric?: string
  confidence?: number | null
  prompt_version?: string
  review_job_id?: string
  raw_response_id?: string
  metadata?: Record<string, unknown>
  created_at: number
  updated_at?: number
}

export type AIReviewList = {
  object: 'list'
  data: AIReview[]
  has_more: boolean
}

export type AssetReviewItem = {
  id: string
  object: 'asset_review_item'
  review_id: string
  asset_id: string
  decision: string
  note?: string
  score?: number | null
  metadata?: Record<string, unknown>
  created_at: number
  updated_at?: number
}

export type AssetReview = {
  id: string
  object: 'asset_review'
  title: string
  status: string
  reference_asset_id?: string
  selected_asset_id?: string
  reviewer?: string
  source?: string
  trace_id?: string
  metadata?: Record<string, unknown>
  items: AssetReviewItem[]
  created_at: number
  updated_at?: number
  completed_at?: number | null
}

export type AssetReviewList = {
  object: 'list'
  data: AssetReview[]
  has_more: boolean
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
