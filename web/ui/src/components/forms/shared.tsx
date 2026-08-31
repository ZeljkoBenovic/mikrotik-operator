import { AutoComplete, Form, Input, Select, Typography } from 'antd'
import { useQuery } from '@tanstack/react-query'
import { useEffect, useMemo, useState } from 'react'
import { api, queryKeys } from '../../api/client'
import {
  KUBERNETES_NAME_MAX_LENGTH,
  kubernetesNameError,
  nameWasAdjusted,
  sanitizeKubernetesName,
} from '../../utils/k8sName'
import { liveRouterRefOptions } from './routerRef'

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

export function ResourceNameInput({ disabled }: { disabled: boolean }) {
  const form = Form.useFormInstance()
  const [adjusted, setAdjusted] = useState(false)

  function markAdjusted(raw: string, sanitized: string) {
    if (raw === '') {
      setAdjusted(false)
      return
    }
    if (nameWasAdjusted(raw, sanitized)) {
      setAdjusted(true)
    }
  }

  return (
    <Form.Item
      name="name"
      label="Name"
      extra={
        adjusted
          ? 'Adjusted to a valid Kubernetes name (lowercase letters, numbers, hyphens, and dots).'
          : undefined
      }
      rules={[
        { required: true, message: 'Name is required' },
        {
          validator: async (_, value: string) => {
            const err = kubernetesNameError(value)
            if (err) {
              return Promise.reject(new Error(err))
            }
          },
        },
      ]}
      getValueFromEvent={(event: { target?: { value?: string } } | string) => {
        const raw = typeof event === 'string' ? event : String(event?.target?.value ?? '')
        markAdjusted(raw, sanitizeKubernetesName(raw, { finalize: false }))
        return raw
      }}
      normalize={(value: string | undefined) => {
        if (typeof value !== 'string') {
          return value
        }
        return sanitizeKubernetesName(value, { finalize: false })
      }}
    >
      <Input
        disabled={disabled || undefined}
        placeholder="resource name"
        maxLength={KUBERNETES_NAME_MAX_LENGTH}
        autoComplete="off"
        spellCheck={false}
        onBlur={() => {
          const raw = String(form.getFieldValue('name') ?? '')
          const finalized = sanitizeKubernetesName(raw, { finalize: true })
          markAdjusted(raw, finalized)
          if (finalized !== raw) {
            form.setFieldValue('name', finalized)
          }
        }}
      />
    </Form.Item>
  )
}

export function RouterRefSelect({
  namespace,
  disabled,
  autoSelect,
  value,
  onChange,
}: {
  namespace?: string
  disabled?: boolean
  autoSelect?: boolean
  value?: string
  onChange?: (value: string | undefined) => void
}) {
  const query = useQuery({
    queryKey: queryKeys.resources('mikrotikrouters'),
    queryFn: () => api.listResources('mikrotikrouters'),
  })
  const options = useMemo(
    () => liveRouterRefOptions(query.data, namespace),
    [namespace, query.data],
  )

  useEffect(() => {
    if (!autoSelect || disabled || !namespace || value || options.length !== 1) {
      return
    }
    onChange?.(options[0].value)
  }, [autoSelect, disabled, namespace, onChange, options, value])

  const empty = !query.isLoading && options.length === 0

  return (
    <>
      <Select
        allowClear
        showSearch
        optionFilterProp="label"
        disabled={disabled}
        loading={query.isLoading}
        options={options}
        value={value || undefined}
        onChange={onChange}
        placeholder={empty ? 'No routers found' : 'Select a router'}
        notFoundContent={empty ? 'No MikroTikRouter resources found' : undefined}
        style={{ width: '100%' }}
      />
      {empty ? (
        <Typography.Text type="secondary" style={{ display: 'block', marginTop: 4 }}>
          No MikroTikRouters found. Create a Router first.
        </Typography.Text>
      ) : null}
    </>
  )
}
