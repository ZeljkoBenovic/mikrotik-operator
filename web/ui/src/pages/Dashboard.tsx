import { Card, Col, Empty, Row, Statistic, Table, Typography } from 'antd'
import { useQueries, useQuery } from '@tanstack/react-query'
import { Link, useOutletContext } from 'react-router-dom'
import { api, normalizeOverview, queryKeys } from '../api/client'
import type { KindCount, ResourceObject } from '../api/types'
import { ReadyBadge } from '../components/ReadyBadge'
import { KINDS } from '../kinds'
import type { OutletContext } from '../layout/AppLayout'
import { isReady, specSummary } from '../utils/resource'

export function Dashboard() {
  const { namespaceFilter } = useOutletContext<OutletContext>()
  const overview = useQuery({
    queryKey: [...queryKeys.overview, namespaceFilter ?? 'all'],
    queryFn: api.overview,
  })
  const lists = useQueries({
    queries: KINDS.map((kind) => ({
      queryKey: queryKeys.resources(kind.slug, namespaceFilter),
      queryFn: () => api.listResources(kind.slug, namespaceFilter),
    })),
  })

  const countsFromLists: Record<string, KindCount> = {}
  const notReady: Array<{ kindPath: string; kindLabel: string; resource: ResourceObject }> = []
  KINDS.forEach((kind, index) => {
    const items = lists[index]?.data ?? []
    const notReadyItems = items.filter((item) => !isReady(item))
    countsFromLists[kind.slug] = {
      total: items.length,
      ready: items.length - notReadyItems.length,
      notReady: notReadyItems.length,
    }
    for (const resource of notReadyItems) {
      notReady.push({ kindPath: kind.path, kindLabel: kind.singular, resource })
    }
  })

  const overviewCounts = overview.data ? normalizeOverview(overview.data) : {}
  const useOverview = Object.keys(overviewCounts).length > 0 && !namespaceFilter

  return (
    <>
      <Row gutter={[16, 16]}>
        {KINDS.map((kind) => {
          const Icon = kind.icon
          const count = useOverview
            ? overviewCounts[kind.slug] ?? countsFromLists[kind.slug]
            : countsFromLists[kind.slug]
          const total = count?.total ?? 0
          const notReadyCount = count?.notReady ?? 0
          const readyCount = count?.ready ?? Math.max(0, total - notReadyCount)
          return (
            <Col xs={24} sm={12} xl={8} xxl={4} key={kind.slug}>
              <Link to={namespaceFilter ? `${kind.path}?namespace=${encodeURIComponent(namespaceFilter)}` : kind.path}>
                <Card hoverable>
                  <Statistic
                    title={
                      <span>
                        <Icon style={{ marginRight: 8 }} />
                        {kind.label}
                      </span>
                    }
                    value={total}
                  />
                  <Typography.Text type={notReadyCount ? 'danger' : 'success'}>
                    {readyCount} Ready · {notReadyCount} NotReady
                  </Typography.Text>
                </Card>
              </Link>
            </Col>
          )
        })}
      </Row>

      <Card title="Not ready resources" style={{ marginTop: 16 }}>
        {notReady.length === 0 ? (
          <Empty description="All observed resources are Ready." />
        ) : (
          <Table
            size="small"
            rowKey={(row) =>
              `${row.kindLabel}/${row.resource.metadata.namespace}/${row.resource.metadata.name}`
            }
            dataSource={notReady}
            pagination={{ pageSize: 10 }}
            columns={[
              { title: 'Kind', dataIndex: 'kindLabel', width: 160 },
              {
                title: 'Name',
                render: (_, row) => (
                  <Link to={`${row.kindPath}/${row.resource.metadata.namespace}/${row.resource.metadata.name}`}>
                    {row.resource.metadata.name}
                  </Link>
                ),
              },
              {
                title: 'Namespace',
                render: (_, row) => row.resource.metadata.namespace || 'default',
              },
              {
                title: 'Summary',
                render: (_, row) =>
                  specSummary(row.resource.kind || row.kindLabel, row.resource.spec ?? {}),
              },
              {
                title: 'Status',
                width: 120,
                render: (_, row) => <ReadyBadge resource={row.resource} />,
              },
            ]}
          />
        )}
      </Card>
      <p className="dashboard-warning">
        This admin UI has no authentication. Use it only on a trusted network or behind an authenticating proxy.
      </p>
    </>
  )
}
