import type { ReactNode } from 'react'
import { assetHubBasePath, assetHubURL } from '../basePath'
import { ThemeToggle } from './ThemeToggle'
import { LangSwitch } from './LangSwitch'

const basePath = assetHubBasePath()
const homePath = basePath || '/'
const docsPath = assetHubURL('/docs')

function isEmbedded() {
  const params = new URLSearchParams(window.location.search)
  return params.get('embed') === '1' || window.self !== window.top
}

export function Layout({ nav, children }: { nav: ReactNode; children: ReactNode }) {
  const embedded = isEmbedded()

  if (embedded) {
    return <main className="content embedded-content">{children}</main>
  }

  return (
    <>
      <header className="header">
        <div className="container header-inner">
          <a href={homePath} className="logo">
            <span className="logo-icon" aria-hidden="true">
              <svg viewBox="0 0 24 24" role="img">
                <path d="M6 6.5h12v11H6z" />
                <path d="M9 3.5h6v3H9z" />
                <path d="M9 17.5h6v3H9z" />
              </svg>
            </span>
            <span>AssetHub</span>
          </a>
          <nav className="nav primary-nav">
            {nav}
          </nav>
          <div className="nav-utilities">
            <a href={docsPath} target="_blank" rel="noreferrer">API</a>
            <a href="https://github.com/saker-ai" target="_blank" rel="noreferrer" className="theme-toggle" title="GitHub">
              <svg width="18" height="18" viewBox="0 0 16 16" fill="currentColor"><path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z" /></svg>
            </a>
            <ThemeToggle />
            <LangSwitch />
          </div>
          <details className="nav-more">
            <summary aria-label="More navigation">More</summary>
            <div className="nav-more-panel">
              <a href={docsPath} target="_blank" rel="noreferrer">API</a>
              <a href="https://github.com/saker-ai" target="_blank" rel="noreferrer">GitHub</a>
              <ThemeToggle />
              <LangSwitch />
            </div>
          </details>
        </div>
      </header>
      <main className="content">{children}</main>
      <footer className="footer">
        <div className="container">AssetHub · AI asset registry and OpenAI-compatible file store</div>
      </footer>
    </>
  )
}
