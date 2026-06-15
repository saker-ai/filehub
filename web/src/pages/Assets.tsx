import { lazy, Suspense, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useSearchParams } from 'react-router-dom'
import { Asset, bulkDelete, listAssets, updateAsset } from '../api/client'
import { AssetCard } from '../components/AssetCard'

const AssetDetail = lazy(() => import('./AssetDetail').then((module) => ({ default: module.AssetDetail })))

export default function Assets() {
  const { t } = useTranslation()
  const [searchParams, setSearchParams] = useSearchParams()
  const [assets, setAssets] = useState<Asset[]>([])
  const [active, setActive] = useState<Asset | null>(null)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [filename, setFilename] = useState(() => searchParams.get('filename') || '')
  const [purpose, setPurpose] = useState(() => searchParams.get('purpose') || '')
  const [tags, setTags] = useState(() => searchParams.get('tags') || '')
  const [status, setStatus] = useState(() => searchParams.get('status') || '')
  const [source, setSource] = useState(() => searchParams.get('source') || '')
  const [contentType, setContentType] = useState(() => searchParams.get('content_type') || '')
  const [metaKey, setMetaKey] = useState(() => searchParams.get('meta_key') || '')
  const [metaValue, setMetaValue] = useState(() => searchParams.get('meta_value') || '')
  const [batchTags, setBatchTags] = useState('')
  const [hasMore, setHasMore] = useState(false)
  const [nextCursor, setNextCursor] = useState('')
  const [cursorStack, setCursorStack] = useState<string[]>([])
  const [view, setView] = useState<'cards' | 'table'>('cards')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [advancedOpen, setAdvancedOpen] = useState(false)

  const rawFilter = useMemo(() => ({
    filename,
    purpose,
    tags,
    status,
    source,
    content_type: contentType,
    meta_key: metaKey,
    meta_value: metaKey ? metaValue : '',
  }), [contentType, filename, metaKey, metaValue, purpose, source, status, tags])
  const debouncedFilter = useDebouncedValue(rawFilter, 300)
  const currentCursor = cursorStack[cursorStack.length - 1] || ''
  const filter = useMemo(() => ({
    ...debouncedFilter,
    cursor: currentCursor,
    limit: 24,
  }), [currentCursor, debouncedFilter])

  useEffect(() => {
    const next = new URLSearchParams()
    Object.entries(rawFilter).forEach(([key, value]) => {
      if (value) next.set(key, String(value))
    })
    setSearchParams(next, { replace: true })
  }, [rawFilter, setSearchParams])

  async function refresh() {
    setLoading(true)
    setError('')
    try {
      const result = await listAssets(filter)
      setAssets(result.data)
      setHasMore(result.has_more)
      setNextCursor(result.next_cursor || '')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void refresh()
  }, [filter])

  function resetPaging() {
    setCursorStack([])
    setNextCursor('')
  }

  async function deleteSelected() {
    if (!window.confirm(t('confirmDeleteSelected'))) return
    await bulkDelete([...selected])
    setSelected(new Set())
    await refresh()
  }

  async function applyTags() {
    const nextTags = batchTags.split(',').map((tag) => tag.trim()).filter(Boolean)
    await Promise.all([...selected].map((id) => updateAsset(id, { tags: nextTags })))
    setSelected(new Set())
    setBatchTags('')
    await refresh()
  }

  function clearFilters() {
    setFilename('')
    setPurpose('')
    setTags('')
    setStatus('')
    setSource('')
    setContentType('')
    setMetaKey('')
    setMetaValue('')
    resetPaging()
  }

  return (
    <div className="page">
      <header className="page-header row">
        <div>
          <h1>{t('assets')}</h1>
          <p>{t('assetsSubtitle')}</p>
        </div>
        <div className="row actions">
          <div className="segmented">
            <button type="button" className={view === 'cards' ? 'active' : ''} onClick={() => setView('cards')}>{t('cardView')}</button>
            <button type="button" className={view === 'table' ? 'active' : ''} onClick={() => setView('table')}>{t('tableView')}</button>
          </div>
          <label className="field compact-field"><span>{t('batchTags')}</span><input value={batchTags} onChange={(event) => setBatchTags(event.target.value)} /></label>
          <button type="button" disabled={selected.size === 0} onClick={applyTags}>{t('applyTags')}</button>
          <button type="button" disabled={selected.size === 0} onClick={deleteSelected}>{t('deleteSelected')}</button>
        </div>
      </header>
      <section className="filters compact-filters">
        <div className="filter-main">
          <label className="field search-field"><span>{t('filename')}</span><input value={filename} onChange={(event) => { resetPaging(); setFilename(event.target.value) }} /></label>
          <label className="field"><span>{t('tags')}</span><input value={tags} onChange={(event) => { resetPaging(); setTags(event.target.value) }} /></label>
          <label className="field"><span>{t('metadataKey')}</span><input value={metaKey} onChange={(event) => { resetPaging(); setMetaKey(event.target.value) }} /></label>
          <label className="field"><span>{t('metadataValue')}</span><input value={metaValue} disabled={!metaKey} onChange={(event) => { resetPaging(); setMetaValue(event.target.value) }} /></label>
          <div className="filter-actions">
            <button type="button" onClick={() => setAdvancedOpen((value) => !value)} aria-expanded={advancedOpen}>{advancedOpen ? t('hideFilters') : t('moreFilters')}</button>
            <button type="button" onClick={clearFilters}>{t('clear')}</button>
          </div>
        </div>
        {advancedOpen ? (
          <div className="filter-advanced">
            <label className="field"><span>{t('purpose')}</span><select value={purpose} onChange={(event) => { resetPaging(); setPurpose(event.target.value) }}>
              <option value="">{t('anyPurpose')}</option>
              {['assistants', 'batch', 'fine-tune', 'media', 'vector-store', 'general'].map((value) => <option key={value}>{value}</option>)}
            </select></label>
            <label className="field"><span>{t('status')}</span><select value={status} onChange={(event) => { resetPaging(); setStatus(event.target.value) }}>
              <option value="">{t('anyStatus')}</option>
              {['uploaded', 'processing', 'ready', 'error'].map((value) => <option key={value}>{value}</option>)}
            </select></label>
            <label className="field"><span>{t('source')}</span><select value={source} onChange={(event) => { resetPaging(); setSource(event.target.value) }}>
              <option value="">{t('anySource')}</option>
              {['upload', 'ai-generated', 'external-url'].map((value) => <option key={value}>{value}</option>)}
            </select></label>
            <label className="field"><span>{t('type')}</span><select value={contentType} onChange={(event) => { resetPaging(); setContentType(event.target.value) }}>
              <option value="">{t('anyType')}</option>
              {['image/', 'video/', 'audio/', 'text/', 'application/pdf', 'model/'].map((value) => <option key={value}>{value}</option>)}
            </select></label>
          </div>
        ) : null}
      </section>
      {error ? <p className="form-error" role="alert">{error}</p> : null}
      {loading ? <p className="muted">{t('loading')}</p> : null}
      {!loading && assets.length === 0 ? <div className="empty-state">{t('noAssets')}</div> : null}
      {view === 'cards' ? (
        <div className="asset-grid">{assets.map((asset) => (
            <AssetCard
              key={asset.id}
              asset={asset}
              selected={selected.has(asset.id)}
              onSelect={(checked) => setSelected((prev) => selectAsset(prev, asset.id, checked))}
              onOpen={() => setActive(asset)}
            />
        ))}</div>
      ) : (
        <table className="asset-table">
          <thead>
            <tr>
              <th>{t('select')}</th>
              <th>{t('filename')}</th>
              <th>{t('purpose')}</th>
              <th>{t('status')}</th>
              <th>{t('type')}</th>
            </tr>
          </thead>
          <tbody>
            {assets.map((asset) => (
              <tr key={asset.id}>
                <td data-label={t('select')}>
                  <label className="checkbox-hit">
                    <input type="checkbox" aria-label={`${t('select')} ${asset.filename}`} checked={selected.has(asset.id)} onChange={(event) => setSelected((prev) => selectAsset(prev, asset.id, event.target.checked))} />
                  </label>
                </td>
                <td data-label={t('filename')}><button type="button" className="link-button" onClick={() => setActive(asset)}>{asset.filename}</button></td>
                <td data-label={t('purpose')}>{asset.purpose}</td>
                <td data-label={t('status')}>{asset.status}</td>
                <td data-label={t('type')}>{asset.content_type}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <div className="row actions">
        <button type="button" disabled={cursorStack.length === 0 || loading} onClick={() => setCursorStack((prev) => prev.slice(0, -1))}>{t('previous')}</button>
        <button type="button" disabled={!hasMore || !nextCursor || loading} onClick={() => setCursorStack((prev) => [...prev, nextCursor])}>{t('next')}</button>
      </div>
      {active ? (
        <Suspense fallback={<p className="muted">{t('loading')}</p>}>
          <AssetDetail
            assetID={active.id}
            onClose={() => setActive(null)}
            onNavigate={(direction) => {
              const index = assets.findIndex((asset) => asset.id === active.id)
              const next = assets[index + direction]
              if (next) setActive(next)
            }}
          />
        </Suspense>
      ) : null}
    </div>
  )
}

function useDebouncedValue<T>(value: T, delayMs: number) {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const timer = window.setTimeout(() => setDebounced(value), delayMs)
    return () => window.clearTimeout(timer)
  }, [delayMs, value])
  return debounced
}

function selectAsset(prev: Set<string>, id: string, checked: boolean) {
  const next = new Set(prev)
  if (checked) next.add(id)
  else next.delete(id)
  return next
}
