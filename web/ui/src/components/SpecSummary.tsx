import { Descriptions, Typography } from 'antd'
import type { ResourceObject } from '../api/types'

function text(value: unknown): string {
  if (value === undefined || value === null || value === '') {
    return '—'
  }
  if (Array.isArray(value)) {
    return value.length ? value.map(String).join(', ') : '—'
  }
  if (typeof value === 'object') {
    const rec = value as Record<string, unknown>
    if (typeof rec.name === 'string' && typeof rec.namespace === 'string') {
      return `${rec.namespace}/${rec.name}`
    }
    if (typeof rec.name === 'string') {
      return rec.name
    }
    return JSON.stringify(value)
  }
  if (typeof value === 'boolean') {
    return value ? 'true' : 'false'
  }
  return String(value)
}

function itemsFor(kind: string, spec: Record<string, unknown>): { label: string; children: string }[] {
  switch (kind) {
    case 'MikroTikRouter': {
      const routers = spec.routers as Array<Record<string, unknown>> | undefined
      if (Array.isArray(routers) && routers.length > 0) {
        return [
          {
            label: 'Endpoints',
            children: routers
              .map((item) => {
                const secret = item.credentialsSecret as { name?: string } | undefined
                return `${item.name} ${item.address}${item.tls ? ' (tls)' : ''} secret=${secret?.name ?? '—'}`
              })
              .join('; '),
          },
        ]
      }
      return [
        { label: 'Address', children: text(spec.address) },
        { label: 'Port', children: text(spec.port) },
        { label: 'TLS', children: text(spec.tls) },
        { label: 'Credentials secret', children: text(spec.credentialsSecret) },
        { label: 'Route gateway', children: text(spec.routeGateway) },
      ]
    }
    case 'MikroTikDNSRecord':
      return [
        { label: 'DNS name', children: text(spec.name) },
        { label: 'Address', children: text(spec.address) },
        { label: 'TTL', children: text(spec.ttl) },
        { label: 'Router', children: text(spec.routerRef) },
        { label: 'Service ref', children: text(spec.serviceRef) },
      ]
    case 'MikroTikRoute':
      return [
        { label: 'Destination', children: text(spec.destination) },
        { label: 'Gateway', children: text(spec.gateway) },
        { label: 'Distance', children: text(spec.distance) },
        { label: 'Router', children: text(spec.routerRef) },
      ]
    case 'MikroTikPortForward':
      return [
        { label: 'Router', children: text(spec.routerRef) },
        { label: 'Protocol', children: text(spec.protocol) },
        { label: 'External port', children: text(spec.externalPort) },
        { label: 'Destination address', children: text(spec.destinationAddress) },
        { label: 'Target port', children: text(spec.targetPort) },
        { label: 'Target address', children: text(spec.targetAddress) },
        { label: 'Service ref', children: text(spec.serviceRef) },
        { label: 'Pod ref', children: text(spec.podRef) },
      ]
    case 'MikroTikFirewallRule':
      return [
        { label: 'Router', children: text(spec.routerRef) },
        { label: 'Chain', children: text(spec.chain) },
        { label: 'Action', children: text(spec.action) },
        { label: 'Protocol', children: text(spec.protocol) },
        { label: 'Source address', children: text(spec.sourceAddress) },
        { label: 'Destination address', children: text(spec.destinationAddress) },
        { label: 'Source port', children: text(spec.sourcePort) },
        { label: 'Destination port', children: text(spec.destinationPort) },
        { label: 'In interface', children: text(spec.inInterface) },
        { label: 'Out interface', children: text(spec.outInterface) },
        { label: 'Connection state', children: text(spec.connectionState) },
        { label: 'Connection NAT state', children: text(spec.connectionNatState) },
        { label: 'Log prefix', children: text(spec.logPrefix) },
        { label: 'Place before', children: text(spec.placeBefore) },
      ]
    case 'MikroTikBackup':
      return [
        { label: 'Router', children: text(spec.routerRef) },
        { label: 'Schedule', children: text(spec.schedule) },
        { label: 'Retention', children: text(spec.retention) },
        { label: 'Remote enabled', children: text((spec.remote as { enabled?: boolean } | undefined)?.enabled) },
      ]
    case 'MikroTikRestore':
      return [
        { label: 'Backup', children: text(spec.backupRef) },
        { label: 'Router', children: text(spec.routerRef) },
        { label: 'Inline address', children: text((spec.connection as { address?: string } | undefined)?.address) },
        { label: 'Confirmed', children: spec.confirm === 'RESTORE' ? 'yes' : 'no' },
      ]
    default:
      return Object.entries(spec).map(([label, value]) => ({ label, children: text(value) }))
  }
}

export function SpecSummary({ resource }: { resource: ResourceObject }) {
  const kind = resource.kind ?? ''
  const items = itemsFor(kind, resource.spec ?? {})
  if (!items.length) {
    return <Typography.Text type="secondary">No spec fields.</Typography.Text>
  }
  return <Descriptions size="small" column={1} bordered items={items} />
}
