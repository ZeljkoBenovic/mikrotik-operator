import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { ConditionsTable } from './ConditionsTable'
import { ManagedBadge } from './ManagedBadge'
import { ManagedBanner } from './ManagedBanner'
import { ReadyBadge } from './ReadyBadge'
import { SpecSummary } from './SpecSummary'
import { ownedDNS, standaloneRouter } from '../test/fixtures'

describe('ReadyBadge', () => {
  it('renders Ready and NotReady', () => {
    const { rerender } = render(<ReadyBadge resource={standaloneRouter()} />)
    expect(screen.getByText('Ready')).toBeInTheDocument()
    rerender(
      <ReadyBadge
        resource={standaloneRouter({
          status: { connected: false, conditions: [{ type: 'Ready', status: 'False' }] },
        })}
      />,
    )
    expect(screen.getByText('NotReady')).toBeInTheDocument()
  })
})

describe('ManagedBadge and ManagedBanner', () => {
  it('hides the badge for standalone resources', () => {
    const { container } = render(<ManagedBadge resource={standaloneRouter()} />)
    expect(container).toBeEmptyDOMElement()
  })

  it('names the owner on owned resources', () => {
    render(<ManagedBadge resource={ownedDNS()} />)
    expect(screen.getByText(/Managed/)).toHaveTextContent('Service/app/frontend')
    render(<ManagedBanner resource={ownedDNS()} />)
    expect(screen.getByText('Managed by Service/app/frontend')).toBeInTheDocument()
    expect(screen.getByText(/owning Service, Ingress, or HTTPRoute/)).toBeInTheDocument()
  })
})

describe('ConditionsTable', () => {
  it('shows an empty state and condition rows', () => {
    const { rerender } = render(<ConditionsTable />)
    expect(screen.getByText(/No conditions reported yet/)).toBeInTheDocument()
    rerender(
      <ConditionsTable
        conditions={[{ type: 'Ready', status: 'True', reason: 'Applied', message: 'ok' }]}
      />,
    )
    expect(screen.getByText('Ready')).toBeInTheDocument()
    expect(screen.getByText('Applied')).toBeInTheDocument()
    expect(screen.getByText('ok')).toBeInTheDocument()
  })
})

describe('SpecSummary', () => {
  it('renders router address and secret name', () => {
    render(<SpecSummary resource={standaloneRouter()} />)
    expect(screen.getByText('192.0.2.10')).toBeInTheDocument()
    expect(screen.getByText('creds')).toBeInTheDocument()
  })
})
