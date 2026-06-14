import { useEffect, useState } from 'react'

export function ThemeToggle() {
  const [dark, setDark] = useState(() => localStorage.getItem('assethub_theme') === 'dark')

  useEffect(() => {
    document.documentElement.dataset.theme = dark ? 'dark' : 'light'
    localStorage.setItem('assethub_theme', dark ? 'dark' : 'light')
  }, [dark])

  return (
    <button type="button" className="icon-btn" onClick={() => setDark((value) => !value)} aria-label="Toggle theme">
      {dark ? 'Light' : 'Dark'}
    </button>
  )
}
