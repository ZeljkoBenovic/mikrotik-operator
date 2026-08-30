import { Navigate, Route, Routes } from 'react-router-dom'
import { AppLayout } from './layout/AppLayout'
import { Dashboard } from './pages/Dashboard'
import { ResourceDetail } from './pages/ResourceDetail'
import { ResourceList } from './pages/ResourceList'
import { KINDS } from './kinds'

export default function App() {
  return (
    <Routes>
      <Route element={<AppLayout />}>
        <Route path="/" element={<Dashboard />} />
        {KINDS.map((kind) => (
          <Route key={`${kind.slug}-list`} path={kind.path} element={<ResourceList kind={kind} />} />
        ))}
        {KINDS.map((kind) => (
          <Route
            key={`${kind.slug}-detail`}
            path={`${kind.path}/:namespace/:name`}
            element={<ResourceDetail kind={kind} />}
          />
        ))}
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  )
}
