import { useEffect, useMemo, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { assetContentURL, createAssetReview, fetchAssetText, getAsset, getAssetReview, updateAssetReview, updateAssetReviewItem, type Asset, type AssetReview } from '../api/client'
import { AuthAudio, AuthFrame, AuthImage, AuthVideo } from '../components/AuthMedia'
import { formatBytes } from '../components/AssetCard'
import { AIReviewResults } from '../components/AIReviews'

type Decision = 'pending' | 'approved' | 'rejected' | 'needs_revision' | 'best'

export default function AssetCompare() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const params = useParams()
  const [searchParams] = useSearchParams()
  const requestedReviewID = params.reviewID || searchParams.get('review_id') || ''
  const ids = useMemo(() => parseIDs(searchParams.get('ids') || ''), [searchParams])
  const [assets, setAssets] = useState<Asset[]>([])
  const [review, setReview] = useState<AssetReview | null>(null)
  const [title, setTitle] = useState('')
  const [reviewer, setReviewer] = useState('')
  const [status, setStatus] = useState('open')
  const [baselineID, setBaselineID] = useState('')
  const [decisions, setDecisions] = useState<Record<string, Decision>>({})
  const [notes, setNotes] = useState<Record<string, string>>({})
  const [saving, setSaving] = useState(false)
  const [notice, setNotice] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (requestedReviewID) {
      let alive = true
      setLoading(true)
      setError('')
      void getAssetReview(requestedReviewID).then(async (loadedReview) => {
        if (!alive) return
        setReview(loadedReview)
        setTitle(loadedReview.title || '')
        setReviewer(loadedReview.reviewer || '')
        setStatus(loadedReview.status || 'open')
        setBaselineID(loadedReview.reference_asset_id || loadedReview.items[0]?.asset_id || '')
        const nextDecisions: Record<string, Decision> = {}
        const nextNotes: Record<string, string> = {}
        loadedReview.items.forEach((item) => {
          nextDecisions[item.asset_id] = (item.decision || 'pending') as Decision
          nextNotes[item.asset_id] = item.note || ''
        })
        setDecisions(nextDecisions)
        setNotes(nextNotes)
        const loadedAssets = await Promise.all(loadedReview.items.map((item) => getAsset(item.asset_id)))
        if (alive) setAssets(loadedAssets)
      }).catch((err) => {
        if (alive) setError(err instanceof Error ? err.message : String(err))
      }).finally(() => {
        if (alive) setLoading(false)
      })
      return () => {
        alive = false
      }
    }
    setReview(null)
    if (ids.length === 0) {
      setAssets([])
      return
    }
    let alive = true
    setLoading(true)
    setError('')
    void Promise.all(ids.map((id) => getAsset(id))).then((loaded) => {
      if (!alive) return
      setAssets(loaded)
      setBaselineID((current) => current && loaded.some((asset) => asset.id === current) ? current : loaded[0]?.id || '')
      setTitle((current) => current || defaultReviewTitle(loaded))
    }).catch((err) => {
      if (alive) setError(err instanceof Error ? err.message : String(err))
    }).finally(() => {
      if (alive) setLoading(false)
    })
    return () => {
      alive = false
    }
  }, [ids, requestedReviewID])

  const baseline = assets.find((asset) => asset.id === baselineID) || assets[0]
  const candidates = assets.filter((asset) => asset.id !== baseline?.id)
  const summary = summarize(assets)
  const selectedAssetID = Object.entries(decisions).find(([, decision]) => decision === 'best')?.[0] || ''

  async function saveReview() {
    if (assets.length === 0) return
    setSaving(true)
    setError('')
    setNotice('')
    try {
      let current = review
      if (!current) {
        current = await createAssetReview({
          title: title.trim() || defaultReviewTitle(assets),
          status,
          reference_asset_id: baseline?.id || '',
          selected_asset_id: selectedAssetID,
          reviewer: reviewer.trim(),
          source: 'human',
          asset_ids: assets.map((asset) => asset.id),
        })
        setReview(current)
        navigate(`/reviews/${encodeURIComponent(current.id)}`, { replace: true })
      } else {
        current = await updateAssetReview(current.id, {
          title: title.trim() || current.title,
          status,
          reference_asset_id: baseline?.id || '',
          selected_asset_id: selectedAssetID,
          reviewer: reviewer.trim(),
          source: 'human',
        })
        setReview(current)
      }
      for (const asset of assets) {
        const updated = await updateAssetReviewItem(current.id, asset.id, {
          decision: decisions[asset.id] || 'pending',
          note: notes[asset.id] || '',
        })
        current = updated
      }
      setReview(current)
      setNotice(t('reviewSaved'))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  if (ids.length === 0) {
    return (
      <div className="page">
        <header className="page-header">
          <h1>{t('compare')}</h1>
          <p>{t('compareEmpty')}</p>
        </header>
        <Link className="button-link" to="/assets">{t('assets')}</Link>
      </div>
    )
  }

  return (
    <div className="page compare-page">
      <header className="page-header compare-header">
        <div>
          <h1>{t('batchCompare')}</h1>
          <p>{t('batchCompareSubtitle', { count: assets.length || ids.length })}</p>
        </div>
        <div className="compare-actions">
          <button type="button" disabled={saving || assets.length === 0} onClick={saveReview}>{saving ? t('working') : review ? t('saveReview') : t('createReview')}</button>
          <Link className="button-link secondary" to="/assets">{t('assets')}</Link>
        </div>
      </header>

      {error ? <p className="form-error" role="alert">{error}</p> : null}
      {notice ? <p className="success" role="status">{notice}</p> : null}
      {loading ? <div className="loading-strip" role="status">{t('loading')}</div> : null}

      {assets.length > 0 ? (
        <>
          <section className="compare-toolbar" aria-label={t('compareControls')}>
            <label className="field">
              <span>{t('reviewTitle')}</span>
              <input value={title} onChange={(event) => setTitle(event.target.value)} />
            </label>
            <label className="field">
              <span>{t('reviewer')}</span>
              <input value={reviewer} onChange={(event) => setReviewer(event.target.value)} />
            </label>
            <label className="field">
              <span>{t('status')}</span>
              <select value={status} onChange={(event) => setStatus(event.target.value)}>
                <option value="open">{t('reviewStatusOpen')}</option>
                <option value="completed">{t('reviewStatusCompleted')}</option>
                <option value="archived">{t('reviewStatusArchived')}</option>
              </select>
            </label>
            <label className="field">
              <span>{t('baseline')}</span>
              <select value={baseline?.id || ''} onChange={(event) => setBaselineID(event.target.value)}>
                {assets.map((asset) => <option key={asset.id} value={asset.id}>{asset.filename}</option>)}
              </select>
            </label>
            <dl className="compare-summary">
              <div><dt>{t('total')}</dt><dd>{assets.length}</dd></div>
              <div><dt>{t('type')}</dt><dd>{summary.types}</dd></div>
              <div><dt>{t('storage')}</dt><dd>{formatBytes(summary.bytes)}</dd></div>
            </dl>
          </section>

          {baseline ? (
            <section className="compare-baseline">
              <AssetComparePanel
                asset={baseline}
                label={t('baseline')}
                decision={decisions[baseline.id] || 'pending'}
                note={notes[baseline.id] || ''}
                onDecision={(decision) => setDecisions((prev) => ({ ...prev, [baseline.id]: decision }))}
                onNote={(note) => setNotes((prev) => ({ ...prev, [baseline.id]: note }))}
              />
            </section>
          ) : null}

          <section className="compare-grid" aria-label={t('compareCandidates')}>
            {candidates.map((asset) => (
              <AssetComparePanel
                key={asset.id}
                asset={asset}
                baseline={baseline}
                label={t('candidate')}
                decision={decisions[asset.id] || 'pending'}
                note={notes[asset.id] || ''}
                onDecision={(decision) => setDecisions((prev) => ({ ...prev, [asset.id]: decision }))}
                onNote={(note) => setNotes((prev) => ({ ...prev, [asset.id]: note }))}
              />
            ))}
          </section>
        </>
      ) : null}
    </div>
  )
}

function AssetComparePanel({ asset, baseline, label, decision, note, onDecision, onNote }: { asset: Asset; baseline?: Asset; label: string; decision: Decision; note: string; onDecision: (decision: Decision) => void; onNote: (note: string) => void }) {
  const { t } = useTranslation()
  return (
    <article className={`compare-card decision-${decision}`}>
      <header className="compare-card-header">
        <div>
          <span className="compare-label">{label}</span>
          <h2 title={asset.filename}>{asset.filename}</h2>
          <p>{asset.id}</p>
        </div>
        <span className={`status ${asset.status}`}>{asset.status}</span>
      </header>
      <div className="preview compare-preview">
        <ComparePreview asset={asset} />
      </div>
      <dl className="compare-fields">
        <CompareField label={t('type')} value={asset.content_type || 'unknown'} baseline={baseline?.content_type || ''} />
        <CompareField label={t('size')} value={formatBytes(asset.bytes)} baseline={baseline ? formatBytes(baseline.bytes) : ''} changed={Boolean(baseline && asset.bytes !== baseline.bytes)} />
        <CompareField label={t('checksum')} value={asset.checksum || '-'} baseline={baseline?.checksum || ''} compact changed={Boolean(baseline && asset.checksum !== baseline.checksum)} />
        <CompareField label={t('source')} value={asset.source || '-'} baseline={baseline?.source || ''} />
        <CompareField label={t('created')} value={formatTimestamp(asset.created_at)} baseline={baseline ? formatTimestamp(baseline.created_at) : ''} />
      </dl>
      <AICompareMetadata asset={asset} baseline={baseline} />
      <AIReviewResults assetID={asset.id} />
      <div className="review-controls">
        <label className="field">
          <span>{t('decision')}</span>
          <select value={decision} onChange={(event) => onDecision(event.target.value as Decision)}>
            <option value="pending">{t('decisionPending')}</option>
            <option value="approved">{t('decisionApproved')}</option>
            <option value="rejected">{t('decisionRejected')}</option>
            <option value="needs_revision">{t('decisionNeedsRevision')}</option>
            <option value="best">{t('decisionBest')}</option>
          </select>
        </label>
        <label className="field">
          <span>{t('reviewNote')}</span>
          <textarea value={note} rows={3} onChange={(event) => onNote(event.target.value)} />
        </label>
      </div>
    </article>
  )
}

function ComparePreview({ asset }: { asset: Asset }) {
  const { t } = useTranslation()
  const type = asset.content_type || ''
  const src = assetContentURL(asset.id)
  if (type.startsWith('image/')) return <AuthImage src={src} alt={asset.filename} />
  if (type.startsWith('video/')) return <AuthVideo src={src} />
  if (type.startsWith('audio/')) return <AuthAudio src={src} />
  if (type === 'application/pdf') return <AuthFrame src={src} title={asset.filename} />
  if (type.startsWith('text/') || type.includes('json') || type.includes('xml') || /\.md$/i.test(asset.filename)) return <CompactTextPreview assetID={asset.id} />
  return <div className="file-preview">{t('file')}</div>
}

function CompactTextPreview({ assetID }: { assetID: string }) {
  const [text, setText] = useState('')
  const [error, setError] = useState('')
  useEffect(() => {
    let alive = true
    setText('')
    setError('')
    void fetchAssetText(assetID).then((value) => {
      if (alive) setText(value)
    }).catch((err) => {
      if (alive) setError(err instanceof Error ? err.message : String(err))
    })
    return () => {
      alive = false
    }
  }, [assetID])
  if (error) return <span className="preview-error">{error}</span>
  if (!text) return <span className="preview-loading">Loading</span>
  return <pre className="code text-preview compact-text-preview">{text}</pre>
}

function CompareField({ label, value, baseline, compact = false, changed = false }: { label: string; value: string; baseline?: string; compact?: boolean; changed?: boolean }) {
  return (
    <div className={changed ? 'changed' : ''}>
      <dt>{label}</dt>
      <dd className={compact ? 'compact-value' : ''}>{value}</dd>
      {baseline && changed ? <dd className="baseline-value">{baseline}</dd> : null}
    </div>
  )
}

function AICompareMetadata({ asset, baseline }: { asset: Asset; baseline?: Asset }) {
  const { t } = useTranslation()
  const current = extractAI(asset.metadata)
  const base = baseline ? extractAI(baseline.metadata) : {}
  const keys = ['model', 'prompt', 'negative_prompt', 'seed', 'task_id', 'parent_asset_id', 'variant_index'].filter((key) => current[key] !== undefined || base[key] !== undefined)
  if (keys.length === 0) return null
  return (
    <section className="ai-compare">
      <h3>{t('aiMetadata')}</h3>
      <dl>
        {keys.map((key) => {
          const value = stringifyMeta(current[key])
          const baselineValue = stringifyMeta(base[key])
          const changed = Boolean(baseline && value !== baselineValue)
          return <CompareField key={key} label={key} value={value || '-'} baseline={baselineValue} compact changed={changed} />
        })}
      </dl>
    </section>
  )
}

function parseIDs(value: string) {
  return [...new Set(value.split(',').map((id) => id.trim()).filter(Boolean))].slice(0, 12)
}

function summarize(assets: Asset[]) {
  const types = new Set(assets.map((asset) => asset.content_type || 'unknown'))
  return {
    bytes: assets.reduce((total, asset) => total + asset.bytes, 0),
    types: types.size === 1 ? [...types][0] : `${types.size}`,
  }
}

function defaultReviewTitle(assets: Asset[]) {
  if (assets.length === 0) return 'Asset review'
  return `${assets[0].filename}${assets.length > 1 ? ` + ${assets.length - 1}` : ''}`
}

function extractAI(metadata?: Record<string, unknown>) {
  const ai = metadata?.ai
  if (ai && typeof ai === 'object' && !Array.isArray(ai)) return ai as Record<string, unknown>
  return metadata || {}
}

function stringifyMeta(value: unknown) {
  if (value === undefined || value === null) return ''
  if (typeof value === 'string') return value
  return JSON.stringify(value)
}

function formatTimestamp(value: number) {
  if (!value) return '-'
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value * 1000))
}
