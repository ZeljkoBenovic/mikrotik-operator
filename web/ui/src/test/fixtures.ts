import type { ResourceObject } from '../api/types'
import { KINDS } from '../kinds'

export const routerKind = KINDS[0]
export const dnsKind = KINDS[1]
export const portForwardKind = KINDS[3]

export function standaloneRouter(overrides: Partial<ResourceObject> = {}): ResourceObject {
  return {
    apiVersion: 'mikrotik.operator.io/v1alpha1',
    kind: 'MikroTikRouter',
    metadata: { name: 'edge', namespace: 'app', resourceVersion: '1' },
    spec: {
      address: '192.0.2.10',
      credentialsSecret: { name: 'creds' },
    },
    status: {
      connected: true,
      conditions: [{ type: 'Ready', status: 'True', reason: 'Connected' }],
    },
    ...overrides,
  }
}

export function ownedDNS(): ResourceObject {
  return {
    apiVersion: 'mikrotik.operator.io/v1alpha1',
    kind: 'MikroTikDNSRecord',
    metadata: {
      name: 'web',
      namespace: 'app',
      ownerReferences: [
        { apiVersion: 'v1', kind: 'Service', name: 'frontend', controller: true },
      ],
    },
    spec: { name: 'web.example.com', address: '10.0.0.8' },
    managedBy: { apiVersion: 'v1', kind: 'Service', namespace: 'app', name: 'frontend' },
    status: {
      applied: true,
      conditions: [{ type: 'Ready', status: 'True' }],
    },
  }
}

export function notReadyRoute(): ResourceObject {
  return {
    apiVersion: 'mikrotik.operator.io/v1alpha1',
    kind: 'MikroTikRoute',
    metadata: { name: 'default', namespace: 'app' },
    spec: { destination: '0.0.0.0/0', gateway: '192.0.2.1' },
    status: {
      applied: false,
      conditions: [{ type: 'Ready', status: 'False', reason: 'Pending', message: 'waiting' }],
    },
  }
}

