import { describe, expect, it } from 'vitest'
import { KINDS, kindFromPath } from './kinds'

describe('kind registry', () => {
  it('covers the five CRDs with stable slugs and routes', () => {
    expect(KINDS.map((kind) => kind.slug)).toEqual([
      'mikrotikrouters',
      'mikrotikdnsrecords',
      'mikrotikroutes',
      'mikrotikportforwards',
      'mikrotikfirewallrules',
    ])
    expect(KINDS.map((kind) => kind.path)).toEqual([
      '/routers',
      '/dns-records',
      '/routes',
      '/port-forwards',
      '/firewall-rules',
    ])
  })

  it('resolves list and detail paths', () => {
    expect(kindFromPath('/port-forwards')?.apiKind).toBe('MikroTikPortForward')
    expect(kindFromPath('/port-forwards/app/https')?.slug).toBe('mikrotikportforwards')
    expect(kindFromPath('/nope')).toBeUndefined()
    expect(kindFromPath('/')).toBeUndefined()
  })

  it('does not treat singular or prefix-overlapping paths as a kind', () => {
    expect(kindFromPath('/router')).toBeUndefined()
    expect(kindFromPath('/port-forward')).toBeUndefined()
    expect(kindFromPath('/routes')?.apiKind).toBe('MikroTikRoute')
    expect(kindFromPath('/routers')?.apiKind).toBe('MikroTikRouter')
    expect(kindFromPath('/routes/app/default')?.apiKind).toBe('MikroTikRoute')
    expect(kindFromPath('/routers/app/edge')?.apiKind).toBe('MikroTikRouter')
  })
})
