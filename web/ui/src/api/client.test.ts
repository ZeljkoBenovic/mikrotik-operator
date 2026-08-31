import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, ApiError, normalizeOverview, queryKeys } from './client'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('normalizeOverview', () => {
  it('reads the backend kinds array', () => {
    expect(
      normalizeOverview({
        kinds: [
          { kind: 'mikrotikrouters', count: 4, notReady: 1 },
          { kind: 'mikrotikroutes', count: 0, notReady: 0 },
        ],
      }),
    ).toEqual({
      mikrotikrouters: { total: 4, ready: 3, notReady: 1 },
      mikrotikroutes: { total: 0, ready: 0, notReady: 0 },
    })
  })

  it('accepts map-shaped counts and ignores junk', () => {
    expect(normalizeOverview({ counts: { mikrotikrouters: { total: 2, notReady: 2 } } })).toEqual({
      mikrotikrouters: { total: 2, ready: 0, notReady: 2 },
    })
    expect(normalizeOverview(null)).toEqual({})
    expect(normalizeOverview({ kinds: [{ notReady: 1 }] })).toEqual({})
  })
})

describe('api client', () => {
  it('lists namespaces and secret names without leaking objects', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url === '/api/namespaces') {
        return new Response(JSON.stringify({ items: [{ name: 'other' }, { name: 'app' }] }), { status: 200 })
      }
      if (url === '/api/secrets/app') {
        return new Response(JSON.stringify({ items: [{ name: 'b' }, { name: 'a' }] }), { status: 200 })
      }
      throw new Error(url)
    })
    vi.stubGlobal('fetch', fetchMock)
    await expect(api.namespaces()).resolves.toEqual(['app', 'other'])
    await expect(api.secrets('app')).resolves.toEqual(['a', 'b'])
    expect(String(fetchMock.mock.calls[1]?.[0])).toBe('/api/secrets/app')
  })

  it('reads the operator namespace from config', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        expect(String(input)).toBe('/api/config')
        return new Response(JSON.stringify({ namespace: 'mikrotik-operator-system' }), { status: 200 })
      }),
    )
    await expect(api.config()).resolves.toBe('mikrotik-operator-system')
  })

  it('creates, updates, and deletes resources', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (init?.method === 'POST') {
        expect(url).toBe('/api/resources/mikrotikrouters/app')
        expect(init.headers).toMatchObject({ 'Content-Type': 'application/json' })
        return new Response(JSON.stringify({ metadata: { name: 'edge', namespace: 'app' }, spec: {} }), {
          status: 201,
        })
      }
      if (init?.method === 'PUT') {
        expect(url).toBe('/api/resources/mikrotikrouters/app/edge')
        return new Response(JSON.stringify({ metadata: { name: 'edge' }, spec: { address: '192.0.2.11' } }), {
          status: 200,
        })
      }
      if (init?.method === 'DELETE') {
        return new Response(null, { status: 204 })
      }
      throw new Error(`${init?.method} ${url}`)
    })
    vi.stubGlobal('fetch', fetchMock)
    const created = await api.createResource('mikrotikrouters', 'app', {
      metadata: { name: 'edge' },
      spec: { address: '192.0.2.10' },
    })
    expect(created.metadata.namespace).toBe('app')
    const updated = await api.updateResource('mikrotikrouters', 'app', 'edge', {
      metadata: { name: 'edge' },
      spec: { address: '192.0.2.11' },
    })
    expect(updated.spec.address).toBe('192.0.2.11')
    await api.deleteResource('mikrotikrouters', 'app', 'edge')
  })

  it('raises ApiError from JSON error bodies including 409', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        return new Response(
          JSON.stringify({
            error: 'resource is owned by Service/frontend in namespace app',
            managedBy: { kind: 'Service', name: 'frontend' },
          }),
          { status: 409 },
        )
      }),
    )
    await expect(api.deleteResource('mikrotikdnsrecords', 'app', 'web')).rejects.toMatchObject({
      name: 'ApiError',
      status: 409,
      message: 'resource is owned by Service/frontend in namespace app',
    } satisfies Partial<ApiError>)
  })

  it('falls back when the error body is not JSON', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response('nope', { status: 500, statusText: 'Internal Server Error' })),
    )
    await expect(api.overview()).rejects.toMatchObject({ status: 500, message: '500 Internal Server Error' })
  })

  it('encodes namespace and name in resource URLs', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ items: [] }), { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    await api.listResources('mikrotikrouters', 'kube-system')
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe('/api/resources/mikrotikrouters?namespace=kube-system')
    fetchMock.mockImplementationOnce(async () => new Response('{}', { status: 200 }))
    await api.getResource('mikrotikrouters', 'app ns', 'edge/x')
    expect(String(fetchMock.mock.calls[1]?.[0])).toBe('/api/resources/mikrotikrouters/app%20ns/edge%2Fx')
  })
})

describe('queryKeys', () => {
  it('distinguishes all-namespaces from a namespace filter', () => {
    expect(queryKeys.resources('mikrotikrouters')).toEqual(['resources', 'mikrotikrouters', 'all'])
    expect(queryKeys.resources('mikrotikrouters', 'app')).toEqual(['resources', 'mikrotikrouters', 'app'])
  })
})
