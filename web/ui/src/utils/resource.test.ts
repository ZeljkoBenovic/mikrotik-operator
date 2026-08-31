import { describe, expect, it } from 'vitest'
import type { ResourceObject } from '../api/types'
import {
  displayNamespace,
  isManaged,
  isReady,
  managedLabel,
  omitEmpty,
  specSummary,
  toSubmitBody,
} from './resource'

function resource(partial: Partial<ResourceObject> & Pick<ResourceObject, 'metadata' | 'spec'>): ResourceObject {
  return partial
}

describe('isManaged', () => {
  it('treats managedBy as owned', () => {
    expect(
      isManaged(
        resource({
          metadata: { name: 'web' },
          spec: {},
          managedBy: { apiVersion: 'v1', kind: 'Service', namespace: 'app', name: 'frontend' },
        }),
      ),
    ).toBe(true)
  })

  it('treats controller ownerReferences as owned', () => {
    expect(
      isManaged(
        resource({
          metadata: {
            name: 'web',
            ownerReferences: [{ apiVersion: 'v1', kind: 'Service', name: 'frontend', controller: true }],
          },
          spec: {},
        }),
      ),
    ).toBe(true)
  })

  it('ignores non-controller owners and empty resources', () => {
    expect(isManaged(undefined)).toBe(false)
    expect(
      isManaged(
        resource({
          metadata: {
            name: 'web',
            ownerReferences: [{ apiVersion: 'v1', kind: 'ConfigMap', name: 'note', controller: false }],
          },
          spec: {},
        }),
      ),
    ).toBe(false)
  })
})

describe('managedLabel', () => {
  it('prefers managedBy including namespace', () => {
    expect(
      managedLabel(
        resource({
          metadata: { name: 'web' },
          spec: {},
          managedBy: { apiVersion: 'v1', kind: 'Service', namespace: 'app', name: 'frontend' },
        }),
      ),
    ).toBe('Service/app/frontend')
  })

  it('falls back to the controller owner reference', () => {
    expect(
      managedLabel(
        resource({
          metadata: {
            name: 'web',
            ownerReferences: [{ apiVersion: 'v1', kind: 'Ingress', name: 'public', controller: true }],
          },
          spec: {},
        }),
      ),
    ).toBe('Ingress/public')
  })
})

describe('isReady', () => {
  it('reads the Ready condition', () => {
    expect(
      isReady(
        resource({
          metadata: { name: 'edge' },
          spec: {},
          status: { conditions: [{ type: 'Ready', status: 'True' }] },
        }),
      ),
    ).toBe(true)
    expect(
      isReady(
        resource({
          metadata: { name: 'edge' },
          spec: {},
          status: { conditions: [{ type: 'Ready', status: 'False' }] },
        }),
      ),
    ).toBe(false)
  })

  it('falls back to connected and applied', () => {
    expect(isReady(resource({ metadata: { name: 'edge' }, spec: {}, status: { connected: true } }))).toBe(true)
    expect(isReady(resource({ metadata: { name: 'dns' }, spec: {}, status: { applied: true } }))).toBe(true)
    expect(isReady(resource({ metadata: { name: 'dns' }, spec: {} }))).toBe(false)
  })
})

describe('specSummary', () => {
  it('summarizes each kind', () => {
    expect(specSummary('MikroTikRouter', { address: '192.0.2.10' })).toBe('192.0.2.10')
    expect(
      specSummary('MikroTikRouter', {
        routers: [{ address: '192.0.2.10' }, { address: '192.0.2.11' }],
      }),
    ).toBe('192.0.2.10, 192.0.2.11')
    expect(specSummary('MikroTikDNSRecord', { name: 'www', address: '10.0.0.8' })).toBe('www → 10.0.0.8')
    expect(specSummary('MikroTikRoute', { destination: '0.0.0.0/0', gateway: '192.0.2.1' })).toBe(
      '0.0.0.0/0 via 192.0.2.1',
    )
    expect(
      specSummary('MikroTikPortForward', {
        protocol: 'tcp',
        externalPort: 80,
        targetPort: 8080,
        targetAddress: '10.0.0.8',
      }),
    ).toBe('TCP :80 → 10.0.0.8:8080')
    expect(specSummary('MikroTikFirewallRule', { chain: 'forward', action: 'accept' })).toBe('forward / accept')
    expect(specSummary('MikroTikBackup', { routerRef: 'edge' })).toBe('edge (once)')
    expect(specSummary('MikroTikRestore', { backupRef: { name: 'once' }, routerRef: 'edge' })).toBe('once → edge')
    expect(specSummary('Unknown', {})).toBe('—')
  })
})

describe('omitEmpty and toSubmitBody', () => {
  it('strips empty nested values and status', () => {
    const submitted = toSubmitBody({
      apiVersion: 'mikrotik.operator.io/v1alpha1',
      kind: 'MikroTikRouter',
      metadata: {
        name: 'edge',
        namespace: 'app',
        resourceVersion: '9',
        uid: 'should-drop',
        labels: { app: 'edge' },
      },
      spec: {
        address: '192.0.2.10',
        routeGateway: '',
        credentialsSecret: { name: 'creds' },
        routers: [],
      },
      status: { connected: true },
      managedBy: { apiVersion: 'v1', kind: 'Service', namespace: 'app', name: 'web' },
    })
    expect(submitted.status).toBeUndefined()
    expect(submitted.managedBy).toBeUndefined()
    expect(submitted.metadata.uid).toBeUndefined()
    expect(submitted.metadata.resourceVersion).toBe('9')
    expect(submitted.spec).toEqual({
      address: '192.0.2.10',
      credentialsSecret: { name: 'creds' },
    })
  })

  it('drops empty objects and blank strings', () => {
    expect(omitEmpty({ a: '', b: {}, c: [], d: 1, e: { name: 'x' } })).toEqual({ d: 1, e: { name: 'x' } })
  })
})

describe('displayNamespace', () => {
  it('falls back to default', () => {
    expect(displayNamespace(resource({ metadata: { name: 'edge' }, spec: {} }))).toBe('default')
    expect(displayNamespace(resource({ metadata: { name: 'edge', namespace: 'app' }, spec: {} }))).toBe('app')
  })
})
