import type { Condition, ResourceObject } from '../api/types'

export function isManaged(resource: ResourceObject | undefined): boolean {
  if (!resource) {
    return false
  }
  if (resource.managedBy?.name) {
    return true
  }
  return Boolean(resource.metadata.ownerReferences?.some((ref) => ref.controller))
}

export function isAppliedRestore(resource: ResourceObject | undefined): boolean {
  return resource?.kind === 'MikroTikRestore' && Boolean(resource.status?.applied)
}

export function isReadOnly(resource: ResourceObject | undefined): boolean {
  return isManaged(resource) || isAppliedRestore(resource)
}

export function managedLabel(resource: ResourceObject): string | undefined {
  const owner = resource.managedBy
  if (owner?.kind && owner.name) {
    const ns = owner.namespace ? `${owner.namespace}/` : ''
    return `${owner.kind}/${ns}${owner.name}`
  }
  const controller = resource.metadata.ownerReferences?.find((ref) => ref.controller)
  if (controller) {
    return `${controller.kind}/${controller.name}`
  }
  return undefined
}

export function readyCondition(resource: ResourceObject): Condition | undefined {
  return resource.status?.conditions?.find((item) => item.type === 'Ready')
}

export function isReady(resource: ResourceObject): boolean {
  const ready = readyCondition(resource)
  if (ready) {
    return ready.status === 'True'
  }
  if (typeof resource.status?.connected === 'boolean') {
    return resource.status.connected
  }
  if (typeof resource.status?.applied === 'boolean') {
    return resource.status.applied
  }
  return false
}

export function displayName(resource: ResourceObject): string {
  return resource.metadata.name
}

export function displayNamespace(resource: ResourceObject): string {
  return resource.metadata.namespace || 'default'
}

export function specSummary(kind: string, spec: Record<string, unknown>): string {
  switch (kind) {
    case 'MikroTikRouter': {
      const routers = spec.routers as Array<{ address?: string }> | undefined
      if (Array.isArray(routers) && routers.length > 0) {
        return routers.map((item) => item.address).filter(Boolean).join(', ')
      }
      return String(spec.address ?? '—')
    }
    case 'MikroTikDNSRecord':
      return `${spec.name ?? '—'} → ${spec.address ?? '—'}`
    case 'MikroTikRoute':
      return `${spec.destination ?? '—'} via ${spec.gateway ?? '—'}`
    case 'MikroTikPortForward': {
      const proto = String(spec.protocol ?? '').toUpperCase()
      const target =
        spec.targetAddress ||
        (spec.serviceRef as { name?: string } | undefined)?.name ||
        (spec.podRef as { name?: string } | undefined)?.name ||
        '—'
      return `${proto} :${spec.externalPort ?? '—'} → ${target}:${spec.targetPort ?? '—'}`
    }
    case 'MikroTikFirewallRule':
      return `${spec.chain ?? '—'} / ${spec.action ?? '—'}`
    case 'MikroTikBackup':
      return spec.schedule ? `${spec.routerRef ?? '—'} @ ${spec.schedule}` : `${spec.routerRef ?? '—'} (once)`
    case 'MikroTikRestore':
      return `${(spec.backupRef as { name?: string } | undefined)?.name ?? '—'} → ${spec.routerRef || (spec.connection as { address?: string } | undefined)?.address || '—'}`
    default:
      return '—'
  }
}

export function omitEmpty(value: unknown): unknown {
  if (Array.isArray(value)) {
    const items = value.map(omitEmpty).filter((item) => item !== undefined)
    return items
  }
  if (value && typeof value === 'object') {
    const out: Record<string, unknown> = {}
    for (const [key, nested] of Object.entries(value as Record<string, unknown>)) {
      const cleaned = omitEmpty(nested)
      if (cleaned === undefined || cleaned === '') {
        continue
      }
      if (Array.isArray(cleaned) && cleaned.length === 0) {
        continue
      }
      if (cleaned && typeof cleaned === 'object' && !Array.isArray(cleaned) && Object.keys(cleaned).length === 0) {
        continue
      }
      out[key] = cleaned
    }
    return out
  }
  if (value === '' || value === undefined || value === null) {
    return undefined
  }
  return value
}

export function toSubmitBody(resource: ResourceObject): ResourceObject {
  const spec = omitEmpty(resource.spec) as Record<string, unknown>
  return {
    apiVersion: resource.apiVersion,
    kind: resource.kind,
    metadata: {
      name: resource.metadata.name,
      namespace: resource.metadata.namespace,
      ...(resource.metadata.resourceVersion
        ? { resourceVersion: resource.metadata.resourceVersion }
        : {}),
      ...(resource.metadata.labels ? { labels: resource.metadata.labels } : {}),
      ...(resource.metadata.annotations ? { annotations: resource.metadata.annotations } : {}),
    },
    spec,
  }
}
