import type {
  ApiErrorBody,
  ApiKindSlug,
  ConfigResponse,
  KindCount,
  ListResponse,
  NamespacesResponse,
  NameListResponse,
  OverviewResponse,
  ResourceObject,
} from './types'

export class ApiError extends Error {
  readonly status: number
  readonly reason?: string

  constructor(message: string, status: number, reason?: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.reason = reason
  }
}

async function parseApiError(res: Response): Promise<ApiError> {
  const fallback = `${res.status} ${res.statusText}`
  try {
    const body = (await res.json()) as ApiErrorBody
    const message = body.message || body.error || fallback
    return new ApiError(message, res.status, body.reason)
  } catch {
    return new ApiError(fallback, res.status)
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...init,
    headers: {
      Accept: 'application/json',
      ...(init?.body ? { 'Content-Type': 'application/json' } : {}),
      ...init?.headers,
    },
  })
  if (!res.ok) {
    throw await parseApiError(res)
  }
  if (res.status === 204) {
    return undefined as T
  }
  const text = await res.text()
  if (!text) {
    return undefined as T
  }
  return JSON.parse(text) as T
}

function asStringList(value: unknown): string[] {
  if (!Array.isArray(value)) {
    return []
  }
  return value
    .map((item) => {
      if (typeof item === 'string') {
        return item
      }
      if (item && typeof item === 'object') {
        const rec = item as Record<string, unknown>
        const meta = rec.metadata as Record<string, unknown> | undefined
        if (typeof rec.name === 'string') {
          return rec.name
        }
        if (meta && typeof meta.name === 'string') {
          return meta.name
        }
      }
      return ''
    })
    .filter(Boolean)
}

function finiteNumber(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

function kindCountFromUnknown(value: unknown): KindCount | undefined {
  if (!value || typeof value !== 'object') {
    return undefined
  }
  const rec = value as Record<string, unknown>
  const total = finiteNumber(rec.total) ?? finiteNumber(rec.count)
  if (total === undefined) {
    return undefined
  }
  const notReady = finiteNumber(rec.notReady) ?? 0
  const ready = finiteNumber(rec.ready) ?? Math.max(0, total - notReady)
  return { total, ready, notReady }
}

function kindCountMapFromUnknown(value: unknown): Record<string, KindCount> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return {}
  }
  const out: Record<string, KindCount> = {}
  for (const [key, item] of Object.entries(value as Record<string, unknown>)) {
    const count = kindCountFromUnknown(item)
    if (count) {
      out[key] = count
    }
  }
  return out
}

export function normalizeOverview(raw: unknown): Record<string, KindCount> {
  if (!raw || typeof raw !== 'object') {
    return {}
  }
  const rec = raw as Record<string, unknown>

  // Backend returns { kinds: [{ kind, count, notReady }, ...] }.
  if (Array.isArray(rec.kinds)) {
    const out: Record<string, KindCount> = {}
    for (const item of rec.kinds) {
      if (!item || typeof item !== 'object') {
        continue
      }
      const row = item as Record<string, unknown>
      if (typeof row.kind !== 'string') {
        continue
      }
      const count = kindCountFromUnknown(row)
      if (count) {
        out[row.kind] = count
      }
    }
    if (Object.keys(out).length > 0) {
      return out
    }
  }

  const fromKinds = kindCountMapFromUnknown(rec.kinds)
  if (Object.keys(fromKinds).length > 0) {
    return fromKinds
  }
  const fromCounts = kindCountMapFromUnknown(rec.counts)
  if (Object.keys(fromCounts).length > 0) {
    return fromCounts
  }
  return kindCountMapFromUnknown(rec)
}

export const queryKeys = {
  overview: ['overview'] as const,
  config: ['config'] as const,
  namespaces: ['namespaces'] as const,
  secrets: (namespace: string) => ['secrets', namespace] as const,
  services: (namespace: string) => ['services', namespace] as const,
  pods: (namespace: string) => ['pods', namespace] as const,
  resources: (kind: ApiKindSlug, namespace?: string) =>
    ['resources', kind, namespace || 'all'] as const,
  resource: (kind: ApiKindSlug, namespace: string, name: string) =>
    ['resource', kind, namespace, name] as const,
}

export const api = {
  overview: () => request<OverviewResponse>('/api/overview'),

  config: async (): Promise<string> => {
    const raw = await request<ConfigResponse>('/api/config')
    const namespace = typeof raw.namespace === 'string' ? raw.namespace.trim() : ''
    return namespace || 'default'
  },

  namespaces: async (): Promise<string[]> => {
    const raw = await request<NamespacesResponse>('/api/namespaces')
    return asStringList(raw.items ?? raw.namespaces).sort()
  },

  secrets: async (namespace: string): Promise<string[]> => {
    const raw = await request<NameListResponse>(`/api/secrets/${encodeURIComponent(namespace)}`)
    return asStringList(raw.items ?? raw.secrets).sort()
  },

  services: async (namespace: string): Promise<string[]> => {
    const raw = await request<NameListResponse>(`/api/services/${encodeURIComponent(namespace)}`)
    return asStringList(raw.items ?? raw.services).sort()
  },

  pods: async (namespace: string): Promise<string[]> => {
    const raw = await request<NameListResponse>(`/api/pods/${encodeURIComponent(namespace)}`)
    return asStringList(raw.items ?? raw.pods).sort()
  },

  listResources: async (kind: ApiKindSlug, namespace?: string): Promise<ResourceObject[]> => {
    const params = namespace ? `?namespace=${encodeURIComponent(namespace)}` : ''
    const raw = await request<ListResponse>(`/api/resources/${kind}${params}`)
    return raw.items ?? []
  },

  getResource: (kind: ApiKindSlug, namespace: string, name: string) =>
    request<ResourceObject>(
      `/api/resources/${kind}/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`,
    ),

  createResource: (kind: ApiKindSlug, namespace: string, body: ResourceObject) =>
    request<ResourceObject>(`/api/resources/${kind}/${encodeURIComponent(namespace)}`, {
      method: 'POST',
      body: JSON.stringify(body),
    }),

  updateResource: (kind: ApiKindSlug, namespace: string, name: string, body: ResourceObject) =>
    request<ResourceObject>(
      `/api/resources/${kind}/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`,
      {
        method: 'PUT',
        body: JSON.stringify(body),
      },
    ),

  deleteResource: (kind: ApiKindSlug, namespace: string, name: string) =>
    request<void>(
      `/api/resources/${kind}/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`,
      { method: 'DELETE' },
    ),
}
