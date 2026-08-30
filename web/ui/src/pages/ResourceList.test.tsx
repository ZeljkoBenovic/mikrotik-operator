import { screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { ResourceList } from '../pages/ResourceList'
import { jsonResponse, renderWithProviders } from '../test/render'
import { dnsKind, ownedDNS, routerKind, standaloneRouter } from '../test/fixtures'

describe('ResourceList', () => {
  it('disables edit and delete for owned resources', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url.startsWith('/api/resources/mikrotikdnsrecords')) {
          return jsonResponse({ items: [ownedDNS()] })
        }
        if (url === '/api/namespaces') {
          return jsonResponse({ items: [{ name: 'app' }] })
        }
        return jsonResponse({ items: [] })
      }),
    )
    renderWithProviders(<ResourceList kind={dnsKind} />, { route: '/dns-records', path: '/dns-records' })
    expect(await screen.findByText('web')).toBeInTheDocument()
    const locked = screen.getAllByTitle('Managed resources are read-only')
    expect(locked).toHaveLength(2)
    for (const button of locked) {
      expect(button).toBeDisabled()
    }
  })

  it('keeps edit and delete enabled for standalone resources', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url.startsWith('/api/resources/mikrotikrouters')) {
          return jsonResponse({ items: [standaloneRouter()] })
        }
        if (url === '/api/namespaces') {
          return jsonResponse({ items: [{ name: 'app' }] })
        }
        return jsonResponse({ items: [] })
      }),
    )
    renderWithProviders(<ResourceList kind={routerKind} />, { route: '/routers', path: '/routers' })
    expect(await screen.findByText('edge')).toBeInTheDocument()
    expect(screen.getByTitle('Edit')).toBeEnabled()
    expect(screen.getByTitle('Delete')).toBeEnabled()
  })
})
