import { Tag, Tooltip } from 'antd'
import { isManaged, managedLabel } from '../utils/resource'
import type { ResourceObject } from '../api/types'

export function ManagedBadge({ resource }: { resource: ResourceObject }) {
  if (!isManaged(resource)) {
    return null
  }
  const owner = managedLabel(resource)
  const text = owner ? `Managed · ${owner}` : 'Managed'
  return (
    <Tooltip title={text}>
      <span className="managed-badge-wrap">
        <Tag color="purple" className="managed-badge">
          {text}
        </Tag>
      </span>
    </Tooltip>
  )
}
