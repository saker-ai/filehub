import { useEffect, useRef, useState } from 'react'
import { fetchAssetBlob } from '@saker/assethub-client'

type BlobState = {
  url: string
  error: string
}

export function useAuthObjectURL(src: string, enabled = true) {
  const [state, setState] = useState<BlobState>({ url: '', error: '' })

  useEffect(() => {
    if (!enabled) return
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
  }, [enabled, src])

  return state
}

export function AuthImage({ src, alt, className, fallback, lazy = false }: { src: string; alt: string; className?: string; fallback?: string; lazy?: boolean }) {
  const ref = useRef<HTMLSpanElement | null>(null)
  const [visible, setVisible] = useState(!lazy)
  useEffect(() => {
    if (!lazy || visible) return
    const node = ref.current
    if (!node) return
    const observer = new IntersectionObserver(([entry]) => {
      if (entry.isIntersecting) {
        setVisible(true)
        observer.disconnect()
      }
    }, { rootMargin: '240px' })
    observer.observe(node)
    return () => observer.disconnect()
  }, [lazy, visible])
  const { url, error } = useAuthObjectURL(src, visible)
  if (!visible) return <span ref={ref} className="preview-loading">Loading</span>
  if (error) return <span className={fallback ? 'preview-fallback' : 'preview-error'}>{fallback || error}</span>
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
