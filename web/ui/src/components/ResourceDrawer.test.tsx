import { fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { ResourceDrawer } from './ResourceDrawer'
import { jsonResponse, renderWithProviders } from '../test/render'
import { KINDS } from '../kinds'
import { portForward, routerKind } from '../test/fixtures'

const portForwardKind = KINDS[3]

describe('ResourceDrawer', () => {
  it('creates resources in the operator namespace without a namespace picker', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url === '/api/config') {
          return jsonResponse({ namespace: 'mikrotik-operator-system' })
        }
        return jsonResponse({ items: [] })
      }),
    )
    renderWithProviders(<ResourceDrawer kind={routerKind} open mode="create" onClose={() => {}} />)

    expect(await screen.findByText('Create Router')).toBeInTheDocument()
    expect(screen.queryByLabelText(/^namespace$/i)).not.toBeInTheDocument()
    await waitFor(() => {
      const hidden = document.querySelector('#namespace') as HTMLInputElement | null
      expect(hidden?.value).toBe('mikrotik-operator-system')
    })
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
