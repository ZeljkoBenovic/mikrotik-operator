import { screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { ResourceDrawer } from './ResourceDrawer'
import { jsonResponse, renderWithProviders } from '../test/render'
import { routerKind } from '../test/fixtures'

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
})
