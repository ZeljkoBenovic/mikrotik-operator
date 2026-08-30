import { screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { ResourceDetail } from './ResourceDetail'
import { jsonResponse, renderWithProviders } from '../test/render'
import { dnsKind, ownedDNS } from '../test/fixtures'

describe('ResourceDetail', () => {
  it('locks owned resources and shows the managed banner', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url.includes('/api/resources/mikrotikdnsrecords/app/web')) {
          return jsonResponse(ownedDNS())
        }
        if (url === '/api/namespaces') {
          return jsonResponse({ items: [{ name: 'app' }] })
        }
        return jsonResponse({})
      }),
    )
    renderWithProviders(<ResourceDetail kind={dnsKind} />, {
      route: '/dns-records/app/web',
      path: '/dns-records/:namespace/:name',
    })
    expect(await screen.findByText('Managed by Service/app/frontend')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /edit/i })).toBeDisabled()
    expect(screen.getByRole('button', { name: /delete/i })).toBeDisabled()
  })
})
