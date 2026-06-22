import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { createAssetAIReview, listAssetAIReviews, type AIReview } from '@saker/assethub-client'

export function AIReviewResults({ assetID, editable = false }: { assetID: string; editable?: boolean }) {
  const { t } = useTranslation()
  const [reviews, setReviews] = useState<AIReview[]>([])
  const [model, setModel] = useState('')
  const [verdict, setVerdict] = useState('approved')
  const [score, setScore] = useState('')
  const [rubric, setRubric] = useState('')
  const [confidence, setConfidence] = useState('')
  const [promptVersion, setPromptVersion] = useState('')
  const [reviewJobID, setReviewJobID] = useState('')
  const [rawResponseID, setRawResponseID] = useState('')
  const [summary, setSummary] = useState('')
  const [metadata, setMetadata] = useState('{}')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function refresh() {
    const result = await listAssetAIReviews(assetID)
    setReviews(result.data)
  }

  useEffect(() => {
    let alive = true
    setError('')
    void listAssetAIReviews(assetID).then((result) => {
      if (alive) setReviews(result.data)
    }).catch((err) => {
      if (alive) setError(err instanceof Error ? err.message : String(err))
    })
    return () => {
      alive = false
    }
  }, [assetID])

  async function save() {
    setBusy(true)
    setError('')
    try {
      const parsedMetadata = metadata.trim() ? JSON.parse(metadata) as Record<string, unknown> : {}
      const parsedScore = score.trim() === '' ? null : Number(score)
      if (parsedScore !== null && (!Number.isFinite(parsedScore) || parsedScore < 0 || parsedScore > 1)) {
        throw new Error(t('aiReviewScoreError'))
      }
      const parsedConfidence = confidence.trim() === '' ? null : Number(confidence)
      if (parsedConfidence !== null && (!Number.isFinite(parsedConfidence) || parsedConfidence < 0 || parsedConfidence > 1)) {
        throw new Error(t('aiReviewConfidenceError'))
      }
      await createAssetAIReview(assetID, {
        model: model.trim(),
        verdict,
        score: parsedScore,
        summary: summary.trim(),
        rubric: rubric.trim(),
        confidence: parsedConfidence,
        prompt_version: promptVersion.trim(),
        review_job_id: reviewJobID.trim(),
        raw_response_id: rawResponseID.trim(),
        metadata: parsedMetadata,
      })
      setSummary('')
      setScore('')
      setConfidence('')
      setMetadata('{}')
      await refresh()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="ai-reviews">
      <header>
        <h3>{t('aiReviewResults')}</h3>
        <span>{reviews.length}</span>
      </header>
      {reviews.length === 0 ? <p className="muted">{t('noAIReviews')}</p> : (
        <div className="ai-review-list">
          {reviews.map((review) => <AIReviewItem key={review.id} review={review} />)}
        </div>
      )}
      {editable ? (
        <div className="ai-review-form">
          <label className="field"><span>{t('aiReviewModel')}</span><input value={model} onChange={(event) => setModel(event.target.value)} /></label>
          <label className="field"><span>{t('decision')}</span><select value={verdict} onChange={(event) => setVerdict(event.target.value)}>
            <option value="approved">{t('decisionApproved')}</option>
            <option value="rejected">{t('decisionRejected')}</option>
            <option value="needs_revision">{t('decisionNeedsRevision')}</option>
            <option value="uncertain">{t('decisionUncertain')}</option>
          </select></label>
          <label className="field"><span>{t('aiReviewScore')}</span><input inputMode="decimal" value={score} onChange={(event) => setScore(event.target.value)} /></label>
          <label className="field"><span>{t('aiReviewRubric')}</span><input value={rubric} onChange={(event) => setRubric(event.target.value)} /></label>
          <label className="field"><span>{t('aiReviewConfidence')}</span><input inputMode="decimal" value={confidence} onChange={(event) => setConfidence(event.target.value)} /></label>
          <label className="field"><span>{t('promptVersion')}</span><input value={promptVersion} onChange={(event) => setPromptVersion(event.target.value)} /></label>
          <label className="field"><span>{t('reviewJobID')}</span><input value={reviewJobID} onChange={(event) => setReviewJobID(event.target.value)} /></label>
          <label className="field"><span>{t('rawResponseID')}</span><input value={rawResponseID} onChange={(event) => setRawResponseID(event.target.value)} /></label>
          <label className="field"><span>{t('summary')}</span><textarea value={summary} onChange={(event) => setSummary(event.target.value)} /></label>
          <label className="field"><span>{t('metadata')}</span><textarea value={metadata} onChange={(event) => setMetadata(event.target.value)} /></label>
          {error ? <p className="form-error" role="alert">{error}</p> : null}
          <button type="button" disabled={busy} onClick={save}>{busy ? t('working') : t('recordAIReview')}</button>
        </div>
      ) : error ? <p className="form-error" role="alert">{error}</p> : null}
    </section>
  )
}

function AIReviewItem({ review }: { review: AIReview }) {
  const { t } = useTranslation()
  return (
    <article className={`ai-review-item verdict-${review.verdict}`}>
      <header>
        <span>{labelForVerdict(review.verdict, t)}</span>
        {review.score === null || review.score === undefined ? null : <strong>{Math.round(review.score * 100)}%</strong>}
      </header>
      <p>{review.summary || '-'}</p>
      <dl>
        <div><dt>{t('aiReviewModel')}</dt><dd>{review.model || '-'}</dd></div>
        <div><dt>{t('aiReviewRubric')}</dt><dd>{review.rubric || '-'}</dd></div>
        <div><dt>{t('aiReviewConfidence')}</dt><dd>{review.confidence === null || review.confidence === undefined ? '-' : `${Math.round(review.confidence * 100)}%`}</dd></div>
        <div><dt>{t('promptVersion')}</dt><dd>{review.prompt_version || '-'}</dd></div>
        <div><dt>{t('reviewJobID')}</dt><dd>{review.review_job_id || '-'}</dd></div>
        <div><dt>{t('rawResponseID')}</dt><dd>{review.raw_response_id || '-'}</dd></div>
        <div><dt>{t('created')}</dt><dd>{formatTimestamp(review.created_at)}</dd></div>
      </dl>
      {review.metadata && Object.keys(review.metadata).length > 0 ? <pre className="code">{JSON.stringify(review.metadata, null, 2)}</pre> : null}
    </article>
  )
}

function labelForVerdict(verdict: string, t: (key: string) => string) {
  if (verdict === 'approved') return t('decisionApproved')
  if (verdict === 'rejected') return t('decisionRejected')
  if (verdict === 'needs_revision') return t('decisionNeedsRevision')
  if (verdict === 'uncertain') return t('decisionUncertain')
  return verdict
}

function formatTimestamp(value: number) {
  if (!value) return '-'
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value * 1000))
}
