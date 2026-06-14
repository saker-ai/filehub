import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Asset, getStats, listAssets, type AssetStats } from '../api/client'
import { AssetCard, formatBytes } from '../components/AssetCard'
import { AssetDetail } from './AssetDetail'

export default function Home() {
  const { t } = useTranslation()
  const [stats, setStats] = useState<AssetStats | null>(null)
  const [assets, setAssets] = useState<Asset[]>([])
  const [active, setActive] = useState<Asset | null>(null)

  useEffect(() => {
    void Promise.all([getStats().then(setStats), listAssets({ limit: 8 }).then((result) => setAssets(result.data))])
  }, [])

  return (
    <div className="page">
      <header className="page-header">
        <h1>{t('dashboard')}</h1>
        <p>{t('dashboardSubtitle')}</p>
      </header>
      <section className="stats-grid">
        <Stat label={t('total')} value={stats?.total ?? 0} />
        <Stat label={t('storage')} value={formatBytes(stats?.total_bytes ?? 0)} />
        <Stat label={t('ready')} value={stats?.by_status.ready ?? 0} />
        <Stat label={t('media')} value={stats?.by_purpose.media ?? 0} />
      </section>
      <section className="panel dashboard-split">
        <div>
          <h2>{t('contentTypes')}</h2>
          <ContentTypeChart values={stats?.by_content_type ?? {}} />
        </div>
        <div>
          <h2>{t('sources')}</h2>
          <Breakdown values={stats?.by_source ?? {}} />
        </div>
      </section>
      <section className="panel">
        <h2>{t('recentAssets')}</h2>
        <div className="asset-grid">{assets.map((asset) => <AssetCard key={asset.id} asset={asset} onOpen={() => setActive(asset)} />)}</div>
      </section>
      {active ? <AssetDetail assetID={active.id} onClose={() => setActive(null)} /> : null}
    </div>
  )
}

function Stat({ label, value }: { label: string; value: string | number }) {
  return <div className="stat"><span>{label}</span><strong>{value}</strong></div>
}

function ContentTypeChart({ values }: { values: Record<string, number> }) {
  const entries = Object.entries(values).filter(([, value]) => value > 0).sort((a, b) => b[1] - a[1])
  const total = entries.reduce((sum, [, value]) => sum + value, 0)
  let cursor = 0
  const stops = entries.map(([, value], index) => {
    const start = cursor
    cursor += (value / Math.max(total, 1)) * 100
    return `${chartColor(index)} ${start}% ${cursor}%`
  })
  const background = total > 0 ? `conic-gradient(${stops.join(', ')})` : '#d8e0e8'
  return (
    <div className="chart-row">
      <div className="pie-chart" style={{ background }} aria-label={tCount(total)} />
      <Breakdown values={values} />
    </div>
  )
}

function Breakdown({ values }: { values: Record<string, number> }) {
  const entries = Object.entries(values).filter(([, value]) => value > 0).sort((a, b) => b[1] - a[1])
  if (entries.length === 0) return <p className="muted">0</p>
  return (
    <dl className="breakdown">
      {entries.map(([key, value], index) => (
        <div key={key}>
          <dt><span className="swatch" style={{ background: chartColor(index) }} />{key}</dt>
          <dd>{value}</dd>
        </div>
      ))}
    </dl>
  )
}

function chartColor(index: number) {
  return ['#1f7a8c', '#d64545', '#5f7d3a', '#d49a26', '#6d5bd0', '#2f855a', '#8a4b32'][index % 7]
}

function tCount(value: number) {
  return `${value} assets`
}
