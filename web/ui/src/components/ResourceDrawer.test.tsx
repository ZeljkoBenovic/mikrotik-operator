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

function stubClusterLists(
  options: {
    namespaces?: string[]
    services?: Record<string, string[]>
    pods?: Record<string, string[]>
    routers?: ReturnType<typeof standaloneRouter>[]
  } = {},
) {
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    if (url === '/api/config') {
      return jsonResponse({ namespace: 'mikrotik-operator-system' })
    }
    if (url === '/api/namespaces') {
      return jsonResponse({ items: (options.namespaces ?? ['app', 'kube-system']).map((name) => ({ name })) })
    }
    const servicesMatch = /^\/api\/services\/([^/?]+)$/.exec(url)
    if (servicesMatch) {
      const namespace = decodeURIComponent(servicesMatch[1])
      return jsonResponse({ items: (options.services?.[namespace] ?? []).map((name) => ({ name })) })
    }
    const podsMatch = /^\/api\/pods\/([^/?]+)$/.exec(url)
    if (podsMatch) {
      const namespace = decodeURIComponent(podsMatch[1])
      return jsonResponse({ items: (options.pods?.[namespace] ?? []).map((name) => ({ name })) })
    }
    if (url.startsWith('/api/resources/mikrotikrouters') && (!init?.method || init.method === 'GET')) {
      return jsonResponse({ items: options.routers ?? [] })
    }
    if (init?.method === 'POST') {
      return jsonResponse(JSON.parse(String(init.body ?? '{}')))
    }
    return jsonResponse({ items: [] })
  })
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

