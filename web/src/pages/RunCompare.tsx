import { useEffect, useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { assetHubPath, listAssets, type Asset } from '../api/client'
import { AuthAudio, AuthFrame, AuthImage, AuthVideo } from '../components/AuthMedia'
import { formatBytes } from '../components/AssetCard'

type RunColumn = {
  id: string
  label: string
  status: 'idle' | 'loading' | 'loaded' | 'error'
  error: string
  assets: Asset[]
}

type Row = {
  key: string
  prompt: string
  assetsByRun: Record<string, Asset>
}

const MAX_RUNS = 4
const DEFAULT_RUNS = [
  { id: '', label: 'Baseline' },
  { id: '', label: 'Tuned' },
]

export default function RunCompare() {
  const { t } = useTranslation()
  const [searchParams, setSearchParams] = useSearchParams()
  const [runs, setRuns] = useState<RunColumn[]>(() => initialRuns(searchParams))
  const [groupKey, setGroupKey] = useState(() => searchParams.get('group_key') || 'prompt_id')
  const [contentType, setContentType] = useState(() => searchParams.get('content_type') || 'video/')
  const [notice, setNotice] = useState('')

  const activeRuns = runs.filter((run) => run.id.trim())
  const rows = useMemo(() => buildRows(runs, groupKey), [groupKey, runs])
  const loadedCount = activeRuns.reduce((total, run) => total + run.assets.length, 0)
  const completeRows = rows.filter((row) => activeRuns.every((run) => row.assetsByRun[run.id])).length

  useEffect(() => {
    const next = new URLSearchParams()
    const ids = runs.map((run) => run.id.trim()).filter(Boolean)
    const labels = runs.map((run) => run.label.trim()).filter(Boolean)
    if (ids.length) next.set('runs', ids.join(','))
    if (labels.length) next.set('labels', labels.join(','))
    if (groupKey) next.set('group_key', groupKey)
    if (contentType) next.set('content_type', contentType)
    setSearchParams(next, { replace: true })
  }, [contentType, groupKey, runs, setSearchParams])

  async function loadRun(index: number) {
    const run = runs[index]
    const runID = run?.id.trim()
    if (!runID) return
    setNotice('')
    setRuns((prev) => updateRun(prev, index, { status: 'loading', error: '', assets: [] }))
    try {
      const result = await listAssets({
        meta_key: 'run_id',
        meta_value: runID,
        content_type: contentType,
        status: 'ready',
        limit: 100,
      })
      setRuns((prev) => updateRun(prev, index, { status: 'loaded', assets: result.data }))
      if (result.has_more) setNotice(t('runCompareLimitNotice'))
    } catch (err) {
      setRuns((prev) => updateRun(prev, index, { status: 'error', error: err instanceof Error ? err.message : String(err), assets: [] }))
    }
  }

  async function loadAll() {
    await Promise.all(runs.map((run, index) => run.id.trim() ? loadRun(index) : Promise.resolve()))
  }

  function setRun(index: number, patch: Partial<RunColumn>) {
    setRuns((prev) => updateRun(prev, index, patch))
  }

  function addRun() {
    if (runs.length >= MAX_RUNS) return
    setRuns((prev) => [...prev, { id: '', label: `${t('run')} ${prev.length + 1}`, status: 'idle', error: '', assets: [] }])
  }

  function removeRun(index: number) {
    if (runs.length <= 2) return
    setRuns((prev) => prev.filter((_, itemIndex) => itemIndex !== index))
  }

  return (
    <div className="page run-compare-page">
      <header className="page-header compare-header">
        <div>
          <h1>{t('runCompare')}</h1>
          <p>{t('runCompareSubtitle')}</p>
        </div>
        <div className="compare-actions">
          <button type="button" onClick={loadAll} disabled={activeRuns.length < 2 || runs.some((run) => run.status === 'loading')}>
            {runs.some((run) => run.status === 'loading') ? t('loading') : t('loadRuns')}
          </button>
          <Link className="button-link secondary" to="/assets">{t('assets')}</Link>
        </div>
      </header>

      <section className="run-setup" aria-label={t('runCompareControls')}>
        <div className="run-options">
          <label className="field">
            <span>{t('alignBy')}</span>
            <input value={groupKey} onChange={(event) => setGroupKey(event.target.value)} />
          </label>
          <label className="field">
            <span>{t('type')}</span>
            <select value={contentType} onChange={(event) => setContentType(event.target.value)}>
              <option value="video/">video/</option>
              <option value="image/">image/</option>
              <option value="audio/">audio/</option>
              <option value="">{t('anyType')}</option>
            </select>
          </label>
          <dl className="run-summary">
            <div><dt>{t('runs')}</dt><dd>{activeRuns.length}</dd></div>
            <div><dt>{t('assets')}</dt><dd>{loadedCount}</dd></div>
            <div><dt>{t('alignedRows')}</dt><dd>{completeRows}/{rows.length}</dd></div>
          </dl>
        </div>
        <div className="run-column-editor">
          {runs.map((run, index) => (
            <article className="run-column-card" key={index}>
              <header>
                <strong>{index === 0 ? t('baseline') : `${t('run')} ${index + 1}`}</strong>
                {runs.length > 2 ? <button type="button" className="ghost-button" onClick={() => removeRun(index)}>{t('remove')}</button> : null}
              </header>
              <label className="field">
                <span>{t('runID')}</span>
                <input value={run.id} placeholder="video-run-20260617-a" onChange={(event) => setRun(index, { id: event.target.value, status: 'idle', assets: [], error: '' })} />
              </label>
              <label className="field">
                <span>{t('label')}</span>
                <input value={run.label} onChange={(event) => setRun(index, { label: event.target.value })} />
              </label>
              <button type="button" className="secondary-button" disabled={!run.id.trim() || run.status === 'loading'} onClick={() => loadRun(index)}>
                {run.status === 'loading' ? t('loading') : t('loadRun')}
              </button>
              <p className={`run-load-state ${run.status}`}>{runStatus(run, t)}</p>
              {run.error ? <p className="form-error" role="alert">{run.error}</p> : null}
            </article>
          ))}
          <button type="button" className="add-run-button" disabled={runs.length >= MAX_RUNS} onClick={addRun}>{t('addRun')}</button>
        </div>
      </section>

      {notice ? <p className="success" role="status">{notice}</p> : null}
      {activeRuns.length < 2 ? (
        <div className="empty-state">
          <strong>{t('runCompareNeedsRuns')}</strong>
          <p>{t('runCompareNeedsRunsHint')}</p>
        </div>
      ) : null}
      {activeRuns.length >= 2 && rows.length === 0 && !runs.some((run) => run.status === 'loading') ? (
        <div className="empty-state">
          <strong>{t('runCompareEmpty')}</strong>
          <p>{t('runCompareEmptyHint')}</p>
        </div>
      ) : null}
      {rows.length > 0 ? (
        <section className="run-matrix" aria-label={t('runCompareMatrix')}>
          <div className="run-matrix-header" style={{ gridTemplateColumns: `minmax(220px, 0.85fr) repeat(${activeRuns.length}, minmax(280px, 1fr))` }}>
            <div>{t('promptTask')}</div>
            {activeRuns.map((run, index) => <div key={run.id}>{run.label || (index === 0 ? t('baseline') : run.id)}</div>)}
          </div>
          {rows.map((row) => (
            <article className="run-matrix-row" key={row.key} style={{ gridTemplateColumns: `minmax(220px, 0.85fr) repeat(${activeRuns.length}, minmax(280px, 1fr))` }}>
              <div className="run-row-label">
                <strong title={row.key}>{row.key}</strong>
                <p>{row.prompt || t('noPrompt')}</p>
                <Link className={`button-link${rowAssetIDs(row, activeRuns).length < 2 ? ' disabled' : ''}`} to={`/compare?ids=${encodeURIComponent(rowAssetIDs(row, activeRuns).join(','))}`} onClick={(event) => {
                  if (rowAssetIDs(row, activeRuns).length < 2) event.preventDefault()
                }}>{t('deepCompare')}</Link>
              </div>
              {activeRuns.map((run, index) => (
                <RunAssetCell key={run.id} asset={row.assetsByRun[run.id]} baseline={index === 0 ? undefined : row.assetsByRun[activeRuns[0].id]} />
              ))}
            </article>
          ))}
        </section>
      ) : null}
    </div>
  )
}

function RunAssetCell({ asset, baseline }: { asset?: Asset; baseline?: Asset }) {
  const { t } = useTranslation()
  if (!asset) {
    return (
      <div className="run-cell missing">
        <span>{t('missingAsset')}</span>
      </div>
    )
  }
  return (
    <div className="run-cell">
      <div className="preview run-preview"><RunPreview asset={asset} /></div>
      <header>
        <strong title={asset.filename}>{asset.filename}</strong>
        <span className={`status ${asset.status}`}>{asset.status}</span>
      </header>
      <dl>
        <MetaField label={t('size')} value={formatBytes(asset.bytes)} changed={Boolean(baseline && baseline.bytes !== asset.bytes)} />
        <MetaField label="model" value={metaString(asset.metadata, 'model')} baseline={metaString(baseline?.metadata, 'model')} />
        <MetaField label="seed" value={nestedMetaString(asset.metadata, 'seed') || nestedMetaString(asset.metadata, 'params.seed')} baseline={nestedMetaString(baseline?.metadata, 'seed') || nestedMetaString(baseline?.metadata, 'params.seed')} />
        <MetaField label="cfg" value={nestedMetaString(asset.metadata, 'cfg') || nestedMetaString(asset.metadata, 'params.cfg')} baseline={nestedMetaString(baseline?.metadata, 'cfg') || nestedMetaString(baseline?.metadata, 'params.cfg')} />
      </dl>
    </div>
  )
}

function RunPreview({ asset }: { asset: Asset }) {
  const { t } = useTranslation()
  const type = asset.content_type || ''
  const content = assetHubPath(`/v1/assets/${asset.id}/content`)
  if (type.startsWith('video/')) return <AuthVideo src={content} />
  if (type.startsWith('image/')) return <AuthImage src={content} alt={asset.filename} />
  if (type.startsWith('audio/')) return <AuthAudio src={content} />
  if (type === 'application/pdf') return <AuthFrame src={content} title={asset.filename} />
  return <div className="file-preview">{t('file')}</div>
}

function MetaField({ label, value, baseline, changed }: { label: string; value: string; baseline?: string; changed?: boolean }) {
  const isChanged = changed || Boolean(baseline && value !== baseline)
  return (
    <div className={isChanged ? 'changed' : ''}>
      <dt>{label}</dt>
      <dd>{value || '-'}</dd>
    </div>
  )
}

function initialRuns(searchParams: URLSearchParams): RunColumn[] {
  const ids = (searchParams.get('runs') || '').split(',').map((value) => value.trim())
  const labels = (searchParams.get('labels') || '').split(',').map((value) => value.trim())
  const source = ids.some(Boolean) ? ids.slice(0, MAX_RUNS).map((id, index) => ({ id, label: labels[index] || DEFAULT_RUNS[index]?.label || `Run ${index + 1}` })) : DEFAULT_RUNS
  return source.map((run) => ({ ...run, status: 'idle', error: '', assets: [] }))
}

function updateRun(runs: RunColumn[], index: number, patch: Partial<RunColumn>) {
  return runs.map((run, itemIndex) => itemIndex === index ? { ...run, ...patch } : run)
}

function buildRows(runs: RunColumn[], groupKey: string): Row[] {
  const rowMap = new Map<string, Row>()
  for (const run of runs.filter((item) => item.id.trim())) {
    for (const asset of run.assets) {
      const key = alignmentKey(asset, groupKey)
      const row = rowMap.get(key) || { key, prompt: metaString(asset.metadata, 'prompt'), assetsByRun: {} }
      if (!row.prompt) row.prompt = metaString(asset.metadata, 'prompt')
      row.assetsByRun[run.id] = asset
      rowMap.set(key, row)
    }
  }
  return [...rowMap.values()].sort((left, right) => new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' }).compare(left.key, right.key))
}

function alignmentKey(asset: Asset, groupKey: string) {
  return metaString(asset.metadata, groupKey) || metaString(asset.metadata, 'prompt_id') || metaString(asset.metadata, 'task_id') || metaString(asset.metadata, 'shot_id') || asset.filename
}

function rowAssetIDs(row: Row, runs: RunColumn[]) {
  return runs.map((run) => row.assetsByRun[run.id]?.id).filter(Boolean) as string[]
}

function metaString(metadata: Record<string, unknown> | undefined, key: string) {
  const value = metadata?.[key]
  if (value === undefined || value === null) return ''
  if (typeof value === 'object') return JSON.stringify(value)
  return String(value)
}

function nestedMetaString(metadata: Record<string, unknown> | undefined, path: string) {
  const parts = path.split('.')
  let value: unknown = metadata
  for (const part of parts) {
    if (!value || typeof value !== 'object' || Array.isArray(value)) return ''
    value = (value as Record<string, unknown>)[part]
  }
  if (value === undefined || value === null) return ''
  if (typeof value === 'object') return JSON.stringify(value)
  return String(value)
}

function runStatus(run: RunColumn, t: (key: string, options?: Record<string, unknown>) => string) {
  if (!run.id.trim()) return t('runNotConfigured')
  if (run.status === 'loading') return t('loading')
  if (run.status === 'loaded') return t('runLoadedCount', { count: run.assets.length })
  if (run.status === 'error') return t('runLoadFailed')
  return t('runReadyToLoad')
}
