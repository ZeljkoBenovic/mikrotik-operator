import { screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { Dashboard } from './Dashboard'
import { jsonResponse, renderWithProviders } from '../test/render'
import { notReadyRoute, standaloneRouter } from '../test/fixtures'

describe('Dashboard', () => {
  it('renders per-kind counts and not-ready rows', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url === '/api/overview') {
          return jsonResponse({
            kinds: [
              { kind: 'mikrotikrouters', count: 1, notReady: 0 },
              { kind: 'mikrotikdnsrecords', count: 0, notReady: 0 },
              { kind: 'mikrotikroutes', count: 1, notReady: 1 },
              { kind: 'mikrotikportforwards', count: 0, notReady: 0 },
              { kind: 'mikrotikfirewallrules', count: 0, notReady: 0 },
            ],
          })
        }
        if (url.startsWith('/api/resources/mikrotikrouters')) {
          return jsonResponse({ items: [standaloneRouter()] })
        }
        if (url.startsWith('/api/resources/mikrotikroutes')) {
          return jsonResponse({ items: [notReadyRoute()] })
        }
        if (url.startsWith('/api/resources/')) {
          return jsonResponse({ items: [] })
        }
        return jsonResponse({ items: [] })
      }),
    )
    renderWithProviders(<Dashboard />)
    expect(await screen.findByText('Routers')).toBeInTheDocument()
    expect(screen.getByText('Routes')).toBeInTheDocument()
    expect(await screen.findByText('default')).toBeInTheDocument()
    expect(screen.getByText(/no authentication/i)).toBeInTheDocument()
  })
})
