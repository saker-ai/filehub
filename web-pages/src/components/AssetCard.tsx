import { assetThumbnailURL, type Asset } from '@saker/assethub-client'
import { AuthImage } from './AuthMedia'

export function AssetCard({ asset, selected, onOpen, onSelect }: { asset: Asset; selected?: boolean; onOpen: () => void; onSelect?: (checked: boolean) => void }) {
  return (
    <article className={`asset-card${selected ? ' selected' : ''}`}>
      <header>
        {onSelect ? (
          <label className="checkbox-hit">
            <input type="checkbox" aria-label={`Select ${asset.filename}`} checked={selected} onChange={(event) => onSelect(event.target.checked)} />
          </label>
        ) : null}
        <button type="button" className="asset-title" title={asset.filename} onClick={onOpen}>{asset.filename}</button>
        <span className={`status ${asset.status}`}>{asset.status}</span>
      </header>
      <button type="button" className="asset-preview asset-preview-button" onClick={onOpen} aria-label={`Open ${asset.filename}`}>
        <Preview asset={asset} />
      </button>
      <dl>
        <div><dt>Purpose</dt><dd>{asset.purpose}</dd></div>
        <div><dt>Type</dt><dd>{asset.content_type || 'unknown'}</dd></div>
        <div><dt>Size</dt><dd>{formatBytes(asset.bytes)}</dd></div>
      </dl>
      <div className="tags">{(asset.tags || []).map((tag) => <span key={tag}>{tag}</span>)}</div>
    </article>
  )
}

export function AssetListItem({ asset, selected, onOpen, onSelect }: { asset: Asset; selected?: boolean; onOpen: () => void; onSelect: (checked: boolean) => void }) {
  return (
    <article className={`asset-list-item${selected ? ' selected' : ''}`}>
      <label className="checkbox-hit">
        <input type="checkbox" aria-label={`Select ${asset.filename}`} checked={selected} onChange={(event) => onSelect(event.target.checked)} />
      </label>
      <button type="button" className="asset-title" title={asset.filename} onClick={onOpen}>{asset.filename}</button>
      <span className={`status ${asset.status}`}>{asset.status}</span>
      <span>{asset.purpose}</span>
      <span>{asset.content_type || 'unknown'}</span>
      <span>{formatBytes(asset.bytes)}</span>
      <div className="tags compact-tags">{(asset.tags || []).slice(0, 2).map((tag) => <span key={tag}>{tag}</span>)}</div>
    </article>
  )
}

function Preview({ asset }: { asset: Asset }) {
  const type = asset.content_type || ''
  if (type.startsWith('image/') || type.startsWith('video/')) {
    return <AuthImage src={assetThumbnailURL(asset.id, { width: 320, height: 180, format: 'jpg' })} alt="" className="thumb-image" fallback={asset.content_type?.startsWith('video/') ? 'Video' : 'Image'} lazy />
  }
  if (type.startsWith('audio/')) {
    return <div className="wave-icon" aria-label="Audio">{[16, 34, 22, 44, 28, 38].map((height, index) => <span key={index} style={{ height }} />)}</div>
  }
  if (type.startsWith('model/')) return '3D'
  if (type.includes('pdf')) return 'PDF'
  if (type.startsWith('text/') || type.includes('json')) return 'Text'
  return 'File'
}

export function formatBytes(bytes: number) {
  if (!bytes) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const index = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  return `${(bytes / 1024 ** index).toFixed(index ? 1 : 0)} ${units[index]}`
}
