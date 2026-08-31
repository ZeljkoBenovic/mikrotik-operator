import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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

  it('reveals a create in the operator namespace when another namespace is filtered', async () => {
    const created = {
      apiVersion: 'mikrotik.operator.io/v1alpha1',
      kind: 'MikroTikDNSRecord',
      metadata: { name: 'ui-dns', namespace: 'mikrotik-operator-system' },
      spec: { name: 'ui.home.arpa', address: '10.0.0.8' },
    }
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input)
        if (url === '/api/config') {
          return jsonResponse({ namespace: 'mikrotik-operator-system' })
        }
        if (url === '/api/namespaces') {
          return jsonResponse({ items: [{ name: 'app' }, { name: 'mikrotik-operator-system' }] })
        }
        if (url.startsWith('/api/resources/mikrotikrouters')) {
          return jsonResponse({ items: [] })
        }
        if (init?.method === 'POST' && url === '/api/resources/mikrotikdnsrecords/mikrotik-operator-system') {
          return jsonResponse(created)
        }
        if (url.startsWith('/api/resources/mikrotikdnsrecords')) {
          const namespace = new URL(url, 'http://ui.local').searchParams.get('namespace')
          if (namespace === 'mikrotik-operator-system') {
            return jsonResponse({ items: [created] })
          }
          return jsonResponse({ items: [] })
        }
        return jsonResponse({ items: [] })
      }),
    )
    const user = userEvent.setup()
    renderWithProviders(<ResourceList kind={dnsKind} />, {
      route: '/dns-records?namespace=app',
      path: '/dns-records',
    })

    expect(await screen.findByText('DNS Records')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /plus create/i }))
    const drawer = await screen.findByRole('dialog')
    const name = await within(drawer).findByLabelText(/^name$/i)
    await waitFor(() => {
      expect(name).toBeEnabled()
    })
    await user.type(name, 'ui-dns')
    await user.type(within(drawer).getByLabelText(/^dns name$/i), 'ui.home.arpa')
    await user.type(within(drawer).getByLabelText(/^address$/i), '10.0.0.8')
    await user.click(within(drawer).getByRole('button', { name: 'Create' }))

    expect(await screen.findByText('DNS Record created in mikrotik-operator-system')).toBeInTheDocument()
    expect(await screen.findByRole('link', { name: 'ui-dns' })).toBeInTheDocument()
  })
})
