import { describe, expect, it } from 'vitest'
import { KINDS, kindFromPath } from './kinds'

describe('kind registry', () => {
  it('covers the CRDs with stable slugs and routes', () => {
    expect(KINDS.map((kind) => kind.slug)).toEqual([
      'mikrotikrouters',
      'mikrotikdnsrecords',
      'mikrotikroutes',
      'mikrotikportforwards',
      'mikrotikfirewallrules',
      'mikrotikbackups',
      'mikrotikrestores',
    ])
    expect(KINDS.map((kind) => kind.path)).toEqual([
      '/routers',
      '/dns-records',
      '/routes',
      '/port-forwards',
      '/firewall-rules',
      '/backups',
      '/restores',
    ])
  })

  it('resolves list and detail paths', () => {
    expect(kindFromPath('/port-forwards')?.apiKind).toBe('MikroTikPortForward')
    expect(kindFromPath('/port-forwards/app/https')?.slug).toBe('mikrotikportforwards')
    expect(kindFromPath('/nope')).toBeUndefined()
    expect(kindFromPath('/')).toBeUndefined()
  })
})
