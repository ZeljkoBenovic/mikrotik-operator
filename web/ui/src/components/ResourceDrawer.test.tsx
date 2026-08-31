import { fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { queryKeys } from '../api/client'
import { ResourceDrawer } from './ResourceDrawer'
import { jsonResponse, renderWithProviders } from '../test/render'
import { dnsKind, portForward, portForwardKind, routerKind, standaloneRouter } from '../test/fixtures'

function stubFetch(routers: ReturnType<typeof standaloneRouter>[] = []) {
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    if (url === '/api/config') {
      return jsonResponse({ namespace: 'mikrotik-operator-system' })
    }
    if (url.startsWith('/api/resources/mikrotikrouters') && (!init?.method || init.method === 'GET')) {
      return jsonResponse({ items: routers })
    }
    if (init?.method === 'POST') {
      return jsonResponse(JSON.parse(String(init.body ?? '{}')))
    }
    return jsonResponse({ items: [] })
  })
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

function stubFetchDeferredConfig(routers: ReturnType<typeof standaloneRouter>[] = []) {
  let resolveConfig: (value: Response) => void = () => {}
  const configPromise = new Promise<Response>((resolve) => {
    resolveConfig = resolve
  })
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url === '/api/config') {
        return configPromise
      }
      if (url.startsWith('/api/resources/mikrotikrouters')) {
        return jsonResponse({ items: routers })
      }
      return jsonResponse({ items: [] })
    }),
  )
  return {
    loadConfig(namespace = 'mikrotik-operator-system') {
      resolveConfig(jsonResponse({ namespace }))
    },
  }
}

function yamlSwitch() {
  const label = screen.getByText('YAML')
  const toggle = label.closest('.ant-space')?.querySelector('[role="switch"]')
  if (!toggle) {
    throw new Error('YAML switch not found')
  }
  return toggle
}

