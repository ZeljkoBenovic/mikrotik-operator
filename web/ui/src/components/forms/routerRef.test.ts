import { describe, expect, it } from 'vitest'
import { standaloneRouter } from '../../test/fixtures'
import { liveRouterRefOptions } from './routerRef'

describe('liveRouterRefOptions', () => {
  it('includes routers from other namespaces as namespace/name refs', () => {
    const options = liveRouterRefOptions(
      [
        standaloneRouter({
          metadata: { name: 'edge', namespace: 'network' },
        }),
      ],
      'mikrotik-operator-system',
    )

    expect(options).toEqual([{ value: 'network/edge', label: 'edge (network)' }])
  })

  it('uses a bare name when the router is in the resource namespace', () => {
    const options = liveRouterRefOptions(
      [
        standaloneRouter({
          metadata: { name: 'edge', namespace: 'mikrotik-operator-system' },
        }),
      ],
      'mikrotik-operator-system',
    )

    expect(options).toEqual([
      { value: 'edge', label: 'edge (mikrotik-operator-system)' },
    ])
  })

  it('lists every live router instead of only the resource namespace', () => {
    const options = liveRouterRefOptions(
      [
        standaloneRouter({
          metadata: { name: 'edge', namespace: 'network' },
        }),
        standaloneRouter({
          metadata: { name: 'core', namespace: 'mikrotik-operator-system' },
        }),
        standaloneRouter({
          metadata: { name: 'gone', namespace: 'network', deletionTimestamp: '2026-01-01T00:00:00Z' },
        }),
      ],
      'mikrotik-operator-system',
    )

    expect(options.map((option) => option.value)).toEqual(['core', 'network/edge'])
  })
})
