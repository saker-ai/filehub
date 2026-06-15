import { lazy, Suspense } from 'react'
import { NavLink, Route, Routes } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Layout } from './components/Layout'
import Home from './pages/Home'

const Assets = lazy(() => import('./pages/Assets'))
const Upload = lazy(() => import('./pages/Upload'))
const Login = lazy(() => import('./pages/Login'))

export default function App() {
  const { t } = useTranslation()
  return (
    <Layout
      nav={
        <>
          <NavLink to="/" end>{t('dashboard')}</NavLink>
          <NavLink to="/assets">{t('assets')}</NavLink>
          <NavLink to="/upload">{t('upload')}</NavLink>
          <NavLink to="/login">{t('login')}</NavLink>
        </>
      }
    >
      <Suspense fallback={<p className="muted">{t('loading')}</p>}>
        <Routes>
          <Route path="/" element={<Home />} />
          <Route path="/assets" element={<Assets />} />
          <Route path="/upload" element={<Upload />} />
          <Route path="/login" element={<Login />} />
        </Routes>
      </Suspense>
    </Layout>
  )
}
