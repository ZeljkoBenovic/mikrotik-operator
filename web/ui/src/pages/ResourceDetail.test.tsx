import { fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { ResourceDetail } from './ResourceDetail'
import { jsonResponse, renderWithProviders } from '../test/render'
import { KINDS } from '../kinds'
import { dnsKind, ownedDNS, portForward } from '../test/fixtures'

const portForwardKind = KINDS[3]

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

  it('refreshes Ready status without a page reload', async () => {
    let served = 0
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url.includes('/api/resources/mikrotikportforwards/app/web')) {
          served += 1
          if (served === 1) {
            return jsonResponse(portForward())
          }
          return jsonResponse(
            portForward({
              status: {
                applied: true,
                conditions: [{ type: 'Ready', status: 'True', reason: 'Applied' }],
              },
            }),
          )
        }
        return jsonResponse({})
      }),
    )
    renderWithProviders(<ResourceDetail kind={portForwardKind} />, {
      route: '/port-forwards/app/web',
      path: '/port-forwards/:namespace/:name',
    })
    expect(await screen.findByText('NotReady')).toBeInTheDocument()
    await waitFor(
      () => {
        expect(screen.queryByText('NotReady')).not.toBeInTheDocument()
      },
      { timeout: 5000 },
    )
    expect(document.querySelector('.ant-badge-status-success')).toBeTruthy()
  })

  it('keeps in-progress edits while status polling updates the resource', async () => {
    let served = 0
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url.includes('/api/resources/mikrotikportforwards/app/web')) {
          served += 1
          return jsonResponse(
            portForward({
              metadata: { name: 'web', namespace: 'app', resourceVersion: String(served) },
              status: {
                applied: false,
                conditions: [
                  { type: 'Ready', status: 'False', reason: 'Pending', message: `attempt-${served}` },
                ],
              },
            }),
          )
        }
        if (url === '/api/config') {
          return jsonResponse({ namespace: 'mikrotik-operator-system' })
        }
        return jsonResponse({ items: [] })
      }),
    )
    const user = userEvent.setup()
    renderWithProviders(<ResourceDetail kind={portForwardKind} />, {
      route: '/port-forwards/app/web',
      path: '/port-forwards/:namespace/:name',
    })
    expect(await screen.findByText('NotReady')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /edit/i }))
    const targetAddress = await screen.findByLabelText(/^target address$/i)
    expect(targetAddress).toHaveValue('10.0.20.100')
    fireEvent.change(targetAddress, { target: { value: '10.0.99.99' } })
    expect(targetAddress).toHaveValue('10.0.99.99')
    const servedAfterEdit = served
    await waitFor(
      () => {
        expect(served).toBeGreaterThan(servedAfterEdit)
      },
      { timeout: 5000 },
    )
    expect(screen.getByLabelText(/^target address$/i)).toHaveValue('10.0.99.99')
  })

  it('saves with the latest resourceVersion after status polling', async () => {
    let served = 0
    let freezeVersion = false
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (init?.method === 'PUT') {
        return jsonResponse({ metadata: { name: 'web', namespace: 'app', resourceVersion: '9' }, spec: {} })
      }
      if (url.includes('/api/resources/mikrotikportforwards/app/web')) {
        if (!freezeVersion) {
          served += 1
        }
        return jsonResponse(
          portForward({
            metadata: { name: 'web', namespace: 'app', resourceVersion: String(served) },
            status: {
              applied: false,
              conditions: [{ type: 'Ready', status: 'False', reason: 'Pending' }],
            },
          }),
        )
      }
      if (url === '/api/config') {
        return jsonResponse({ namespace: 'mikrotik-operator-system' })
      }
      return jsonResponse({ items: [] })
    })
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()
    const { queryClient } = renderWithProviders(<ResourceDetail kind={portForwardKind} />, {
      route: '/port-forwards/app/web',
      path: '/port-forwards/:namespace/:name',
    })
    expect(await screen.findByText('NotReady')).toBeInTheDocument()
    const versionAtEdit = served
    await user.click(screen.getByRole('button', { name: /edit/i }))
    expect(await screen.findByLabelText(/^target address$/i)).toHaveValue('10.0.20.100')
    await waitFor(
      () => {
        expect(served).toBeGreaterThan(versionAtEdit)
      },
      { timeout: 5000 },
    )
    freezeVersion = true
    await queryClient.refetchQueries()
    const liveVersion = String(served)
    await user.click(screen.getByRole('button', { name: 'Save', hidden: true }))
    await waitFor(() => {
      const put = fetchMock.mock.calls.find((call) => (call[1] as RequestInit | undefined)?.method === 'PUT')
      expect(put).toBeTruthy()
      const body = JSON.parse(String((put?.[1] as RequestInit).body)) as {
        metadata: { resourceVersion?: string }
      }
      expect(body.metadata.resourceVersion).toBe(liveVersion)
      expect(body.metadata.resourceVersion).not.toBe(String(versionAtEdit))
    })
  })
})
