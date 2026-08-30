import { describe, expect, it } from 'vitest'
import { fromYAML, toYAML } from './yaml'

describe('yaml helpers', () => {
  it('round-trips a resource object', () => {
    const resource = {
      apiVersion: 'mikrotik.operator.io/v1alpha1',
      kind: 'MikroTikRoute',
      metadata: { name: 'default', namespace: 'app' },
      spec: { destination: '0.0.0.0/0', gateway: '192.0.2.1' },
    }
    const parsed = fromYAML(toYAML(resource))
    expect(parsed.kind).toBe('MikroTikRoute')
    expect(parsed.metadata.name).toBe('default')
    expect(parsed.spec.destination).toBe('0.0.0.0/0')
  })

  it('rejects non-objects and incomplete documents', () => {
    expect(() => fromYAML('- just a list')).toThrow(/Kubernetes resource object/)
    expect(() => fromYAML('kind: MikroTikRouter')).toThrow(/metadata/)
    expect(() => fromYAML('metadata:\n  name: x\n')).toThrow(/spec/)
  })
})
