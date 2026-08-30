import { AutoComplete } from 'antd'
import { useQuery } from '@tanstack/react-query'
import { api, queryKeys } from '../../api/client'

export function SecretNameSelect({ namespace, disabled }: { namespace?: string; disabled?: boolean }) {
  const query = useQuery({
    queryKey: queryKeys.secrets(namespace ?? ''),
    queryFn: () => api.secrets(namespace ?? ''),
    enabled: Boolean(namespace),
  })
  return (
    <AutoComplete
      allowClear
      disabled={disabled || !namespace}
      options={(query.data ?? []).map((name) => ({ value: name }))}
      placeholder={namespace ? 'Secret name in this namespace' : 'Select a namespace first'}
      filterOption={(input, option) =>
        (option?.value ?? '').toLowerCase().includes(input.toLowerCase())
      }
    />
  )
}

export function RouterRefSelect({ namespace, disabled }: { namespace?: string; disabled?: boolean }) {
  const query = useQuery({
    queryKey: queryKeys.resources('mikrotikrouters', namespace),
    queryFn: () => api.listResources('mikrotikrouters', namespace),
    enabled: Boolean(namespace),
  })
  return (
    <AutoComplete
      allowClear
      disabled={disabled}
      options={(query.data ?? [])
        .filter((item) => !namespace || item.metadata.namespace === namespace)
        .map((item) => ({ value: item.metadata.name }))}
      placeholder="MikroTikRouter name"
      filterOption={(input, option) =>
        (option?.value ?? '').toLowerCase().includes(input.toLowerCase())
      }
    />
  )
}

export function NamespaceSelect({
  namespaces,
  loading,
  disabled,
}: {
  namespaces: string[]
  loading?: boolean
  disabled?: boolean
}) {
  const options = Array.from(new Set(['default', ...namespaces])).map((name) => ({ value: name }))
  return (
    <AutoComplete
      allowClear={false}
      disabled={disabled}
      options={options}
      placeholder={loading ? 'Loading namespaces…' : 'Namespace'}
      filterOption={(input, option) =>
        (option?.value ?? '').toLowerCase().includes(input.toLowerCase())
      }
    />
  )
}
