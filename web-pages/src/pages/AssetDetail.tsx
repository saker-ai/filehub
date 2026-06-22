import { createElement, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { assetContentURL, deleteAsset, fetchAssetContentBlob, fetchAssetText, getAsset, presignAsset, updateAsset, type Asset } from '@saker/assethub-client'
import { CodeBlock } from '../components/CodeBlock'
import { formatBytes } from '../components/AssetCard'
import { AuthAudio, AuthFrame, AuthImage, AuthVideo, useAuthObjectURL } from '../components/AuthMedia'
import { AIReviewResults } from '../components/AIReviews'

export function AssetDetail({ assetID, positionLabel, canNavigatePrev = true, canNavigateNext = true, onClose, onNavigate }: { assetID: string; positionLabel?: string; canNavigatePrev?: boolean; canNavigateNext?: boolean; onClose: () => void; onNavigate?: (direction: -1 | 1) => void }) {
  const { t } = useTranslation()
  const [asset, setAsset] = useState<Asset | null>(null)
  const [tags, setTags] = useState('')
  const [metadata, setMetadata] = useState('')
  const [signedURL, setSignedURL] = useState('')
  const [downloadError, setDownloadError] = useState('')
  const [formError, setFormError] = useState('')

  useEffect(() => {
    void getAsset(assetID).then((value) => {
      setAsset(value)
      setTags((value.tags || []).join(', '))
      setMetadata(JSON.stringify(value.metadata || {}, null, 2))
    })
  }, [assetID])

  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [onClose])

  if (!asset) return null

  async function save() {
    setFormError('')
    try {
      const updated = await updateAsset(assetID, {
        tags: tags.split(',').map((tag) => tag.trim()).filter(Boolean),
        metadata: JSON.parse(metadata || '{}'),
      })
      setAsset(updated)
      setTags((updated.tags || []).join(', '))
      setMetadata(JSON.stringify(updated.metadata || {}, null, 2))
    } catch (err) {
      setFormError(err instanceof Error ? err.message : String(err))
    }
  }

  async function remove() {
    if (!asset || !window.confirm(t('confirmDeleteAsset'))) return
    await deleteAsset(assetID)
    onClose()
  }

  async function presign() {
    const result = await presignAsset(assetID)
    setSignedURL(result.url)
  }

  async function download() {
    setDownloadError('')
    try {
      const blob = await fetchAssetContentBlob(assetID, { download: true })
      const url = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = url
      link.download = asset?.filename || assetID
      document.body.appendChild(link)
      link.click()
      link.remove()
      URL.revokeObjectURL(url)
    } catch (err) {
      setDownloadError(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <div className="overlay" role="dialog" aria-modal="true" onMouseDown={(event) => {
      if (event.target === event.currentTarget) onClose()
    }}>
      <article className="detail">
        <header className="detail-header">
          <div className="detail-title">
            <span className={`status ${asset.status}`}>{asset.status}</span>
            <h2>{asset.filename}</h2>
            <p>{asset.id} · {formatBytes(asset.bytes)}</p>
            {positionLabel ? <span className="detail-position">{positionLabel}</span> : null}
          </div>
          <div className="row actions">
            {onNavigate ? <button type="button" disabled={!canNavigatePrev} onClick={() => onNavigate(-1)}>{t('previous')}</button> : null}
            {onNavigate ? <button type="button" disabled={!canNavigateNext} onClick={() => onNavigate(1)}>{t('next')}</button> : null}
            <button type="button" onClick={onClose} aria-label={t('close')}>{t('close')}</button>
          </div>
        </header>

        <section className="detail-hero">
          <div className="preview detail-preview">
            <Preview asset={asset} />
          </div>
          <aside className="detail-side">
            <dl className="detail-summary">
              <div><dt>{t('type')}</dt><dd>{asset.content_type || 'unknown'}</dd></div>
              <div><dt>{t('checksum')}</dt><dd>{asset.checksum || '-'}</dd></div>
              <div><dt>{t('created')}</dt><dd>{formatTimestamp(asset.created_at)}</dd></div>
              <div><dt>{t('source')}</dt><dd>{asset.source || '-'}</dd></div>
            </dl>
            <div className="detail-actions">
              <button type="button" onClick={download}>{t('download')}</button>
              <button type="button" onClick={presign}>{t('presign')}</button>
            </div>
            {downloadError ? <p className="form-error" role="alert">{downloadError}</p> : null}
            {signedURL ? (
              <div className="signed-url">
                <pre className="code">{signedURL}</pre>
                <button type="button" onClick={() => void navigator.clipboard.writeText(signedURL)}>{t('copy')}</button>
              </div>
            ) : null}
          </aside>
        </section>

        {asset.source === 'ai-generated' ? <AIInfo metadata={asset.metadata || {}} title={t('aiMetadata')} /> : null}

        <AIReviewResults assetID={asset.id} editable />

        <section className="detail-editor">
          <label>{t('tags')}<input value={tags} onChange={(event) => setTags(event.target.value)} /></label>
          <label>{t('metadata')}<textarea value={metadata} onChange={(event) => setMetadata(event.target.value)} /></label>
          {formError ? <p className="form-error" role="alert">{formError}</p> : null}
          <div className="detail-editor-actions">
            <button type="button" onClick={save}>{t('save')}</button>
            <button type="button" className="danger" onClick={remove}>{t('delete')}</button>
          </div>
        </section>

        <section className="raw-object">
          <h3>{t('rawObject')}</h3>
          <CodeBlock value={asset} />
        </section>
      </article>
    </div>
  )
}

function Preview({ asset }: { asset: Asset }) {
  const { t } = useTranslation()
  const type = asset.content_type || ''
  const src = assetContentURL(asset.id)
  if (type.startsWith('image/')) return <AuthImage src={src} alt={asset.filename} />
  if (type.startsWith('video/')) return <AuthVideo src={src} />
  if (type.startsWith('audio/')) return <AudioPreview src={src} />
  if (type === 'application/pdf') return <AuthFrame src={src} title={asset.filename} />
  if (type.startsWith('model/') || /\.(glb|gltf)$/i.test(asset.filename)) return <ModelPreview src={src} />
  if (type.startsWith('text/') || type.includes('json') || type.includes('xml') || /\.md$/i.test(asset.filename)) return <TextPreview assetID={asset.id} filename={asset.filename} />
  return <div className="file-preview">{t('file')}</div>
}

function AudioPreview({ src }: { src: string }) {
  const containerRef = useRef<HTMLDivElement | null>(null)
  const [ready, setReady] = useState(false)
  const { url, error } = useAuthObjectURL(src)

  useEffect(() => {
    if (!containerRef.current || !url) return
    setReady(false)
    let destroyed = false
    let wave: { destroy: () => void } | null = null
    void import('wavesurfer.js').then(({ default: WaveSurfer }) => {
      if (destroyed || !containerRef.current) return
      const created = WaveSurfer.create({
        container: containerRef.current,
        waveColor: '#8aa4bf',
        progressColor: '#1f7a8c',
        height: 96,
        url,
      })
      wave = created
      created.on('ready', () => setReady(true))
    })
    return () => {
      destroyed = true
      wave?.destroy()
    }
  }, [url])

  return (
    <div className="audio-preview">
      <div ref={containerRef} />
      {error ? <span className="preview-error">{error}</span> : <AuthAudio src={src} />}
      {!ready && !error ? <span className="preview-loading">Loading</span> : null}
    </div>
  )
}

function ModelPreview({ src }: { src: string }) {
  const { url, error } = useAuthObjectURL(src)
  const [ready, setReady] = useState(false)
  useEffect(() => {
    let cancelled = false
    void import('@google/model-viewer').then(() => {
      if (!cancelled) setReady(true)
    })
    return () => {
      cancelled = true
    }
  }, [])
  if (error) return <span className="preview-error">{error}</span>
  if (!url || !ready) return <span className="preview-loading">Loading</span>
  return createElement('model-viewer', { src: url, 'camera-controls': true, ar: true, 'auto-rotate': true })
}

function TextPreview({ assetID, filename }: { assetID: string; filename: string }) {
  const [text, setText] = useState('')
  const [highlighted, setHighlighted] = useState('')
  useEffect(() => {
    void fetchAssetText(assetID).then(setText)
  }, [assetID])
  const [html, setHTML] = useState('')

  useEffect(() => {
    if (!text || !/\.md$/i.test(filename)) {
      setHTML('')
      return
    }
    let cancelled = false
    void Promise.all([import('marked'), import('dompurify')]).then(([{ Marked }, { default: DOMPurify }]) => {
      if (cancelled) return
      const marked = new Marked()
      setHTML(DOMPurify.sanitize(marked.parse(text, { async: false }) as string))
    })
    return () => {
      cancelled = true
    }
  }, [filename, text])

  useEffect(() => {
    if (!text || /\.md$/i.test(filename)) {
      setHighlighted('')
      return
    }
    const lang = languageFor(filename)
    if (!lang) {
      setHighlighted('')
      return
    }
    let cancelled = false
    void Promise.all([highlightCode(text, lang), import('dompurify')]).then(([html, { default: DOMPurify }]) => {
      if (cancelled) return
      setHighlighted(DOMPurify.sanitize(html))
    })
    return () => {
      cancelled = true
    }
  }, [filename, text])

  if (/\.md$/i.test(filename)) {
    return <article className="markdown-preview" dangerouslySetInnerHTML={{ __html: html }} />
  }
  if (highlighted) {
    return <article className="text-preview" dangerouslySetInnerHTML={{ __html: highlighted }} />
  }
  return <pre className="code text-preview">{text}</pre>
}

function AIInfo({ metadata, title }: { metadata: Record<string, unknown>; title: string }) {
  const { t } = useTranslation()
  const known = ['model', 'prompt', 'negative_prompt', 'seed', 'steps', 'cfg_scale', 'guidance_scale', 'sampler', 'size', 'width', 'height', 'engine', 'workflow_id', 'duration', 'provider']
  const knownSet = new Set(known)
  const unknown = Object.keys(metadata).filter((key) => !knownSet.has(key)).sort()
  return (
    <section className="ai-info">
      <h3>{title}</h3>
      <dl>
        {known.filter((key) => metadata[key] !== undefined).map((key) => (
          <div key={key}>
            <dt>{key}</dt>
            <dd>
              {String(metadata[key])}
              {['prompt', 'negative_prompt', 'seed'].includes(key) ? <button type="button" onClick={() => void navigator.clipboard.writeText(String(metadata[key]))}>{t('copy')}</button> : null}
            </dd>
          </div>
        ))}
        {unknown.map((key) => (
          <div key={key}><dt>{key}</dt><dd>{String(metadata[key])}</dd></div>
        ))}
      </dl>
    </section>
  )
}

function languageFor(filename: string) {
  if (/\.jsonl?$/i.test(filename)) return 'json'
  if (/\.ya?ml$/i.test(filename)) return 'yaml'
  if (/\.xml$/i.test(filename)) return 'xml'
  if (/\.tsx?$/i.test(filename)) return 'tsx'
  if (/\.jsx?$/i.test(filename)) return 'jsx'
  if (/\.go$/i.test(filename)) return 'go'
  if (/\.py$/i.test(filename)) return 'python'
  if (/\.sh$/i.test(filename)) return 'bash'
  return ''
}

function formatTimestamp(value: number) {
  if (!value) return '-'
  return new Date(value * 1000).toLocaleString()
}

let highlighterPromise: Promise<{ codeToHtml: (code: string, options: { lang: string; theme: string }) => string }> | null = null

async function highlightCode(code: string, lang: string) {
  const highlighter = await loadHighlighter()
  return highlighter.codeToHtml(code, { lang, theme: 'github-dark' })
}

async function loadHighlighter() {
  highlighterPromise ||= Promise.all([
    import('shiki/core'),
    import('@shikijs/engine-javascript'),
    import('shiki/themes/github-dark.mjs'),
    import('shiki/langs/json.mjs'),
    import('shiki/langs/yaml.mjs'),
    import('shiki/langs/xml.mjs'),
    import('shiki/langs/tsx.mjs'),
    import('shiki/langs/jsx.mjs'),
    import('shiki/langs/go.mjs'),
    import('shiki/langs/python.mjs'),
    import('shiki/langs/bash.mjs'),
  ]).then(([{ createHighlighterCore }, { createJavaScriptRegexEngine }, theme, json, yaml, xml, tsx, jsx, go, python, bash]) => createHighlighterCore({
    themes: [theme.default],
    langs: [json.default, yaml.default, xml.default, tsx.default, jsx.default, go.default, python.default, bash.default],
    engine: createJavaScriptRegexEngine(),
  }))
  return highlighterPromise
}
