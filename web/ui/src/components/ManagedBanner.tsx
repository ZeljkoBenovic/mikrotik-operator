import { Alert } from 'antd'
import { managedLabel } from '../utils/resource'
import type { ResourceObject } from '../api/types'

export function ManagedBanner({ resource }: { resource: ResourceObject }) {
  const label = managedLabel(resource) ?? 'an owning Kubernetes object'
  const owner = resource.managedBy
  const kind = owner?.kind ?? 'Service, Ingress, or HTTPRoute'
  return (
    <Alert
      className="managed-banner"
      type="info"
      showIcon
      message={`Managed by ${label}`}
      description={`This resource is owned by a ${kind}. Edit and delete are disabled here — change the owning Service, Ingress, or HTTPRoute instead.`}
    />
  )
}
