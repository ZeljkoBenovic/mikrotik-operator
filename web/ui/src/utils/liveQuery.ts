import type { ResourceObject } from '../api/types'
import { isReady } from './resource'

/** Poll faster while resources are reconciling so Ready/conditions appear without a refresh. */
export const LIVE_STATUS_FAST_MS = 2_000
export const LIVE_STATUS_SLOW_MS = 8_000

type QueryLike<T> = {
  state: {
    data: T
  }
}

export function liveListRefetchInterval(query: QueryLike<ResourceObject[] | undefined>): number {
  const items = query.state.data
  if (!items || items.some((item) => !isReady(item))) {
    return LIVE_STATUS_FAST_MS
  }
  return LIVE_STATUS_SLOW_MS
}

export function liveResourceRefetchInterval(query: QueryLike<ResourceObject | undefined>): number {
  const resource = query.state.data
  if (!resource || !isReady(resource)) {
    return LIVE_STATUS_FAST_MS
  }
  return LIVE_STATUS_SLOW_MS
}
