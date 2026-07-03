import { useTranslation } from 'react-i18next'

export function LangSwitch() {
  const { i18n } = useTranslation()
  const next = i18n.language.startsWith('zh') ? 'en' : 'zh'

  async function toggle() {
    await i18n.changeLanguage(next)
    localStorage.setItem('filehub_lang', next)
  }

  return (
    <button type="button" className="icon-btn" aria-label="Language" onClick={toggle}>
      {i18n.language.startsWith('zh') ? '中' : 'EN'}
    </button>
  )
}
