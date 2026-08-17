import { useEffect, useRef, useState } from 'react'
import { assetPreviewURL, fetchAssetContentBlob } from '@saker/filehub-client'
import { useAuthObjectURL } from './AuthMedia'

// Largest document we are willing to render in-browser. Beyond this the
// preview falls back to a plain "download" hint so a huge upload cannot
// freeze the tab.
const MAX_PREVIEW_BYTES = 64 * 1024 * 1024

export type OfficeKind = 'docx' | 'sheet' | 'csv'

// officeKindFor returns the office/document preview family for a file, or
// null when the type is not handled by the client-side renderers. Detection
// is filename-first because Office Open XML files are zip containers whose
// sniffed content-type is usually just application/zip.
export function officeKindFor(filename: string, contentType: string): OfficeKind | null {
  const name = (filename || '').toLowerCase()
  const type = (contentType || '').toLowerCase()
  if (
    /\.docx$/.test(name) ||
    type === 'application/vnd.openxmlformats-officedocument.wordprocessingml.document'
  ) {
    return 'docx'
  }
  if (
    /\.(xlsx|xlsm|xlsb|ods)$/.test(name) ||
    type === 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet' ||
    type === 'application/vnd.oasis.opendocument.spreadsheet'
  ) {
    return 'sheet'
  }
  if (/\.(csv|tsv)$/.test(name)) {
    return 'csv'
  }
  if (type === 'text/csv' || type === 'text/tab-separated-values') {
    return 'csv'
  }
  return null
}

// serverOfficeKindFor reports whether a document is an office format that the
// browser cannot render client-side and that FileHub converts to PDF
// server-side via LibreOffice (when installed): presentations and legacy
// binary Office formats.
export function serverOfficeKindFor(filename: string, contentType: string): boolean {
  const name = (filename || '').toLowerCase()
  const type = (contentType || '').toLowerCase()
  if (/\.(pptx|ppt|doc|xls|odp)$/.test(name)) return true
  return (
    type === 'application/vnd.ms-powerpoint' ||
    type === 'application/vnd.openxmlformats-officedocument.presentationml.presentation' ||
    type === 'application/msword' ||
    type === 'application/vnd.ms-excel'
  )
}

async function fetchArrayBuffer(assetID: string, bytes: number): Promise<ArrayBuffer | null> {
  if (bytes > MAX_PREVIEW_BYTES) {
    return null
  }
  const blob = await fetchAssetContentBlob(assetID)
  return blob.arrayBuffer()
}

// DocxPreview renders a Word .docx document to HTML in the browser using
// docx-preview (lazy-loaded). No server-side conversion is involved.
export function DocxPreview({ assetID, bytes }: { assetID: string; bytes: number }) {
  const containerRef = useRef<HTMLDivElement | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let cancelled = false
    setError('')
    setLoading(true)
    void (async () => {
      try {
        const buffer = await fetchArrayBuffer(assetID, bytes)
        if (cancelled) return
        if (!buffer) {
          setError('Document too large to preview')
          setLoading(false)
          return
        }
        const { renderAsync } = await import('docx-preview')
        if (cancelled || !containerRef.current) return
        containerRef.current.innerHTML = ''
        await renderAsync(buffer, containerRef.current, containerRef.current, {
          className: 'docx-preview',
          inWrapper: true,
          ignoreWidth: false,
          ignoreHeight: false,
        })
        if (!cancelled) setLoading(false)
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : String(err))
          setLoading(false)
        }
      }
    })()
    return () => {
      cancelled = true
    }
  }, [assetID, bytes])

  if (error) return <span className="preview-error">{error}</span>
  return (
    <div className="docx-preview-wrap">
      <div ref={containerRef} className="docx-preview-container" />
      {loading ? <span className="preview-loading">Loading</span> : null}
    </div>
  )
}

// SheetPreview renders a spreadsheet (xlsx/xlsm/ods) or delimited text
// (csv/tsv) as an HTML table using SheetJS (lazy-loaded). Multiple sheets are
// selectable via tabs; only cell values are shown (formulas render their
// cached results, charts and macros are not supported).
export function SheetPreview({ assetID, bytes, delimiter }: { assetID: string; bytes: number; delimiter?: string }) {
  const [sheets, setSheets] = useState<{ name: string; rows: unknown[][] }[]>([])
  const [active, setActive] = useState(0)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let cancelled = false
    setError('')
    setLoading(true)
    setSheets([])
    setActive(0)
    void (async () => {
      try {
        const buffer = await fetchArrayBuffer(assetID, bytes)
        if (cancelled) return
        if (!buffer) {
          setError('Spreadsheet too large to preview')
          setLoading(false)
          return
        }
        const XLSX = await import('xlsx')
        const workbook = XLSX.read(new Uint8Array(buffer), { type: 'array' })
        const parsed = workbook.SheetNames.map((name) => ({
          name,
          rows: XLSX.utils.sheet_to_json(workbook.Sheets[name], { header: 1, defval: '' }) as unknown[][],
        }))
        if (!cancelled) {
          setSheets(parsed)
          setLoading(false)
        }
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : String(err))
          setLoading(false)
        }
      }
    })()
    return () => {
      cancelled = true
    }
  }, [assetID, bytes, delimiter])

  if (error) return <span className="preview-error">{error}</span>
  if (loading) return <span className="preview-loading">Loading</span>
  const current = sheets[active]
  if (!current) return <span className="preview-error">No sheets</span>
  const maxRows = Math.min(current.rows.length, 1000)

  return (
    <div className="sheet-preview">
      {sheets.length > 1 ? (
        <div className="sheet-tabs" role="tablist">
          {sheets.map((sheet, index) => (
            <button
              key={sheet.name}
              type="button"
              role="tab"
              aria-selected={index === active}
              className={index === active ? 'active' : ''}
              onClick={() => setActive(index)}
            >
              {sheet.name}
            </button>
          ))}
        </div>
      ) : null}
      <div className="sheet-table-wrap">
        <table className="sheet-table">
          <tbody>
            {current.rows.slice(0, maxRows).map((row, rowIndex) => (
              <tr key={rowIndex}>
                {row.map((cell, cellIndex) => (
                  <td key={cellIndex}>{cell === null || cell === undefined ? '' : String(cell)}</td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
        {current.rows.length > maxRows ? (
          <p className="muted">Showing first {maxRows} of {current.rows.length} rows.</p>
        ) : null}
      </div>
    </div>
  )
}

// ServerOfficePreview shows a server-rendered PDF for office formats the
// browser cannot display natively (ppt/pptx/doc/xls/odp). The /preview
// endpoint answers 404 when LibreOffice is not installed or the type is not
// convertible; in that case we fall back to a download hint.
export function ServerOfficePreview({ assetID, filename }: { assetID: string; filename: string }) {
  const { url, error } = useAuthObjectURL(assetPreviewURL(assetID))
  if (error) {
    return (
      <div className="file-preview">
        <span>{filename}: preview not available, download to view.</span>
      </div>
    )
  }
  if (!url) return <span className="preview-loading">Loading</span>
  return <iframe src={url} title={filename} className="preview-frame" />
}
