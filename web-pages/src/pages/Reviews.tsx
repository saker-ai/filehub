import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { listAssetReviews, type AssetReview } from '@saker/filehub-client'

export default function Reviews() {
  const { t } = useTranslation()
  const [reviews, setReviews] = useState<AssetReview[]>([])
  const [status, setStatus] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    let alive = true
    setLoading(true)
    setError('')
    void listAssetReviews({ status, limit: 50 }).then((result) => {
      if (alive) setReviews(result.data)
    }).catch((err) => {
      if (alive) setError(err instanceof Error ? err.message : String(err))
    }).finally(() => {
      if (alive) setLoading(false)
    })
    return () => {
      alive = false
    }
  }, [status])

  return (
    <div className="page">
      <header className="page-header compare-header">
        <div>
          <h1>{t('reviews')}</h1>
          <p>{t('reviewsSubtitle')}</p>
        </div>
        <label className="field review-status-filter">
          <span>{t('status')}</span>
          <select value={status} onChange={(event) => setStatus(event.target.value)}>
            <option value="">{t('anyStatus')}</option>
            <option value="open">{t('reviewStatusOpen')}</option>
            <option value="completed">{t('reviewStatusCompleted')}</option>
            <option value="archived">{t('reviewStatusArchived')}</option>
          </select>
        </label>
      </header>
      {error ? <p className="form-error" role="alert">{error}</p> : null}
      {loading ? <div className="loading-strip" role="status">{t('loading')}</div> : null}
      {!loading && reviews.length === 0 ? (
        <div className="empty-state">
          <strong>{t('noReviews')}</strong>
          <p>{t('noReviewsHint')}</p>
          <Link className="button-link" to="/assets">{t('assets')}</Link>
        </div>
      ) : null}
      <div className="review-list">
        {reviews.map((review) => (
          <article className="review-row" key={review.id}>
            <div>
              <span className={`status ${review.status}`}>{labelStatus(review.status, t)}</span>
              <h2>{review.title}</h2>
              <p>{review.id} · {review.items.length} {t('assets')}</p>
            </div>
            <dl>
              <div><dt>{t('reviewer')}</dt><dd>{review.reviewer || '-'}</dd></div>
              <div><dt>{t('selectedAsset')}</dt><dd>{review.selected_asset_id || '-'}</dd></div>
              <div><dt>{t('created')}</dt><dd>{formatTimestamp(review.created_at)}</dd></div>
            </dl>
            <Link className="button-link" to={`/reviews/${encodeURIComponent(review.id)}`}>{t('openReview')}</Link>
          </article>
        ))}
      </div>
    </div>
  )
}

function labelStatus(status: string, t: (key: string) => string) {
  if (status === 'open') return t('reviewStatusOpen')
  if (status === 'completed') return t('reviewStatusCompleted')
  if (status === 'archived') return t('reviewStatusArchived')
  return status
}

function formatTimestamp(value: number) {
  if (!value) return '-'
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value * 1000))
}
