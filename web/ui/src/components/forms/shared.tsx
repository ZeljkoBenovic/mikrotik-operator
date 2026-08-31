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
      placeholder={namespace ? 'Secret name' : 'Waiting for operator namespace'}
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
