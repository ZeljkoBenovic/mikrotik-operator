import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { queryKeys } from '../api/client'
import { ResourceDrawer } from './ResourceDrawer'
import { jsonResponse, renderWithProviders } from '../test/render'
import { portForwardKind, routerKind, standaloneRouter } from '../test/fixtures'

function stubFetch(routers: ReturnType<typeof standaloneRouter>[] = []) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url === '/api/config') {
        return jsonResponse({ namespace: 'mikrotik-operator-system' })
      }
      if (url.startsWith('/api/resources/mikrotikrouters')) {
        return jsonResponse({ items: routers })
      }
      return jsonResponse({ items: [] })
    }),
  )
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

  it('locks create fields and the YAML switch until operator config loads', async () => {
    const { loadConfig } = stubFetchDeferredConfig()
    const { queryClient } = renderWithProviders(
      <ResourceDrawer kind={routerKind} open mode="create" onClose={() => {}} />,
    )

    expect(await screen.findByText('Create Router')).toBeInTheDocument()
    expect(screen.getByLabelText(/^name$/i)).toBeDisabled()
    expect(yamlSwitch()).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Create' })).toBeDisabled()

    loadConfig()
    await waitFor(() => {
      expect(queryClient.getQueryData(queryKeys.config)).toBe('mikrotik-operator-system')
    })
    expect(screen.getByLabelText(/^name$/i)).toBeEnabled()
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
})
