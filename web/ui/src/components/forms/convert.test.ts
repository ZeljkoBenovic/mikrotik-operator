import { describe, expect, it } from 'vitest'
import { KINDS } from '../../kinds'
import { emptyForm, emptyResource, formFromResource, resourceFromForm } from './convert'
import type { ResourceObject } from '../../api/types'

const routers = KINDS[0]
const dns = KINDS[1]
const forwards = KINDS[3]
const firewall = KINDS[4]

describe('form conversion', () => {
  it('builds empty defaults per kind', () => {
    expect(emptyForm(routers, 'app')).toMatchObject({
      name: '',
      namespace: 'app',
      spec: { endpointMode: 'single', tls: true },
    })
    expect(emptyForm(forwards, 'app').spec).toMatchObject({ protocol: 'tcp', targetType: 'address' })
    expect(emptyForm(firewall, 'app').spec).toMatchObject({ chain: 'forward', action: 'accept' })
    expect(emptyResource(dns, 'app')).toMatchObject({
      kind: 'MikroTikDNSRecord',
      metadata: { namespace: 'app' },
    })
  })

  it('strips single-endpoint fields when using routers[]', () => {
    const body = resourceFromForm(routers, {
      name: 'edge',
      namespace: 'app',
      spec: {
        endpointMode: 'multi',
        address: '192.0.2.10',
        credentialsSecret: { name: 'creds' },
        routers: [{ name: 'a', address: '192.0.2.11', credentialsSecret: { name: 'creds' } }],
      },
    })
    expect(body.spec.address).toBeUndefined()
    expect(body.spec.endpointMode).toBeUndefined()
    expect(body.spec.routers).toHaveLength(1)
  })

  it('keeps exactly one port-forward target', () => {
    const service = resourceFromForm(forwards, {
      name: 'web',
      namespace: 'app',
      spec: {
        targetType: 'service',
        targetAddress: '10.0.0.8',
        serviceRef: { namespace: 'app', name: 'web' },
        podRef: { namespace: 'app', name: 'web-0' },
        protocol: 'tcp',
        routerRef: 'edge',
        externalPort: 80,
        targetPort: 80,
      },
    })
    expect(service.spec.targetAddress).toBeUndefined()
    expect(service.spec.podRef).toBeUndefined()
    expect(service.spec.serviceRef).toEqual({ namespace: 'app', name: 'web' })

    const fromResource = formFromResource(forwards, {
      metadata: { name: 'web', namespace: 'app' },
      spec: { podRef: { namespace: 'app', name: 'web-0' }, protocol: 'tcp' },
    } as ResourceObject)
    expect(fromResource.spec.targetType).toBe('pod')
  })

  it('detects single vs multi router mode from the resource', () => {
    const single = formFromResource(routers, {
      metadata: { name: 'edge', namespace: 'app' },
      spec: { address: '192.0.2.10' },
    } as ResourceObject)
    expect(single.spec.endpointMode).toBe('single')
    const multi = formFromResource(routers, {
      metadata: { name: 'edge', namespace: 'app' },
      spec: { routers: [{ address: '192.0.2.10' }] },
    } as ResourceObject)
    expect(multi.spec.endpointMode).toBe('multi')
  })
})
