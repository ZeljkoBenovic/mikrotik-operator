import { Tag } from 'antd'
import { isManaged, managedLabel } from '../utils/resource'
import type { ResourceObject } from '../api/types'

export function ManagedBadge({ resource }: { resource: ResourceObject }) {
  if (!isManaged(resource)) {
    return null
  }
  return <Tag color="purple">Managed{managedLabel(resource) ? ` · ${managedLabel(resource)}` : ''}</Tag>
}
