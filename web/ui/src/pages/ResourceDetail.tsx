import { App, Button, Card, Descriptions, Space, Tabs, Typography } from 'antd'
import { ArrowLeftOutlined, DeleteOutlined, EditOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, queryKeys } from '../api/client'
import { ConditionsTable } from '../components/ConditionsTable'
import { ManagedBanner } from '../components/ManagedBanner'
import { ReadyBadge } from '../components/ReadyBadge'
import { ResourceDrawer } from '../components/ResourceDrawer'
import { SpecSummary } from '../components/SpecSummary'
import { YamlEditor } from '../components/YamlEditor'
import type { KindConfig } from '../kinds'
import { errorMessage } from '../utils/errors'
import { isManaged } from '../utils/resource'
import { toYAML } from '../utils/yaml'

export function ResourceDetail({ kind }: { kind: KindConfig }) {
  const { message, modal } = App.useApp()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const params = useParams()
  const namespace = params.namespace ?? 'default'
  const name = params.name ?? ''
  const [editing, setEditing] = useState(false)

  const query = useQuery({
    queryKey: queryKeys.resource(kind.slug, namespace, name),
    queryFn: () => api.getResource(kind.slug, namespace, name),
    enabled: Boolean(name),
  })

  const remove = useMutation({
    mutationFn: () => api.deleteResource(kind.slug, namespace, name),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['resources', kind.slug] })
      await queryClient.invalidateQueries({ queryKey: queryKeys.overview })
      message.success(`${kind.singular} deleted`)
      navigate(kind.path)
    },
    onError: (error) => {
      message.error(errorMessage(error))
    },
  })

  const resource = query.data
  const owned = Boolean(resource && isManaged(resource))
  const yaml = resource
    ? toYAML({
        apiVersion: resource.apiVersion,
        kind: resource.kind,
        metadata: resource.metadata,
        spec: resource.spec,
        status: resource.status,
      })
    : ''

  function confirmDelete() {
    if (owned) {
      message.warning('Owned resources cannot be deleted from this UI.')
      return
    }
    modal.confirm({
      title: `Delete ${kind.singular} ${name}?`,
      okText: 'Delete',
      okButtonProps: { danger: true },
      onOk: () => remove.mutateAsync(),
    })
  }

  return (
    <>
      <Space style={{ marginBottom: 16 }}>
        <Link to={kind.path}>
          <Button icon={<ArrowLeftOutlined />}>Back to {kind.label}</Button>
        </Link>
      </Space>
      <div className="page-toolbar">
        <div>
          <Typography.Title level={4} style={{ margin: 0 }}>
            {name}
          </Typography.Title>
          <Space>
            <Typography.Text type="secondary">{namespace}</Typography.Text>
            {resource ? <ReadyBadge resource={resource} /> : null}
          </Space>
        </div>
        <Space>
          <Button icon={<EditOutlined />} disabled={!resource || owned} onClick={() => setEditing(true)}>
            Edit
          </Button>
          <Button danger icon={<DeleteOutlined />} disabled={!resource || owned} onClick={confirmDelete}>
            Delete
          </Button>
        </Space>
      </div>
      {owned && resource ? <ManagedBanner resource={resource} /> : null}
      {query.isError ? (
        <Typography.Text type="danger">{errorMessage(query.error)}</Typography.Text>
      ) : null}
      {resource ? (
        <Tabs
          items={[
            {
              key: 'summary',
              label: 'Summary',
              children: (
                <Space direction="vertical" size="large" style={{ width: '100%' }}>
                  <Card title="Spec" size="small">
                    <SpecSummary resource={resource} />
                  </Card>
                  <Card title="Status" size="small">
                    <Descriptions
                      size="small"
                      column={2}
                      bordered
                      items={[
                        { label: 'Connected', children: formatValue(resource.status?.connected) },
                        { label: 'Applied', children: formatValue(resource.status?.applied) },
                        { label: 'Version', children: resource.status?.version || '—' },
                        { label: 'Router ref', children: resource.status?.routerRef || '—' },
                        { label: 'Target address', children: resource.status?.targetAddress || '—' },
                        { label: 'External address', children: resource.status?.externalAddress || '—' },
                      ].filter((item) => item.children !== '—')}
                    />
                  </Card>
                  <Card title="Conditions" size="small">
                    <ConditionsTable conditions={resource.status?.conditions} />
                  </Card>
                </Space>
              ),
            },
            {
              key: 'yaml',
              label: 'YAML',
              children: <YamlEditor value={yaml} readOnly height={560} />,
            },
          ]}
        />
      ) : null}
      <ResourceDrawer
        kind={kind}
        open={editing}
        mode="edit"
        resource={resource}
        onClose={() => setEditing(false)}
      />
    </>
  )
}

function formatValue(value: unknown): string {
  if (typeof value === 'boolean') {
    return value ? 'true' : 'false'
  }
  if (value === undefined || value === null || value === '') {
    return '—'
  }
  return String(value)
}
