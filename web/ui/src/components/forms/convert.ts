import { API_VERSION } from '../../api/types'
import type { ResourceObject } from '../../api/types'
import type { KindConfig } from '../../kinds'

export type EditorFormValues = {
  name: string
  namespace: string
  spec: Record<string, unknown>
}

function asRecord(value: unknown): Record<string, unknown> {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    return { ...(value as Record<string, unknown>) }
  }
  return {}
}

function namespaced(value: unknown): { namespace?: string; name?: string } | undefined {
  const rec = asRecord(value)
  if (!rec.name && !rec.namespace) {
    return undefined
  }
  return rec as { namespace?: string; name?: string }
}

export function formFromResource(kind: KindConfig, resource: ResourceObject): EditorFormValues {
  const spec = asRecord(resource.spec)
  if (kind.apiKind === 'MikroTikRouter') {
    const routers = spec.routers
    spec.endpointMode = Array.isArray(routers) && routers.length > 0 ? 'multi' : 'single'
  }
  if (kind.apiKind === 'MikroTikPortForward') {
    if (namespaced(spec.podRef)?.name) {
      spec.targetType = 'pod'
    } else if (namespaced(spec.serviceRef)?.name && !spec.targetAddress) {
      spec.targetType = 'service'
    } else {
      spec.targetType = 'address'
    }
  }
  return {
    name: resource.metadata.name,
    namespace: resource.metadata.namespace || 'default',
    spec,
  }
}

export function emptyForm(kind: KindConfig, namespace: string): EditorFormValues {
  const spec: Record<string, unknown> = {}
  if (kind.apiKind === 'MikroTikRouter') {
    spec.endpointMode = 'single'
    spec.tls = true
  }
  if (kind.apiKind === 'MikroTikPortForward') {
    spec.protocol = 'tcp'
    spec.targetType = 'address'
  }
  if (kind.apiKind === 'MikroTikFirewallRule') {
    spec.chain = 'forward'
    spec.action = 'accept'
  }
  return {
    name: '',
    namespace,
    spec,
  }
}

export function resourceFromForm(kind: KindConfig, values: EditorFormValues): ResourceObject {
  const spec = asRecord(values.spec)
  if (kind.apiKind === 'MikroTikRouter') {
    if (spec.endpointMode === 'multi') {
      delete spec.address
      delete spec.port
      delete spec.tls
      delete spec.credentialsSecret
      delete spec.routeGateway
    } else {
      delete spec.routers
    }
    delete spec.endpointMode
  }
  if (kind.apiKind === 'MikroTikPortForward') {
    const targetType = spec.targetType
    delete spec.targetType
    if (targetType === 'service') {
      delete spec.targetAddress
      delete spec.podRef
    } else if (targetType === 'pod') {
      delete spec.targetAddress
      delete spec.serviceRef
    } else {
      delete spec.serviceRef
      delete spec.podRef
    }
    if (!spec.destinationAddress) {
      delete spec.destinationAddress
    }
  }
  delete spec._namespace
  return {
    apiVersion: API_VERSION,
    kind: kind.apiKind,
    metadata: {
      name: values.name,
      namespace: values.namespace,
    },
    spec,
  }
}

export function emptyResource(kind: KindConfig, namespace: string): ResourceObject {
  return resourceFromForm(kind, emptyForm(kind, namespace))
}
