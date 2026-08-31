import { Layout, Menu, Select, Typography } from 'antd'
import { DashboardOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'
import { Link, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { api, queryKeys } from '../api/client'
import { KINDS, kindFromPath } from '../kinds'

export type OutletContext = {
  namespaceFilter?: string
}

const ALL_NAMESPACES = '__all__'

export function AppLayout() {
  const location = useLocation()
  const navigate = useNavigate()
  const namespaces = useQuery({ queryKey: queryKeys.namespaces, queryFn: api.namespaces })
  const params = new URLSearchParams(location.search)
  const namespace = params.get('namespace') || ALL_NAMESPACES
  const kind = kindFromPath(location.pathname)
  const selectedKeys = useMemo(() => {
    if (location.pathname === '/') {
      return ['dashboard']
    }
    return kind ? [kind.path] : []
  }, [kind, location.pathname])

  const title = location.pathname === '/' ? 'Dashboard' : kind?.label ?? 'MikroTik Operator'

  function setNamespace(value: string) {
    const next = new URLSearchParams(location.search)
    if (value === ALL_NAMESPACES) {
      next.delete('namespace')
    } else {
      next.set('namespace', value)
    }
    const search = next.toString()
    const base = kind ? kind.path : location.pathname === '/' ? '/' : location.pathname
    navigate(search ? `${base}?${search}` : base)
  }

  return (
    <Layout style={{ minHeight: '100dvh' }}>
      <Layout.Sider width={232} breakpoint="lg" collapsedWidth={72}>
        <div className="app-sider-brand">
          <div className="app-sider-mark">MK</div>
          <div>
            <div className="app-sider-title">MikroTik Operator</div>
            <div className="app-sider-sub">Admin</div>
          </div>
        </div>
        <Menu
          theme="dark"
          mode="inline"
          selectedKeys={selectedKeys}
          items={[
            {
              key: 'dashboard',
              icon: <DashboardOutlined />,
              label: <Link to="/">Dashboard</Link>,
            },
            ...KINDS.map((item) => {
              const Icon = item.icon
              const search = namespace !== ALL_NAMESPACES ? `?namespace=${encodeURIComponent(namespace)}` : ''
              return {
                key: item.path,
                icon: <Icon />,
                label: <Link to={`${item.path}${search}`}>{item.label}</Link>,
              }
            }),
          ]}
        />
      </Layout.Sider>
      <Layout>
        <Layout.Header className="app-header">
          <Typography.Text className="app-header-title">{title}</Typography.Text>
          <Select
            showSearch
            style={{ minWidth: 220 }}
            value={namespace}
            onChange={setNamespace}
            placeholder="Namespace"
            options={[
              { value: ALL_NAMESPACES, label: 'All namespaces' },
              ...(namespaces.data ?? []).map((name) => ({ value: name, label: name })),
            ]}
            loading={namespaces.isLoading}
          />
        </Layout.Header>
        <Layout.Content className="app-content">
          <Outlet context={{ namespaceFilter: namespace === ALL_NAMESPACES ? undefined : namespace }} />
        </Layout.Content>
      </Layout>
    </Layout>
  )
}
