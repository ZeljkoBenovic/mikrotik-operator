import { App, Button, Input, Space, Table, Typography } from 'antd'
import { DeleteOutlined, EditOutlined, PlusOutlined, ReloadOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useMemo, useState } from 'react'
import { Link, useLocation, useNavigate, useOutletContext } from 'react-router-dom'
import { api, queryKeys } from '../api/client'
import type { ResourceObject } from '../api/types'
import { ManagedBadge } from '../components/ManagedBadge'
import { ReadyBadge } from '../components/ReadyBadge'
import { ResourceDrawer } from '../components/ResourceDrawer'
import type { KindConfig } from '../kinds'
import type { OutletContext } from '../layout/AppLayout'
import { errorMessage } from '../utils/errors'
import { liveListRefetchInterval } from '../utils/liveQuery'
import { displayNamespace, isManaged, specSummary } from '../utils/resource'

export function ResourceList({ kind }: { kind: KindConfig }) {
  const { message, modal } = App.useApp()
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const location = useLocation()
  const { namespaceFilter } = useOutletContext<OutletContext>()
  const [search, setSearch] = useState('')
  const [drawer, setDrawer] = useState<{ mode: 'create' | 'edit'; resource?: ResourceObject } | null>(null)

  const list = useQuery({
    queryKey: queryKeys.resources(kind.slug, namespaceFilter),
    queryFn: () => api.listResources(kind.slug, namespaceFilter),
    refetchInterval: liveListRefetchInterval,
  })

  const remove = useMutation({
    mutationFn: (resource: ResourceObject) =>
      api.deleteResource(kind.slug, displayNamespace(resource), resource.metadata.name),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: queryKeys.resources(kind.slug, namespaceFilter) })
      await queryClient.invalidateQueries({ queryKey: queryKeys.overview })
      message.success(`${kind.singular} deleted`)
    },
    onError: (error) => {
      message.error(errorMessage(error))
    },
  })

  const filtered = useMemo(() => {
    const items = list.data ?? []
    const q = search.trim().toLowerCase()
    if (!q) {
      return items
    }
    return items.filter((item) => {
      const hay = [
        item.metadata.name,
        item.metadata.namespace,
        specSummary(kind.apiKind, item.spec ?? {}),
      ]
        .join(' ')
        .toLowerCase()
      return hay.includes(q)
    })
  }, [kind.apiKind, list.data, search])

  const liveDrawerResource = useMemo(() => {
    const snapshot = drawer?.resource
    if (!snapshot) {
      return snapshot
    }
    return (
      (list.data ?? []).find(
        (item) =>
          item.metadata.name === snapshot.metadata.name &&
          item.metadata.namespace === snapshot.metadata.namespace,
      ) ?? snapshot
    )
  }, [drawer?.resource, list.data])

  function showCreatedResource(namespace: string) {
    if (!namespaceFilter || namespaceFilter === namespace) {
      return
    }
    const params = new URLSearchParams(location.search)
    params.set('namespace', namespace)
    navigate(`${location.pathname}?${params.toString()}`, { replace: true })
  }

  function confirmDelete(resource: ResourceObject) {
    if (isManaged(resource)) {
      message.warning('Owned resources cannot be deleted from this UI.')
      return
    }
    modal.confirm({
      title: `Delete ${kind.singular} ${resource.metadata.name}?`,
      content: 'This removes the custom resource. RouterOS entries owned by the operator are cleaned up by the controller.',
      okText: 'Delete',
      okButtonProps: { danger: true },
      onOk: () => remove.mutateAsync(resource),
    })
  }

  return (
    <>
      <div className="page-toolbar">
        <div>
          <Typography.Title level={4} style={{ margin: 0 }}>
            {kind.label}
          </Typography.Title>
          <Typography.Text type="secondary">{kind.description}</Typography.Text>
        </div>
        <div className="page-toolbar-filters">
          <Input.Search
            allowClear
            placeholder="Search name or spec"
            style={{ width: 260 }}
            value={search}
            onChange={(event) => setSearch(event.target.value)}
          />
          <Button icon={<ReloadOutlined />} onClick={() => void list.refetch()} />
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setDrawer({ mode: 'create' })}>
            Create
          </Button>
        </div>
      </div>
      <Table
        rowKey={(row) => `${row.metadata.namespace}/${row.metadata.name}`}
        loading={list.isLoading}
        dataSource={filtered}
        pagination={{ pageSize: 20, showSizeChanger: true }}
        columns={[
          {
            title: 'Name',
            render: (_, row) => (
              <Link to={`${kind.path}/${displayNamespace(row)}/${row.metadata.name}`}>{row.metadata.name}</Link>
            ),
          },
          { title: 'Namespace', render: (_, row) => displayNamespace(row), width: 160 },
          {
            title: 'Summary',
            ellipsis: true,
            render: (_, row) => specSummary(kind.apiKind, row.spec ?? {}),
          },
          { title: 'Ready', width: 120, render: (_, row) => <ReadyBadge resource={row} /> },
          { title: 'Ownership', width: 260, render: (_, row) => <ManagedBadge resource={row} /> },
          {
            title: 'Actions',
            width: 120,
            render: (_, row) => {
              const owned = isManaged(row)
              return (
                <Space>
                  <Button
                    size="small"
                    icon={<EditOutlined />}
                    disabled={owned}
                    title={owned ? 'Managed resources are read-only' : 'Edit'}
                    onClick={() => setDrawer({ mode: 'edit', resource: row })}
                  />
                  <Button
                    size="small"
                    danger
                    icon={<DeleteOutlined />}
                    disabled={owned}
                    title={owned ? 'Managed resources are read-only' : 'Delete'}
                    onClick={() => confirmDelete(row)}
                  />
                </Space>
              )
            },
          },
        ]}
      />
      <ResourceDrawer
        kind={kind}
        open={Boolean(drawer)}
        mode={drawer?.mode ?? 'create'}
        resource={liveDrawerResource}
        onClose={() => setDrawer(null)}
        onCreated={showCreatedResource}
      />
    </>
  )
}