async function chooseSelectOption(user: ReturnType<typeof userEvent.setup>, label: RegExp, option: string) {
  const combobox = screen.getByRole('combobox', { name: label })
  await user.click(combobox)
  await user.click(await screen.findByText(option, { selector: '.ant-select-item-option-content' }))
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
  it('stores the selected router credentials secret in the form', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url === '/api/config') {
        return jsonResponse({ namespace: 'mikrotik-operator-system' })
      }
      if (url === '/api/secrets/mikrotik-operator-system') {
        return jsonResponse({ items: ['router-local'] })
      }
      if (init?.method === 'POST') {
        return jsonResponse(JSON.parse(String(init.body ?? '{}')))
      }
      return jsonResponse({ items: [] })
    })
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()
    renderWithProviders(<ResourceDrawer kind={routerKind} open mode="create" onClose={() => {}} />)

    const name = await screen.findByLabelText(/^name$/i)
    await waitFor(() => expect(name).toBeEnabled())
    await user.type(name, 'edge')
    await user.type(screen.getByLabelText(/^address$/i), '10.0.20.254')
    const credentials = screen.getByRole('combobox')
    await user.click(credentials)
    await user.click(await screen.findByText('router-local', { selector: '.ant-select-item-option-content' }))
    await user.click(screen.getByRole('button', { name: /create/i }))

    await waitFor(() => {
      const createCall = fetchMock.mock.calls.find(
        ([url, request]) => String(url).includes('/api/resources/mikrotikrouters/') && request?.method === 'POST',
      )
      expect(createCall).toBeTruthy()
      const body = JSON.parse(String(createCall?.[1]?.body))
      expect(body.spec.credentialsSecret).toEqual({ name: 'router-local' })
    })
    expect(screen.queryByText('Credentials secret is required')).not.toBeInTheDocument()
  })

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
    const picker = screen.getByRole('combobox', { name: /router/i })
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

  it('unlocks create when operator config fails instead of spinning forever', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url === '/api/config') {
          return jsonResponse({ message: 'config unavailable' }, 500)
        }
        if (url.startsWith('/api/resources/mikrotikrouters')) {
          return jsonResponse({ items: [] })
        }
        return jsonResponse({ items: [] })
      }),
    )
    renderWithProviders(
      <ResourceDrawer kind={routerKind} open mode="create" onClose={() => {}} />,
    )

    expect(await screen.findByText('Create Router')).toBeInTheDocument()
    const create = await screen.findByRole('button', { name: /create/i })
    await waitFor(() => {
      expect(screen.getByLabelText(/^name$/i)).toBeEnabled()
      expect(create).not.toHaveClass('ant-btn-loading')
      expect(create).toBeEnabled()
    })
  })

  it('updates the create namespace when operator config recovers after a failure', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url === '/api/config') {
        return jsonResponse({ message: 'config unavailable' }, 500)
      }
      if (url.startsWith('/api/resources/mikrotikrouters')) {
        return jsonResponse({ items: [] })
      }
      return jsonResponse({ items: [] })
    })
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()
    const { queryClient } = renderWithProviders(
      <ResourceDrawer kind={routerKind} open mode="create" onClose={() => {}} />,
    )

    const name = await screen.findByLabelText(/^name$/i)
    await waitFor(() => {
      expect(name).toBeEnabled()
      const hidden = document.querySelector('#namespace') as HTMLInputElement | null
      expect(hidden?.value).toBe('default')
    })
    await user.type(name, 'edge-router')
    expect(name).toHaveValue('edge-router')

    queryClient.setQueryData(queryKeys.config, 'mikrotik-operator-system')

    await waitFor(() => {
      const hidden = document.querySelector('#namespace') as HTMLInputElement | null
      expect(hidden?.value).toBe('mikrotik-operator-system')
    })
    expect(screen.getByLabelText(/^name$/i)).toHaveValue('edge-router')
    await waitFor(() => {
      expect(fetchMock.mock.calls.map(([url]) => String(url))).toContain(
        '/api/secrets/mikrotik-operator-system',
      )
    })
  })

  it('clears the name error after blur finalizes a trailing separator', async () => {
    stubFetch()
    const user = userEvent.setup()
    renderWithProviders(<ResourceDrawer kind={routerKind} open mode="create" onClose={() => {}} />)

    const name = await screen.findByLabelText(/^name$/i)
    await waitFor(() => {
      expect(name).toBeEnabled()
    })
    await user.type(name, 'web-')
    expect(name).toHaveValue('web-')
    expect(
      await screen.findByText(/start and end with a letter or number/),
    ).toBeInTheDocument()

    await user.tab()
    await waitFor(() => {
      expect(name).toHaveValue('web')
      expect(screen.queryByText(/start and end with a letter or number/)).not.toBeInTheDocument()
    })
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

  it('picks a service from searchable namespace and name dropdowns', async () => {
    const fetchMock = stubClusterLists({
      routers: [standaloneRouter({ metadata: { name: 'edge', namespace: 'mikrotik-operator-system' } })],
      services: { app: ['api', 'web'], 'kube-system': ['kube-dns'] },
    })
    const user = userEvent.setup()
    renderWithProviders(<ResourceDrawer kind={dnsKind} open mode="create" onClose={() => {}} />)

    const name = await screen.findByLabelText(/^name$/i)
    await waitFor(() => {
      expect(name).toBeEnabled()
    })
    expect(screen.getByRole('combobox', { name: /^service name$/i })).toBeDisabled()

    await user.type(name, 'ui-dns')
    await user.type(screen.getByLabelText(/^dns name$/i), 'ui.home.arpa')
    await user.type(screen.getByLabelText(/^address$/i), '10.0.0.8')
    await chooseSelectOption(user, /^service namespace$/i, 'app')
    expect(fetchMock.mock.calls.map(([url]) => String(url))).toContain('/api/services/app')
    await chooseSelectOption(user, /^service name$/i, 'web')
    await user.click(screen.getByRole('button', { name: /create/i }))

    await waitFor(() => {
      const createCall = fetchMock.mock.calls.find(
        ([url, init]) =>
          String(url) === '/api/resources/mikrotikdnsrecords/mikrotik-operator-system' &&
          init?.method === 'POST',
      )
      expect(createCall).toBeTruthy()
      const body = JSON.parse(String(createCall?.[1]?.body))
      expect(body.spec.serviceRef).toEqual({ namespace: 'app', name: 'web' })
    })
  })

  it('clears the service name when the namespace changes', async () => {
    const fetchMock = stubClusterLists({
      services: { app: ['web'], 'kube-system': ['kube-dns'] },
    })
    const user = userEvent.setup()
    renderWithProviders(<ResourceDrawer kind={dnsKind} open mode="create" onClose={() => {}} />)

    const name = await screen.findByLabelText(/^name$/i)
    await waitFor(() => {
      expect(name).toBeEnabled()
    })
    await user.type(name, 'ui-dns')
    await user.type(screen.getByLabelText(/^dns name$/i), 'ui.home.arpa')
    await user.type(screen.getByLabelText(/^address$/i), '10.0.0.8')
    await chooseSelectOption(user, /^service namespace$/i, 'app')
    await waitFor(() => {
      expect(screen.getByRole('combobox', { name: /^service name$/i })).toBeEnabled()
    })
    await chooseSelectOption(user, /^service name$/i, 'web')
    await chooseSelectOption(user, /^service namespace$/i, 'kube-system')
    await user.click(screen.getByRole('button', { name: /create/i }))

    await waitFor(() => {
      const createCall = fetchMock.mock.calls.find(
        ([url, init]) =>
          String(url) === '/api/resources/mikrotikdnsrecords/mikrotik-operator-system' &&
          init?.method === 'POST',
      )
      expect(createCall).toBeTruthy()
      const body = JSON.parse(String(createCall?.[1]?.body))
      expect(body.spec.serviceRef).toEqual({ namespace: 'kube-system' })
    })
  })

  it('filters namespace options as the user types', async () => {
    stubClusterLists({ namespaces: ['app', 'kube-system', 'monitoring'] })
    const user = userEvent.setup()
    renderWithProviders(<ResourceDrawer kind={dnsKind} open mode="create" onClose={() => {}} />)

    await waitFor(() => {
      expect(screen.getByLabelText(/^name$/i)).toBeEnabled()
    })
    const namespace = screen.getByRole('combobox', { name: /^service namespace$/i })
    await user.click(namespace)
    await user.type(namespace, 'kube')
    expect(await screen.findByText('kube-system', { selector: '.ant-select-item-option-content' })).toBeInTheDocument()
    expect(screen.queryByText('app', { selector: '.ant-select-item-option-content' })).not.toBeInTheDocument()
    expect(screen.queryByText('monitoring', { selector: '.ant-select-item-option-content' })).not.toBeInTheDocument()
  })

  it('allows a custom service name that is not yet listed', async () => {
    const fetchMock = stubClusterLists({
      services: { app: ['web'] },
    })
    const user = userEvent.setup()
    renderWithProviders(<ResourceDrawer kind={dnsKind} open mode="create" onClose={() => {}} />)

    const name = await screen.findByLabelText(/^name$/i)
    await waitFor(() => {
      expect(name).toBeEnabled()
    })
    await user.type(name, 'future-dns')
    await user.type(screen.getByLabelText(/^dns name$/i), 'future.home.arpa')
    await user.type(screen.getByLabelText(/^address$/i), '10.0.0.9')
    await chooseSelectOption(user, /^service namespace$/i, 'app')
    const serviceName = screen.getByRole('combobox', { name: /^service name$/i })
    await user.click(serviceName)
    await user.type(serviceName, 'future-web')
    await user.click(await screen.findByText('Use "future-web"', { selector: '.ant-select-item-option-content' }))
    await user.click(screen.getByRole('button', { name: /create/i }))

    await waitFor(() => {
      const createCall = fetchMock.mock.calls.find(
        ([url, init]) =>
          String(url) === '/api/resources/mikrotikdnsrecords/mikrotik-operator-system' &&
          init?.method === 'POST',
      )
      expect(createCall).toBeTruthy()
      const body = JSON.parse(String(createCall?.[1]?.body))
      expect(body.spec.serviceRef).toEqual({ namespace: 'app', name: 'future-web' })
    })
  })

  it('lists pods after a namespace is chosen on a port forward', async () => {
    const fetchMock = stubClusterLists({
      routers: [standaloneRouter({ metadata: { name: 'edge', namespace: 'mikrotik-operator-system' } })],
      pods: { app: ['web-0', 'web-1'] },
    })
    const user = userEvent.setup()
    renderWithProviders(
      <ResourceDrawer kind={portForwardKind} open mode="create" onClose={() => {}} />,
    )

    const name = await screen.findByLabelText(/^name$/i)
    await waitFor(() => {
      expect(name).toBeEnabled()
    })
    await user.click(screen.getByText('Pod'))
    expect(screen.getByRole('combobox', { name: /^pod name$/i })).toBeDisabled()
    await chooseSelectOption(user, /^pod namespace$/i, 'app')
    expect(fetchMock.mock.calls.map(([url]) => String(url))).toContain('/api/pods/app')
    await user.click(screen.getByRole('combobox', { name: /^pod name$/i }))
    expect(await screen.findByText('web-0', { selector: '.ant-select-item-option-content' })).toBeInTheDocument()
    expect(screen.getByText('web-1', { selector: '.ant-select-item-option-content' })).toBeInTheDocument()
  })

  it('keeps service and pod name fields usable when list APIs fail', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url === '/api/config') {
          return jsonResponse({ namespace: 'mikrotik-operator-system' })
        }
        if (url === '/api/namespaces') {
          return jsonResponse({ message: 'namespaces unavailable' }, 500)
        }
        if (url.startsWith('/api/resources/mikrotikrouters')) {
          return jsonResponse({ items: [] })
        }
        return jsonResponse({ items: [] })
      }),
    )
    renderWithProviders(<ResourceDrawer kind={dnsKind} open mode="create" onClose={() => {}} />)

    await waitFor(() => {
      expect(screen.getByLabelText(/^name$/i)).toBeEnabled()
    })
    expect(await screen.findByText('namespaces unavailable')).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: /^service namespace$/i })).toBeEnabled()
  })
})
