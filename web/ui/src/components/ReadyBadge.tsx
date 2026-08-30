import { Badge } from 'antd'
import { isReady, readyCondition } from '../utils/resource'
import type { ResourceObject } from '../api/types'

export function ReadyBadge({ resource }: { resource: ResourceObject }) {
  const ready = isReady(resource)
  const condition = readyCondition(resource)
  const title = condition?.message || condition?.reason || (ready ? 'Ready' : 'Not ready')
  return <Badge status={ready ? 'success' : 'error'} text={ready ? 'Ready' : 'NotReady'} title={title} />
}
