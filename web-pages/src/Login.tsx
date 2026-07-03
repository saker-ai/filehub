import { useState } from 'react'
import type { FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { getAPIKey, setAPIKey } from '@saker/filehub-client'

export default function Login() {
  const { t } = useTranslation()
  const [key, setKey] = useState(getAPIKey())
  const [saved, setSaved] = useState(false)

  function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setAPIKey(key)
    setSaved(true)
  }

  return (
    <div className="page narrow">
      <header className="page-header">
        <h1>{t('login')}</h1>
        <p>{t('loginSubtitle')}</p>
      </header>
      <form className="upload-form" onSubmit={save}>
        <label className="field"><span>{t('apiKey')}</span><input type="password" value={key} onChange={(event) => { setKey(event.target.value); setSaved(false) }} autoComplete="off" /></label>
        <button type="submit">{t('save')}</button>
      </form>
      {saved ? <p className="success" role="status">{t('saved')}</p> : null}
    </div>
  )
}
