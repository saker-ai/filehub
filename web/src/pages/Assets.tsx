import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Asset, bulkDelete, listAssets, updateAsset } from '../api/client'
import { AssetCard } from '../components/AssetCard'
import { AssetDetail } from './AssetDetail'

export default function Assets() {
  const { t } = useTranslation()
  const [assets, setAssets] = useState<Asset[]>([])
  const [active, setActive] = useState<Asset | null>(null)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [filename, setFilename] = useState('')
  const [purpose, setPurpose] = useState('')
  const [tags, setTags] = useState('')
  const [status, setStatus] = useState('')
  const [source, setSource] = useState('')
  const [contentType, setContentType] = useState('')
  const [metaKey, setMetaKey] = useState('')
  const [metaValue, setMetaValue] = useState('')
  const [batchTags, setBatchTags] = useState('')
  const [offset, setOffset] = useState(0)
  const [hasMore, setHasMore] = useState(false)
  const [view, setView] = useState<'cards' | 'table'>('cards')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [advancedOpen, setAdvancedOpen] = useState(false)

  const filter = useMemo(() => ({
    filename,
    purpose,
    tags,
    status,
    source,
    content_type: contentType,
    meta_key: metaKey,
    meta_value: metaKey ? metaValue : '',
    limit: 24,
    offset,
  }), [contentType, filename, metaKey, metaValue, offset, purpose, source, status, tags])

  async function refresh() {
    setLoading(true)
    setError('')
    try {
      const result = await listAssets(filter)
      setAssets(result.data)
      setHasMore(result.has_more)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void refresh()
  }, [filter])

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
    setOffset(0)
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
          <label className="field search-field"><span>{t('filename')}</span><input value={filename} onChange={(event) => { setOffset(0); setFilename(event.target.value) }} /></label>
          <label className="field"><span>{t('tags')}</span><input value={tags} onChange={(event) => { setOffset(0); setTags(event.target.value) }} /></label>
          <label className="field"><span>{t('metadataKey')}</span><input value={metaKey} onChange={(event) => { setOffset(0); setMetaKey(event.target.value) }} /></label>
          <label className="field"><span>{t('metadataValue')}</span><input value={metaValue} disabled={!metaKey} onChange={(event) => { setOffset(0); setMetaValue(event.target.value) }} /></label>
          <div className="filter-actions">
            <button type="button" onClick={() => setAdvancedOpen((value) => !value)} aria-expanded={advancedOpen}>{advancedOpen ? t('hideFilters') : t('moreFilters')}</button>
            <button type="button" onClick={clearFilters}>{t('clear')}</button>
          </div>
        </div>
        {advancedOpen ? (
          <div className="filter-advanced">
            <label className="field"><span>{t('purpose')}</span><select value={purpose} onChange={(event) => { setOffset(0); setPurpose(event.target.value) }}>
              <option value="">{t('anyPurpose')}</option>
              {['assistants', 'batch', 'fine-tune', 'media', 'vector-store', 'general'].map((value) => <option key={value}>{value}</option>)}
            </select></label>
            <label className="field"><span>{t('status')}</span><select value={status} onChange={(event) => { setOffset(0); setStatus(event.target.value) }}>
              <option value="">{t('anyStatus')}</option>
              {['uploaded', 'processing', 'ready', 'error'].map((value) => <option key={value}>{value}</option>)}
            </select></label>
            <label className="field"><span>{t('source')}</span><select value={source} onChange={(event) => { setOffset(0); setSource(event.target.value) }}>
              <option value="">{t('anySource')}</option>
              {['upload', 'ai-generated', 'external-url'].map((value) => <option key={value}>{value}</option>)}
            </select></label>
            <label className="field"><span>{t('type')}</span><select value={contentType} onChange={(event) => { setOffset(0); setContentType(event.target.value) }}>
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
        <button type="button" disabled={offset === 0 || loading} onClick={() => setOffset(Math.max(0, offset - 24))}>{t('previous')}</button>
        <button type="button" disabled={!hasMore || loading} onClick={() => setOffset(offset + 24)}>{t('next')}</button>
      </div>
      {active ? (
        <AssetDetail
          assetID={active.id}
          onClose={() => setActive(null)}
          onNavigate={(direction) => {
            const index = assets.findIndex((asset) => asset.id === active.id)
            const next = assets[index + direction]
            if (next) setActive(next)
          }}
        />
      ) : null}
    </div>
  )
}

function selectAsset(prev: Set<string>, id: string, checked: boolean) {
  const next = new Set(prev)
  if (checked) next.add(id)
  else next.delete(id)
  return next
}
