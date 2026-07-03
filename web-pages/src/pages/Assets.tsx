import { lazy, Suspense, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useSearchParams } from 'react-router-dom'
import { Asset, bulkDelete, listAssets, updateAsset } from '@saker/filehub-client'
import { appBasePath, appURL } from '@saker/web-shared/base-path'
import { AssetCard, AssetListItem, formatBytes } from '../components/AssetCard'

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
  const [view, setView] = useState<'cards' | 'compact' | 'table'>('cards')
  const [sort, setSort] = useState<{ key: 'filename' | 'purpose' | 'status' | 'type' | 'size' | 'created' | 'source'; direction: 'asc' | 'desc' }>({ key: 'created', direction: 'desc' })
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [advancedOpen, setAdvancedOpen] = useState(false)
  const [bulkBusy, setBulkBusy] = useState(false)
  const [notice, setNotice] = useState('')
  const hasFilters = [filename, purpose, tags, status, source, contentType, metaKey, metaValue].some(Boolean)

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
  const filter = useMemo(() => ({
    ...debouncedFilter,
    limit: 24,
  }), [debouncedFilter])
  const sortedAssets = useMemo(() => {
    const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' })
    return [...assets].sort((left, right) => {
      const direction = sort.direction === 'asc' ? 1 : -1
      const leftValue = sortValue(left, sort.key)
      const rightValue = sortValue(right, sort.key)
      if (typeof leftValue === 'number' && typeof rightValue === 'number') return (leftValue - rightValue) * direction
      return collator.compare(String(leftValue), String(rightValue)) * direction
    })
  }, [assets, sort])
  const activeIndex = active ? sortedAssets.findIndex((asset) => asset.id === active.id) : -1
  const uploadPath = appURL(appBasePath(import.meta.env.BASE_URL), '/upload')
  const compareTarget = `/compare?ids=${encodeURIComponent([...selected].join(','))}`
  const activeFilters = useMemo(() => [
    filename ? { key: 'filename', label: `${t('filename')}: ${filename}`, clear: () => setFilename('') } : null,
    tags ? { key: 'tags', label: `${t('tags')}: ${tags}`, clear: () => setTags('') } : null,
    metaKey ? { key: 'metaKey', label: `${t('metadataKey')}: ${metaKey}`, clear: () => { setMetaKey(''); setMetaValue('') } } : null,
    metaValue && metaKey ? { key: 'metaValue', label: `${t('metadataValue')}: ${metaValue}`, clear: () => setMetaValue('') } : null,
    purpose ? { key: 'purpose', label: `${t('purpose')}: ${purpose}`, clear: () => setPurpose('') } : null,
    status ? { key: 'status', label: `${t('status')}: ${status}`, clear: () => setStatus('') } : null,
    source ? { key: 'source', label: `${t('source')}: ${source}`, clear: () => setSource('') } : null,
    contentType ? { key: 'contentType', label: `${t('type')}: ${contentType}`, clear: () => setContentType('') } : null,
  ].filter(Boolean) as Array<{ key: string; label: string; clear: () => void }>, [contentType, filename, metaKey, metaValue, purpose, source, status, tags, t])

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

  async function loadMore() {
    if (!hasMore || !nextCursor || loading) return
    setLoading(true)
    setError('')
    try {
      const result = await listAssets({ ...debouncedFilter, cursor: nextCursor, limit: 24 })
      setAssets((prev) => mergeAssets(prev, result.data))
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
    setNextCursor('')
  }

  async function deleteSelected() {
    if (!window.confirm(t('confirmDeleteSelected'))) return
    setBulkBusy(true)
    setNotice('')
    setError('')
    try {
      const result = await bulkDelete([...selected])
      const failed = result.data.filter((item) => item.error)
      if (failed.length > 0) throw new Error(t('bulkDeletePartial', { count: failed.length }))
      setSelected(new Set())
      setNotice(t('deleteComplete'))
      await refresh()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBulkBusy(false)
    }
  }

  async function applyTags() {
    const nextTags = batchTags.split(',').map((tag) => tag.trim()).filter(Boolean)
    setBulkBusy(true)
    setNotice('')
    setError('')
    try {
      await Promise.all([...selected].map((id) => updateAsset(id, { tags: nextTags })))
      setSelected(new Set())
      setBatchTags('')
      setNotice(t('tagsApplied'))
      await refresh()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBulkBusy(false)
    }
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
      <header className="page-header assets-header">
        <div className="page-title">
          <div>
            <h1>{t('assets')}</h1>
            <p>{t('assetsSubtitle')}</p>
          </div>
          <div className="asset-count" aria-live="polite">
            <strong>{assets.length}</strong>
            <span>{loading ? t('loading') : t('shown')}</span>
          </div>
        </div>
        <div className="assets-toolbar">
          <div className="segmented">
            <button type="button" className={view === 'cards' ? 'active' : ''} onClick={() => setView('cards')}>{t('cardView')}</button>
            <button type="button" className={view === 'compact' ? 'active' : ''} onClick={() => setView('compact')}>{t('compactView')}</button>
            <button type="button" className={view === 'table' ? 'active' : ''} onClick={() => setView('table')}>{t('tableView')}</button>
          </div>
          {selected.size > 0 ? (
            <div className="selection-bar" role="region" aria-label={t('selectionActions')}>
              <span className="selection-count">{t('selectedCount', { count: selected.size })}</span>
              <Link className={`button-link${selected.size < 2 ? ' disabled' : ''}`} to={compareTarget} aria-disabled={selected.size < 2} onClick={(event) => {
                if (selected.size < 2) event.preventDefault()
              }}>{t('compareSelected')}</Link>
              <label className="field compact-field"><span>{t('batchTags')}</span><input value={batchTags} disabled={bulkBusy} onChange={(event) => setBatchTags(event.target.value)} /></label>
              <button type="button" disabled={bulkBusy} onClick={applyTags}>{bulkBusy ? t('working') : t('applyTags')}</button>
              <button type="button" disabled={bulkBusy} className="danger-button" onClick={deleteSelected}>{bulkBusy ? t('working') : t('deleteSelected')}</button>
            </div>
          ) : null}
        </div>
      </header>
      <section className="filters compact-filters">
        <div className="filter-main">
          <label className="field search-field"><span>{t('filename')}</span><input value={filename} onChange={(event) => { resetPaging(); setFilename(event.target.value) }} /></label>
          <label className="field"><span>{t('tags')}</span><input value={tags} onChange={(event) => { resetPaging(); setTags(event.target.value) }} /></label>
          <label className="field"><span>{t('metadataKey')}</span><input value={metaKey} onChange={(event) => { resetPaging(); setMetaKey(event.target.value) }} /></label>
          <label className="field"><span>{t('metadataValue')}</span><input value={metaValue} disabled={!metaKey} onChange={(event) => { resetPaging(); setMetaValue(event.target.value) }} /></label>
          <div className="filter-actions">
            <button type="button" className="secondary-button" onClick={() => setAdvancedOpen((value) => !value)} aria-expanded={advancedOpen}>{advancedOpen ? t('hideFilters') : t('moreFilters')}</button>
            <button type="button" className="ghost-button" disabled={!hasFilters} onClick={clearFilters}>{t('clear')}</button>
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
        {activeFilters.length > 0 ? (
          <div className="filter-chips" aria-label={t('activeFilters')}>
            {activeFilters.map((item) => (
              <button key={item.key} type="button" onClick={() => { resetPaging(); item.clear() }}>{item.label}<span aria-hidden="true">x</span></button>
            ))}
          </div>
        ) : null}
      </section>
      {error ? <p className="form-error" role="alert">{error}</p> : null}
      {notice ? <p className="success" role="status">{notice}</p> : null}
      {loading ? <div className="loading-strip" role="status">{t('loading')}</div> : null}
      {!loading && assets.length === 0 ? (
        <div className="empty-state">
          <strong>{hasFilters ? t('noAssetsWithFilters') : t('noAssetsYet')}</strong>
          <p>{hasFilters ? t('noAssetsFilterHint') : t('noAssetsUploadHint')}</p>
          {hasFilters ? <button type="button" onClick={clearFilters}>{t('clearFilters')}</button> : <a className="button-link" href={uploadPath}>{t('upload')}</a>}
        </div>
      ) : null}
      {view === 'cards' ? (
        <div className="asset-grid">{sortedAssets.map((asset) => (
            <AssetCard
              key={asset.id}
              asset={asset}
              selected={selected.has(asset.id)}
              onSelect={(checked) => setSelected((prev) => selectAsset(prev, asset.id, checked))}
              onOpen={() => setActive(asset)}
            />
        ))}</div>
      ) : view === 'compact' ? (
        <div className="asset-list">{sortedAssets.map((asset) => (
          <AssetListItem
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
              <SortableHeader label={t('filename')} sortKey="filename" sort={sort} onSort={setSort} />
              <SortableHeader label={t('purpose')} sortKey="purpose" sort={sort} onSort={setSort} />
              <SortableHeader label={t('status')} sortKey="status" sort={sort} onSort={setSort} />
              <SortableHeader label={t('type')} sortKey="type" sort={sort} onSort={setSort} />
              <SortableHeader label={t('size')} sortKey="size" sort={sort} onSort={setSort} />
              <SortableHeader label={t('created')} sortKey="created" sort={sort} onSort={setSort} />
              <SortableHeader label={t('source')} sortKey="source" sort={sort} onSort={setSort} />
              <th>{t('tags')}</th>
            </tr>
          </thead>
          <tbody>
            {sortedAssets.map((asset) => (
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
                <td data-label={t('size')}>{formatBytes(asset.bytes)}</td>
                <td data-label={t('created')}>{formatTimestamp(asset.created_at)}</td>
                <td data-label={t('source')}>{asset.source || '-'}</td>
                <td data-label={t('tags')}><div className="tags compact-tags">{(asset.tags || []).slice(0, 3).map((tag) => <span key={tag}>{tag}</span>)}</div></td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <div className="pagination-bar">
        <span>{hasMore ? t('moreAvailable') : t('endOfResults')}</span>
        <button type="button" disabled={!hasMore || !nextCursor || loading} onClick={loadMore}>{loading ? t('loading') : t('loadMore')}</button>
      </div>
      {active ? (
        <Suspense fallback={<p className="muted">{t('loading')}</p>}>
          <AssetDetail
            assetID={active.id}
            positionLabel={activeIndex >= 0 ? t('assetPosition', { current: activeIndex + 1, total: sortedAssets.length }) : ''}
            canNavigatePrev={activeIndex > 0}
            canNavigateNext={activeIndex >= 0 && activeIndex < sortedAssets.length - 1}
            onClose={() => setActive(null)}
            onNavigate={(direction) => {
              const index = sortedAssets.findIndex((asset) => asset.id === active.id)
              const next = sortedAssets[index + direction]
              if (next) setActive(next)
            }}
          />
        </Suspense>
      ) : null}
    </div>
  )
}

function SortableHeader({ label, sortKey, sort, onSort }: { label: string; sortKey: 'filename' | 'purpose' | 'status' | 'type' | 'size' | 'created' | 'source'; sort: { key: 'filename' | 'purpose' | 'status' | 'type' | 'size' | 'created' | 'source'; direction: 'asc' | 'desc' }; onSort: (sort: { key: 'filename' | 'purpose' | 'status' | 'type' | 'size' | 'created' | 'source'; direction: 'asc' | 'desc' }) => void }) {
  const active = sort.key === sortKey
  return (
    <th aria-sort={active ? (sort.direction === 'asc' ? 'ascending' : 'descending') : 'none'}>
      <button type="button" className="table-sort" onClick={() => onSort({ key: sortKey, direction: active && sort.direction === 'asc' ? 'desc' : 'asc' })}>
        {label}
        <span aria-hidden="true">{active ? (sort.direction === 'asc' ? 'ASC' : 'DESC') : ''}</span>
      </button>
    </th>
  )
}

function sortValue(asset: Asset, key: 'filename' | 'purpose' | 'status' | 'type' | 'size' | 'created' | 'source') {
  if (key === 'filename') return asset.filename
  if (key === 'purpose') return asset.purpose
  if (key === 'status') return asset.status
  if (key === 'type') return asset.content_type || ''
  if (key === 'size') return asset.bytes
  if (key === 'created') return asset.created_at
  return asset.source || ''
}

function formatTimestamp(value: number) {
  if (!value) return '-'
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value * 1000))
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

function mergeAssets(prev: Asset[], next: Asset[]) {
  const seen = new Set(prev.map((asset) => asset.id))
  return [...prev, ...next.filter((asset) => !seen.has(asset.id))]
}
