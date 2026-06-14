import { useState } from 'react'
import type { DragEvent, FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { uploadAsset, type Asset } from '../api/client'
import { CodeBlock } from '../components/CodeBlock'

type UploadItem = {
  name: string
  progress: number
  status: 'queued' | 'uploading' | 'done' | 'error'
  asset?: Asset
  error?: string
}

export default function Upload() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [purpose, setPurpose] = useState('media')
  const [tags, setTags] = useState('')
  const [metadata, setMetadata] = useState('{}')
  const [files, setFiles] = useState<File[]>([])
  const [items, setItems] = useState<UploadItem[]>([])
  const [error, setError] = useState('')
  const uploading = items.some((item) => item.status === 'queued' || item.status === 'uploading')

  function addFiles(next: FileList | File[]) {
    setError('')
    setFiles((prev) => [...prev, ...Array.from(next)])
  }

  function drop(event: DragEvent<HTMLLabelElement>) {
    event.preventDefault()
    addFiles(event.dataTransfer.files)
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setError('')
    if (!files.length) return
    try {
      JSON.parse(metadata || '{}')
    } catch {
      setError(t('metadataJsonError'))
      return
    }
    setItems(files.map((file) => ({ name: file.name, progress: 0, status: 'queued' })))
    const uploaded: Asset[] = []
    for (const [index, file] of files.entries()) {
      setItems((prev) => updateItem(prev, index, { status: 'uploading' }))
      try {
        const asset = await uploadAsset(file, { purpose, tags, metadata, source: 'upload' }, (progress) => {
          setItems((prev) => updateItem(prev, index, { progress }))
        })
        uploaded.push(asset)
        setItems((prev) => updateItem(prev, index, { status: 'done', progress: 100, asset }))
      } catch (err) {
        setItems((prev) => updateItem(prev, index, { status: 'error', error: err instanceof Error ? err.message : String(err) }))
      }
    }
    if (uploaded.length === files.length) {
      setTimeout(() => navigate('/assets'), 600)
    }
  }

  return (
    <div className="page">
      <header className="page-header">
        <h1>{t('upload')}</h1>
        <p>{t('uploadSubtitle')}</p>
      </header>
      <form className="upload-form" onSubmit={submit}>
        <label className="drop-zone" onDragOver={(event) => event.preventDefault()} onDrop={drop}>
          <input name="file" type="file" multiple onChange={(event) => event.currentTarget.files && addFiles(event.currentTarget.files)} />
          <span>{files.length ? files.map((file) => file.name).join(', ') : t('dropFiles')}</span>
        </label>
        <label className="field"><span>{t('purpose')}</span><select value={purpose} onChange={(event) => setPurpose(event.target.value)}>
          {['media', 'assistants', 'batch', 'fine-tune', 'vector-store', 'general'].map((value) => <option key={value}>{value}</option>)}
        </select></label>
        <label className="field"><span>{t('tags')}</span><input value={tags} onChange={(event) => setTags(event.target.value)} /></label>
        <label className="field"><span>{t('metadata')}</span><textarea value={metadata} onChange={(event) => setMetadata(event.target.value)} /></label>
        <button type="submit" disabled={!files.length || uploading}>{uploading ? t('uploading') : t('upload')}</button>
      </form>
      {error ? <p className="error" role="alert">{error}</p> : null}
      {items.length ? (
        <div className="panel upload-results">
          {items.map((item) => (
            <div key={item.name} className="upload-row">
              <span>{item.name}</span>
              <progress max={100} value={item.progress} />
              <span>{item.status}</span>
              {item.error ? <span className="error">{item.error}</span> : null}
            </div>
          ))}
        </div>
      ) : null}
      {items.filter((item) => item.asset).map((item) => <CodeBlock key={item.asset!.id} value={item.asset} />)}
    </div>
  )
}

function updateItem(items: UploadItem[], index: number, patch: Partial<UploadItem>) {
  return items.map((item, itemIndex) => itemIndex === index ? { ...item, ...patch } : item)
}
