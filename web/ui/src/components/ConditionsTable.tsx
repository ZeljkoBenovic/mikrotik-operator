import { Table, Tag, Typography } from 'antd'
import type { Condition } from '../api/types'

const statusColor: Record<string, string> = {
  True: 'success',
  False: 'error',
  Unknown: 'warning',
}

export function ConditionsTable({ conditions }: { conditions?: Condition[] }) {
  if (!conditions?.length) {
    return <Typography.Text type="secondary">No conditions reported yet.</Typography.Text>
  }
  return (
    <Table
      size="small"
      rowKey={(row) => `${row.type}-${row.lastTransitionTime ?? ''}`}
      pagination={false}
      dataSource={conditions}
      columns={[
        { title: 'Type', dataIndex: 'type', width: 160 },
        {
          title: 'Status',
          dataIndex: 'status',
          width: 100,
          render: (value: string) => <Tag color={statusColor[value] ?? 'default'}>{value}</Tag>,
        },
        { title: 'Reason', dataIndex: 'reason', width: 180, render: (value?: string) => value || '—' },
        { title: 'Message', dataIndex: 'message', render: (value?: string) => value || '—' },
        { title: 'Last transition', dataIndex: 'lastTransitionTime', width: 200, render: (value?: string) => value || '—' },
      ]}
    />
  )
}