describe('ResourceDrawer', () => {
  it('creates resources in the operator namespace without a namespace picker', async () => {
    stubFetch()
    renderWithProviders(<ResourceDrawer kind={routerKind} open mode="create" onClose={() => {}} />)

    expect(await screen.findByText('Create Router')).toBeInTheDocument()
    expect(screen.queryByLabelText(/^namespace$/i)).not.toBeInTheDocument()
    await waitFor(() => {
      const hidden = document.querySelector('#namespace') as HTMLInputElement | null
      expect(hidden?.value).toBe('mikrotik-operator-system')
    })
  })

  it('sanitizes the resource name as the user types', async () => {
    stubFetch()
    const user = userEvent.setup()
    renderWithProviders(<ResourceDrawer kind={routerKind} open mode="create" onClose={() => {}} />)

    const name = await screen.findByLabelText(/^name$/i)
    await waitFor(() => {
      expect(name).toBeEnabled()
    })
    await user.type(name, 'My Port')
    expect(name).toHaveValue('my-port')
    expect(await screen.findByText(/Adjusted to a valid Kubernetes name/)).toBeInTheDocument()
  })

  it('offers routers in a dropdown and preselects the only one', async () => {
    stubFetch([
      standaloneRouter({
        metadata: { name: 'edge', namespace: 'mikrotik-operator-system' },
      }),
    ])
    renderWithProviders(
      <ResourceDrawer kind={portForwardKind} open mode="create" onClose={() => {}} />,
    )

    expect(await screen.findByText('Create Port Forward')).toBeInTheDocument()
    expect(screen.getByText('Router')).toBeInTheDocument()
    expect(screen.queryByText('Router ref')).not.toBeInTheDocument()
    expect(await screen.findByText('edge (mikrotik-operator-system)')).toBeInTheDocument()
    expect(screen.queryByPlaceholderText('MikroTikRouter name')).not.toBeInTheDocument()
  })

  it('shows an empty state when no routers exist', async () => {
    stubFetch([])
    renderWithProviders(
      <ResourceDrawer kind={portForwardKind} open mode="create" onClose={() => {}} />,
    )

    expect(await screen.findByText('Create Port Forward')).toBeInTheDocument()
    expect(await screen.findByText(/No MikroTikRouters found/)).toBeInTheDocument()
  })

  it('does not treat a failed router list as an empty cluster', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input)
        if (url === '/api/config') {
          return jsonResponse({ namespace: 'mikrotik-operator-system' })
        }
        if (url.startsWith('/api/resources/mikrotikrouters') && (!init?.method || init.method === 'GET')) {
          return jsonResponse({ message: 'routers unavailable' }, 500)
        }
        return jsonResponse({ items: [] })
      }),
    )
    renderWithProviders(
      <ResourceDrawer kind={portForwardKind} open mode="create" onClose={() => {}} />,
    )

    expect(await screen.findByText('Create Port Forward')).toBeInTheDocument()
    expect(await screen.findByText('routers unavailable')).toBeInTheDocument()
    expect(screen.queryByText(/No MikroTikRouters found/)).not.toBeInTheDocument()
    expect(screen.queryByText(/Create a Router first/)).not.toBeInTheDocument()
  })

  it('lists routers from other namespaces and stores a cross-namespace routerRef', async () => {
    const fetchMock = stubFetch([
      standaloneRouter({
        metadata: { name: 'edge', namespace: 'network' },
      }),
    ])
    const user = userEvent.setup()
    renderWithProviders(<ResourceDrawer kind={dnsKind} open mode="create" onClose={() => {}} />)

    expect(await screen.findByText('edge (network)')).toBeInTheDocument()
    expect(fetchMock.mock.calls.map(([url]) => String(url))).toContain('/api/resources/mikrotikrouters')
    expect(
      fetchMock.mock.calls.some(([url]) => String(url).includes('/api/resources/mikrotikrouters?')),
    ).toBe(false)

    const name = await screen.findByLabelText(/^name$/i)
    await waitFor(() => {
      expect(name).toBeEnabled()
    })
    await user.type(name, 'ui-dns')
    await user.type(screen.getByLabelText(/^dns name$/i), 'ui.home.arpa')
    await user.type(screen.getByLabelText(/^address$/i), '10.0.0.8')
    await user.click(screen.getByRole('button', { name: /create/i }))

    await waitFor(() => {
      const createCall = fetchMock.mock.calls.find(
        ([url, init]) =>
          String(url) === '/api/resources/mikrotikdnsrecords/mikrotik-operator-system' &&
          init?.method === 'POST',
      )
      expect(createCall).toBeTruthy()
      const body = JSON.parse(String(createCall?.[1]?.body))
      expect(body.metadata.namespace).toBe('mikrotik-operator-system')
      expect(body.spec.routerRef).toBe('network/edge')
    })
  })

  it('lists every live router, not only those in the operator namespace', async () => {
    stubFetch([
      standaloneRouter({
        metadata: { name: 'edge', namespace: 'network' },
      }),
      standaloneRouter({
        metadata: { name: 'core', namespace: 'mikrotik-operator-system' },
      }),
    ])
    const user = userEvent.setup()
    renderWithProviders(<ResourceDrawer kind={dnsKind} open mode="create" onClose={() => {}} />)

    await waitFor(
      () => {
        expect(screen.getByLabelText(/^name$/i)).toBeEnabled()
        const hidden = document.querySelector('#namespace') as HTMLInputElement | null
        expect(hidden?.value).toBe('mikrotik-operator-system')
      },
      { timeout: 5000 },
    )
    const picker = screen.getByRole('combobox')
    expect(picker).toBeEnabled()
    await user.click(picker)
    expect(await screen.findByText('edge (network)')).toBeInTheDocument()
    expect(screen.getByText('core (mikrotik-operator-system)')).toBeInTheDocument()
  })

  it('locks create fields and the YAML switch until operator config loads', async () => {
    const { loadConfig } = stubFetchDeferredConfig()
    renderWithProviders(
      <ResourceDrawer kind={routerKind} open mode="create" onClose={() => {}} />,
    )

    expect(await screen.findByText('Create Router')).toBeInTheDocument()
    expect(screen.getByLabelText(/^name$/i)).toBeDisabled()
    expect(yamlSwitch()).toBeDisabled()
    expect(screen.getByRole('button', { name: /create/i })).toBeDisabled()

    loadConfig()
    await waitFor(() => {
      expect(screen.getByLabelText(/^name$/i)).toBeEnabled()
    })
    expect(yamlSwitch()).toBeEnabled()
  })

  it('does not reset an in-progress edit when operator config arrives late', async () => {
    const { loadConfig } = stubFetchDeferredConfig()
    const user = userEvent.setup()
    const { queryClient } = renderWithProviders(
      <ResourceDrawer
        kind={routerKind}
        open
        mode="edit"
        resource={standaloneRouter()}
        onClose={() => {}}
      />,
    )

    const address = await screen.findByLabelText(/^address$/i)
    expect(address).toHaveValue('192.0.2.10')
    await user.clear(address)
    await user.type(address, '10.0.0.1')
    await user.click(yamlSwitch())
    expect(yamlSwitch()).toHaveAttribute('aria-checked', 'true')

    loadConfig()
    await waitFor(() => {
      expect(queryClient.getQueryData(queryKeys.config)).toBe('mikrotik-operator-system')
    })
    expect(yamlSwitch()).toHaveAttribute('aria-checked', 'true')

    await user.click(yamlSwitch())
    expect(screen.getByLabelText(/^address$/i)).toHaveValue('10.0.0.1')
  })

  it('keeps in-progress edits and saves the latest resourceVersion', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === 'PUT') {
        return jsonResponse({ metadata: { name: 'web', namespace: 'app', resourceVersion: '8' }, spec: {} })
      }
      if (String(input) === '/api/config') {
        return jsonResponse({ namespace: 'mikrotik-operator-system' })
      }
      return jsonResponse({ items: [] })
    })
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()
    const initial = portForward({ metadata: { name: 'web', namespace: 'app', resourceVersion: '1' } })
    const { rerender } = renderWithProviders(
      <ResourceDrawer kind={portForwardKind} open mode="edit" resource={initial} onClose={() => {}} />,
    )

    const targetAddress = await screen.findByLabelText(/^target address$/i)
    expect(targetAddress).toHaveValue('10.0.20.100')
    fireEvent.change(targetAddress, { target: { value: '10.0.99.99' } })
    expect(targetAddress).toHaveValue('10.0.99.99')

    rerender(
      <ResourceDrawer
        kind={portForwardKind}
        open
        mode="edit"
        resource={portForward({ metadata: { name: 'web', namespace: 'app', resourceVersion: '7' } })}
        onClose={() => {}}
      />,
    )
    expect(screen.getByLabelText(/^target address$/i)).toHaveValue('10.0.99.99')

    await user.click(screen.getByRole('button', { name: 'Save', hidden: true }))
    await waitFor(() => {
      const put = fetchMock.mock.calls.find((call) => (call[1] as RequestInit | undefined)?.method === 'PUT')
      expect(put).toBeTruthy()
      const body = JSON.parse(String((put?.[1] as RequestInit).body)) as {
        metadata: { resourceVersion?: string }
        spec: { targetAddress?: string }
      }
      expect(body.metadata.resourceVersion).toBe('7')
      expect(body.spec.targetAddress).toBe('10.0.99.99')
    })
  })
})
