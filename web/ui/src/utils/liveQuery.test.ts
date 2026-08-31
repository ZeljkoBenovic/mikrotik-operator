import { describe, expect, it } from 'vitest'
import { notReadyRoute, standaloneRouter } from '../test/fixtures'
import {
  LIVE_STATUS_FAST_MS,
  LIVE_STATUS_SLOW_MS,
  liveListRefetchInterval,
  liveResourceRefetchInterval,
} from './liveQuery'

describe('live status polling', () => {
  it('polls lists quickly when any item is not ready', () => {
    expect(liveListRefetchInterval({ state: { data: undefined } })).toBe(LIVE_STATUS_FAST_MS)
    expect(liveListRefetchInterval({ state: { data: [notReadyRoute()] } })).toBe(LIVE_STATUS_FAST_MS)
    expect(liveListRefetchInterval({ state: { data: [standaloneRouter(), notReadyRoute()] } })).toBe(
      LIVE_STATUS_FAST_MS,
    )
    expect(liveListRefetchInterval({ state: { data: [standaloneRouter()] } })).toBe(LIVE_STATUS_SLOW_MS)
  })

  it('polls a detail view quickly until Ready', () => {
    expect(liveResourceRefetchInterval({ state: { data: undefined } })).toBe(LIVE_STATUS_FAST_MS)
    expect(liveResourceRefetchInterval({ state: { data: notReadyRoute() } })).toBe(LIVE_STATUS_FAST_MS)
    expect(liveResourceRefetchInterval({ state: { data: standaloneRouter() } })).toBe(LIVE_STATUS_SLOW_MS)
  })
})
