import { useCallback, useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import {
  Workspace,
  WorkspaceChange,
  WorkspaceEntry,
  WorkspaceRevision,
  WorkspaceShare,
  createWorkspace,
  createWorkspaceShare,
  deleteWorkspace,
  listWorkspaceChanges,
  listWorkspaceHistory,
  listWorkspaceShares,
  listWorkspaceTree,
  listWorkspaces,
  readWorkspaceStats,
  restoreWorkspaceRevision,
  revokeWorkspaceShare,
  workspaceShareURL,
  type ReadStat
} from '@saker/filehub-client'

type Tab = 'files' | 'changes' | 'shares' | 'stats'

function formatTime(unix?: number): string {
  if (!unix) return '-'
  return new Date(unix * 1000).toLocaleString()
}

function formatBytes(bytes?: number): string {
  if (bytes === undefined || bytes === null) return '-'
  if (bytes < 1024) return `${bytes} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB']
  let value = bytes
  let unit = 'B'
  for (const next of units) {
    if (value < 1024) break
    value /= 1024
    unit = next
  }
  return `${value.toFixed(value >= 100 ? 0 : 1)} ${unit}`
}

export default function Workspaces() {
  const [searchParams, setSearchParams] = useSearchParams()
  const [workspaces, setWorkspaces] = useState<Workspace[]>([])
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [loading, setLoading] = useState(false)
  const [creating, setCreating] = useState(false)
  const [newName, setNewName] = useState('')
  const [newDescription, setNewDescription] = useState('')

  const selectedID = searchParams.get('workspace') || ''
  const selected = useMemo(() => workspaces.find((item) => item.id === selectedID) || null, [selectedID, workspaces])
  const [tab, setTab] = useState<Tab>('files')

  const [entries, setEntries] = useState<WorkspaceEntry[]>([])
  const [prefix, setPrefix] = useState('')
  const [treeCursor, setTreeCursor] = useState('')
  const [treeHasMore, setTreeHasMore] = useState(false)

  const [changes, setChanges] = useState<WorkspaceChange[]>([])

  const [shares, setShares] = useState<WorkspaceShare[]>([])
  const [sharePath, setSharePath] = useState('')
  const [lastShareURL, setLastShareURL] = useState('')

  const [stats, setStats] = useState<ReadStat[]>([])

  const [historyPath, setHistoryPath] = useState('')
  const [history, setHistory] = useState<WorkspaceRevision[]>([])
  const [historyError, setHistoryError] = useState('')

  const refreshWorkspaces = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const result = await listWorkspaces({ limit: 100 })
      setWorkspaces(result.data || [])
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void refreshWorkspaces()
  }, [refreshWorkspaces])

  useEffect(() => {
    if (!selectedID) return
    setError('')
    setNotice('')
    if (tab === 'files') {
      listWorkspaceTree(selectedID, { prefix: prefix || undefined, limit: 100 })
        .then((result) => {
          setEntries(result.data || [])
          setTreeHasMore(Boolean(result.has_more))
          setTreeCursor(result.next_cursor || '')
        })
        .catch((err) => setError(err instanceof Error ? err.message : String(err)))
    } else if (tab === 'changes') {
      listWorkspaceChanges(selectedID, { limit: 50 })
        .then((result) => setChanges(result.data || []))
        .catch((err) => setError(err instanceof Error ? err.message : String(err)))
    } else if (tab === 'shares') {
      listWorkspaceShares(selectedID)
        .then((result) => setShares(result.data || []))
        .catch((err) => setError(err instanceof Error ? err.message : String(err)))
    } else if (tab === 'stats') {
      readWorkspaceStats(selectedID, { days: 14 })
        .then((result) => setStats(result.data || []))
        .catch((err) => setError(err instanceof Error ? err.message : String(err)))
    }
  }, [selectedID, tab, prefix])

  useEffect(() => {
    if (!selectedID) {
      setEntries([])
      setChanges([])
      setShares([])
      setStats([])
      setHistory([])
      setHistoryPath('')
      setLastShareURL('')
    }
  }, [selectedID])

  function selectWorkspace(id: string) {
    const next = new URLSearchParams(searchParams)
    if (id) next.set('workspace', id)
    else next.delete('workspace')
    setSearchParams(next, { replace: true })
    setTab('files')
    setHistory([])
    setHistoryPath('')
    setLastShareURL('')
  }

  async function handleCreate() {
    if (!newName.trim()) return
    setCreating(true)
    setError('')
    try {
      const created = await createWorkspace({ name: newName.trim(), description: newDescription.trim() || undefined })
      setNewName('')
      setNewDescription('')
      await refreshWorkspaces()
      selectWorkspace(created.id)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setCreating(false)
    }
  }

  async function handleDelete(workspace: Workspace) {
    setError('')
    try {
      await deleteWorkspace(workspace.id)
      if (selectedID === workspace.id) selectWorkspace('')
      await refreshWorkspaces()
      setNotice(`Workspace "${workspace.name}" deleted (soft delete).`)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  async function loadMoreTree() {
    if (!selectedID || !treeHasMore || !treeCursor) return
    try {
      const result = await listWorkspaceTree(selectedID, { prefix: prefix || undefined, cursor: treeCursor, limit: 100 })
      setEntries((prev) => [...prev, ...(result.data || [])])
      setTreeHasMore(Boolean(result.has_more))
      setTreeCursor(result.next_cursor || '')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  async function openHistory(path: string) {
    if (!selectedID) return
    setHistoryPath(path)
    setHistoryError('')
    setHistory([])
    try {
      const result = await listWorkspaceHistory(selectedID, { path, limit: 50 })
      setHistory(result.data || [])
    } catch (err) {
      setHistoryError(err instanceof Error ? err.message : String(err))
    }
  }

  async function handleRestore(revision: WorkspaceRevision) {
    if (!selectedID || !historyPath) return
    setError('')
    try {
      await restoreWorkspaceRevision(selectedID, { path: historyPath, revision_id: revision.id, note: 'restored from web ui' })
      setNotice(`Restored ${historyPath} to revision ${revision.id}.`)
      await openHistory(historyPath)
      if (tab === 'files') {
        const result = await listWorkspaceTree(selectedID, { prefix: prefix || undefined, limit: 100 })
        setEntries(result.data || [])
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  async function handleCreateShare() {
    if (!selectedID || !sharePath.trim()) return
    setError('')
    setLastShareURL('')
    try {
      const created = await createWorkspaceShare(selectedID, { path: sharePath.trim() })
      if (created.token) {
        setLastShareURL(window.location.origin + workspaceShareURL(created.token))
      }
      const result = await listWorkspaceShares(selectedID)
      setShares(result.data || [])
      setNotice('Share link created. Copy it now — the token is shown only once.')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  async function handleRevoke(share: WorkspaceShare) {
    if (!selectedID) return
    setError('')
    try {
      await revokeWorkspaceShare(selectedID, share.id)
      const result = await listWorkspaceShares(selectedID)
      setShares(result.data || [])
      setNotice('Share link revoked.')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <div className="page">
      <header className="page-header">
        <div>
          <h1 className="page-title">Workspaces</h1>
          <p className="muted">Shared sync workspaces used by Saker agents. Files sync through the FileHub Workspace API.</p>
        </div>
      </header>

      {error && <p className="muted" style={{ color: '#c0392b' }}>{error}</p>}
      {notice && <p className="muted">{notice}</p>}

      <section style={{ display: 'grid', gridTemplateColumns: 'minmax(280px, 1fr) 2fr', gap: '1rem', alignItems: 'start' }}>
        <div>
          <div className="row" style={{ marginBottom: '0.75rem', display: 'flex', gap: '0.5rem' }}>
            <input
              value={newName}
              onChange={(event) => setNewName(event.target.value)}
              placeholder="Workspace name"
              style={{ flex: 1 }}
            />
            <button onClick={() => void handleCreate()} disabled={creating || !newName.trim()}>
              {creating ? 'Creating…' : 'Create'}
            </button>
          </div>
          <input
            value={newDescription}
            onChange={(event) => setNewDescription(event.target.value)}
            placeholder="Description (optional)"
            style={{ width: '100%', marginBottom: '0.75rem' }}
          />
          <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid', gap: '0.5rem' }}>
            {workspaces.map((workspace) => (
              <li key={workspace.id} style={{ border: selectedID === workspace.id ? '1px solid currentColor' : '1px solid rgba(128,128,128,0.3)', borderRadius: 8, padding: '0.5rem 0.75rem' }}>
                <button
                  onClick={() => selectWorkspace(workspace.id)}
                  style={{ background: 'none', border: 'none', padding: 0, cursor: 'pointer', textAlign: 'left', width: '100%' }}
                >
                  <strong>{workspace.name}</strong>
                  <div className="muted" style={{ fontSize: '0.85rem' }}>
                    seq {workspace.sequence} · updated {formatTime(workspace.updated_at)}
                  </div>
                  {workspace.description && <div className="muted" style={{ fontSize: '0.85rem' }}>{workspace.description}</div>}
                </button>
                <button
                  onClick={() => void handleDelete(workspace)}
                  style={{ background: 'none', border: 'none', color: '#c0392b', cursor: 'pointer', fontSize: '0.8rem', padding: 0 }}
                >
                  Delete
                </button>
              </li>
            ))}
            {workspaces.length === 0 && !loading && <li className="muted">No workspaces yet.</li>}
          </ul>
        </div>

        <div>
          {!selected ? (
            <p className="muted">Select or create a workspace to browse its tree, history, shares, and read stats.</p>
          ) : (
            <>
              <nav style={{ display: 'flex', gap: '0.5rem', marginBottom: '1rem' }}>
                {(['files', 'changes', 'shares', 'stats'] as Tab[]).map((item) => (
                  <button
                    key={item}
                    onClick={() => setTab(item)}
                    style={{ fontWeight: tab === item ? 700 : 400 }}
                  >
                    {item}
                  </button>
                ))}
              </nav>

              {tab === 'files' && (
                <div>
                  <div className="row" style={{ display: 'flex', gap: '0.5rem', marginBottom: '0.75rem' }}>
                    <input
                      value={prefix}
                      onChange={(event) => setPrefix(event.target.value)}
                      placeholder="Filter by path prefix"
                      style={{ flex: 1 }}
                    />
                  </div>
                  <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.9rem' }}>
                    <thead>
                      <tr>
                        <th style={{ textAlign: 'left' }}>Path</th>
                        <th style={{ textAlign: 'right' }}>Size</th>
                        <th style={{ textAlign: 'left' }}>Updated</th>
                        <th />
                      </tr>
                    </thead>
                    <tbody>
                      {entries.map((entry) => (
                        <tr key={entry.path}>
                          <td style={{ fontFamily: 'monospace' }}>
                            {entry.path}
                            {entry.path.includes('.saker-conflict-') && <span className="muted"> (conflict)</span>}
                          </td>
                          <td style={{ textAlign: 'right' }}>{formatBytes(entry.revision.bytes)}</td>
                          <td>{formatTime(entry.revision.created_at)}</td>
                          <td>
                            <button onClick={() => void openHistory(entry.path)} style={{ fontSize: '0.8rem' }}>
                              History
                            </button>
                          </td>
                        </tr>
                      ))}
                      {entries.length === 0 && (
                        <tr>
                          <td colSpan={4} className="muted">No files in this workspace yet.</td>
                        </tr>
                      )}
                    </tbody>
                  </table>
                  {treeHasMore && (
                    <button onClick={() => void loadMoreTree()} style={{ marginTop: '0.5rem' }}>
                      Load more
                    </button>
                  )}
                </div>
              )}

              {tab === 'changes' && (
                <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.9rem' }}>
                  <thead>
                    <tr>
                      <th style={{ textAlign: 'right' }}>Seq</th>
                      <th style={{ textAlign: 'left' }}>Kind</th>
                      <th style={{ textAlign: 'left' }}>Path</th>
                      <th style={{ textAlign: 'left' }}>When</th>
                    </tr>
                  </thead>
                  <tbody>
                    {changes.map((change) => (
                      <tr key={change.sequence}>
                        <td style={{ textAlign: 'right' }}>{change.sequence}</td>
                        <td>{change.kind}</td>
                        <td style={{ fontFamily: 'monospace' }}>{change.path}</td>
                        <td>{formatTime(change.revision.created_at)}</td>
                      </tr>
                    ))}
                    {changes.length === 0 && (
                      <tr>
                        <td colSpan={4} className="muted">No changes recorded yet.</td>
                      </tr>
                    )}
                  </tbody>
                </table>
              )}

              {tab === 'shares' && (
                <div>
                  <div className="row" style={{ display: 'flex', gap: '0.5rem', marginBottom: '0.75rem' }}>
                    <input
                      value={sharePath}
                      onChange={(event) => setSharePath(event.target.value)}
                      placeholder="Path to share, e.g. shared/report.md"
                      style={{ flex: 1 }}
                    />
                    <button onClick={() => void handleCreateShare()} disabled={!sharePath.trim()}>
                      Create share link
                    </button>
                  </div>
                  {lastShareURL && (
                    <p className="muted" style={{ wordBreak: 'break-all' }}>
                      New link (shown once): <a href={lastShareURL} target="_blank" rel="noreferrer">{lastShareURL}</a>
                    </p>
                  )}
                  <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid', gap: '0.5rem' }}>
                    {shares.map((share) => (
                      <li key={share.id} style={{ border: '1px solid rgba(128,128,128,0.3)', borderRadius: 8, padding: '0.5rem 0.75rem' }}>
                        <span style={{ fontFamily: 'monospace' }}>{share.path}</span>
                        <span className="muted"> · created {formatTime(share.created_at)}{share.expires_at ? ` · expires ${formatTime(share.expires_at)}` : ''}{share.revoked_at ? ' · revoked' : ''}</span>
                        {!share.revoked_at && (
                          <button onClick={() => void handleRevoke(share)} style={{ marginLeft: '0.5rem', fontSize: '0.8rem', color: '#c0392b' }}>
                            Revoke
                          </button>
                        )}
                      </li>
                    ))}
                    {shares.length === 0 && <li className="muted">No share links.</li>}
                  </ul>
                </div>
              )}

              {tab === 'stats' && (
                <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.9rem' }}>
                  <thead>
                    <tr>
                      <th style={{ textAlign: 'left' }}>Day (UTC)</th>
                      <th style={{ textAlign: 'left' }}>Path</th>
                      <th style={{ textAlign: 'left' }}>Kind</th>
                      <th style={{ textAlign: 'right' }}>Reads</th>
                    </tr>
                  </thead>
                  <tbody>
                    {stats.map((stat, index) => (
                      <tr key={`${stat.day}-${stat.path}-${stat.kind}-${index}`}>
                        <td>{stat.day}</td>
                        <td style={{ fontFamily: 'monospace' }}>{stat.path}</td>
                        <td>{stat.kind}</td>
                        <td style={{ textAlign: 'right' }}>{stat.count}</td>
                      </tr>
                    ))}
                    {stats.length === 0 && (
                      <tr>
                        <td colSpan={4} className="muted">No read events in the last 14 days.</td>
                      </tr>
                    )}
                  </tbody>
                </table>
              )}

              {historyPath && (
                <section style={{ marginTop: '1.5rem' }}>
                  <h2 style={{ fontSize: '1rem' }}>History: <span style={{ fontFamily: 'monospace' }}>{historyPath}</span></h2>
                  {historyError && <p className="muted">{historyError}</p>}
                  <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.9rem' }}>
                    <thead>
                      <tr>
                        <th style={{ textAlign: 'left' }}>Revision</th>
                        <th style={{ textAlign: 'left' }}>Kind</th>
                        <th style={{ textAlign: 'right' }}>Size</th>
                        <th style={{ textAlign: 'left' }}>Device</th>
                        <th style={{ textAlign: 'left' }}>Created</th>
                        <th />
                      </tr>
                    </thead>
                    <tbody>
                      {history.map((revision) => (
                        <tr key={revision.id}>
                          <td style={{ fontFamily: 'monospace' }}>{revision.id}</td>
                          <td>{revision.kind}</td>
                          <td style={{ textAlign: 'right' }}>{formatBytes(revision.bytes)}</td>
                          <td className="muted">{revision.device_id ? revision.device_id.slice(0, 8) : '-'}</td>
                          <td>{formatTime(revision.created_at)}</td>
                          <td>
                            {revision.kind === 'put' && (
                              <button onClick={() => void handleRestore(revision)} style={{ fontSize: '0.8rem' }}>
                                Restore
                              </button>
                            )}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </section>
              )}
            </>
          )}
        </div>
      </section>
    </div>
  )
}
