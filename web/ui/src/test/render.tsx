import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { App as AntApp, ConfigProvider } from 'antd'
import { render, type RenderOptions } from '@testing-library/react'
import type { ReactElement, ReactNode } from 'react'
import { MemoryRouter, Outlet, Route, Routes, useLocation } from 'react-router-dom'
import type { OutletContext } from '../layout/AppLayout'

export function createTestQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0 },
      mutations: { retry: false },
    },
  })
}

type Options = Omit<RenderOptions, 'wrapper'> & {
  route?: string
  path?: string
  namespaceFilter?: string
}

function Shell({ namespaceFilter }: { namespaceFilter?: string }) {
  const location = useLocation()
  const fromSearch = new URLSearchParams(location.search).get('namespace') || undefined
  return <Outlet context={{ namespaceFilter: fromSearch ?? namespaceFilter } satisfies OutletContext} />
}

export function renderWithProviders(ui: ReactElement, options: Options = {}) {
  const { route = '/', path = '/', namespaceFilter, ...renderOptions } = options
  const queryClient = createTestQueryClient()

  function Wrapper({ children }: { children: ReactNode }) {
    return (
      <ConfigProvider>
        <AntApp>
          <QueryClientProvider client={queryClient}>
            <MemoryRouter initialEntries={[route]}>
              <Routes>
                <Route element={<Shell namespaceFilter={namespaceFilter} />}>
                  <Route path={path} element={children} />
                </Route>
              </Routes>
            </MemoryRouter>
          </QueryClientProvider>
        </AntApp>
      </ConfigProvider>
    )
  }

  return { ...render(ui, { wrapper: Wrapper, ...renderOptions }), queryClient }
}

export function jsonResponse(body: unknown, status = 200): Response {
  const text = status === 204 ? '' : JSON.stringify(body)
  return new Response(text, {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}
