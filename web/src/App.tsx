import { lazy, Suspense } from 'react'
import { NavLink, Route, Routes } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Layout } from './components/Layout'
import Home from './pages/Home'

const Assets = lazy(() => import('./pages/Assets'))
const AssetCompare = lazy(() => import('./pages/AssetCompare'))
const Reviews = lazy(() => import('./pages/Reviews'))
const RunCompare = lazy(() => import('./pages/RunCompare'))
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
          <NavLink to="/run-compare">{t('runCompare')}</NavLink>
          <NavLink to="/reviews">{t('reviews')}</NavLink>
          <NavLink to="/upload">{t('upload')}</NavLink>
          <NavLink to="/login">{t('login')}</NavLink>
        </>
      }
    >
      <Suspense fallback={<p className="muted">{t('loading')}</p>}>
        <Routes>
          <Route path="/" element={<Home />} />
          <Route path="/assets" element={<Assets />} />
          <Route path="/compare" element={<AssetCompare />} />
          <Route path="/run-compare" element={<RunCompare />} />
          <Route path="/reviews" element={<Reviews />} />
          <Route path="/reviews/:reviewID" element={<AssetCompare />} />
          <Route path="/upload" element={<Upload />} />
          <Route path="/login" element={<Login />} />
        </Routes>
      </Suspense>
    </Layout>
  )
}
