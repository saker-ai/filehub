import { NavLink, Route, Routes } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Layout } from './components/Layout'
import Home from './pages/Home'
import Assets from './pages/Assets'
import Upload from './pages/Upload'
import Login from './pages/Login'

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
      <Routes>
        <Route path="/" element={<Home />} />
        <Route path="/assets" element={<Assets />} />
        <Route path="/upload" element={<Upload />} />
        <Route path="/login" element={<Login />} />
      </Routes>
    </Layout>
  )
}
