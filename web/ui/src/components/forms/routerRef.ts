import type { ResourceObject } from '../../api/types'

export function liveRouterRefOptions(
  items: ResourceObject[] | undefined,
  resourceNamespace?: string,
): { value: string; label: string }[] {
  const live = (items ?? []).filter((item) => !item.metadata.deletionTimestamp)
  live.sort((a, b) => {
    const ns = (a.metadata.namespace ?? '').localeCompare(b.metadata.namespace ?? '')
    if (ns !== 0) {
      return ns
    }
    return a.metadata.name.localeCompare(b.metadata.name)
  })
  return live.map((item) => {
    const ns = item.metadata.namespace || 'default'
    const ref = resourceNamespace && ns === resourceNamespace ? item.metadata.name : `${ns}/${item.metadata.name}`
    return {
      value: ref,
      label: `${item.metadata.name} (${ns})`,
    }
  })
}
