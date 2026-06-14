import { useEffect, useState } from 'react'
import { fetchAssetBlob } from '../api/client'

type BlobState = {
  url: string
  error: string
}

export function useAuthObjectURL(src: string) {
  const [state, setState] = useState<BlobState>({ url: '', error: '' })

  useEffect(() => {
    let alive = true
    let objectURL = ''
    setState({ url: '', error: '' })
    void fetchAssetBlob(src)
      .then((blob) => {
        if (!alive) return
        objectURL = URL.createObjectURL(blob)
        setState({ url: objectURL, error: '' })
      })
      .catch((err) => {
        if (!alive) return
        setState({ url: '', error: err instanceof Error ? err.message : String(err) })
      })
    return () => {
      alive = false
      if (objectURL) URL.revokeObjectURL(objectURL)
    }
  }, [src])

  return state
}

export function AuthImage({ src, alt, className }: { src: string; alt: string; className?: string }) {
  const { url, error } = useAuthObjectURL(src)
  if (error) return <span className="preview-error">{error}</span>
  if (!url) return <span className="preview-loading">Loading</span>
  return <img src={url} alt={alt} className={className} />
}

export function AuthVideo({ src }: { src: string }) {
  const { url, error } = useAuthObjectURL(src)
  if (error) return <span className="preview-error">{error}</span>
  if (!url) return <span className="preview-loading">Loading</span>
  return <video src={url} controls />
}

export function AuthAudio({ src }: { src: string }) {
  const { url, error } = useAuthObjectURL(src)
  if (error) return <span className="preview-error">{error}</span>
  if (!url) return <span className="preview-loading">Loading</span>
  return <audio src={url} controls />
}

export function AuthFrame({ src, title }: { src: string; title: string }) {
  const { url, error } = useAuthObjectURL(src)
  if (error) return <span className="preview-error">{error}</span>
  if (!url) return <span className="preview-loading">Loading</span>
  return <iframe src={url} title={title} />
}
