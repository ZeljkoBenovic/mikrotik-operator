import { AutoComplete, Col, Form, Input, Row, Select, Typography } from 'antd'
import { useQuery } from '@tanstack/react-query'
import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { api, queryKeys } from '../../api/client'
import { errorMessage } from '../../utils/errors'
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

type ClusterNameKind = 'services' | 'pods'

const SERVICE_REF_NAMESPACE = ['spec', 'serviceRef', 'namespace'] as const
const SERVICE_REF_NAME = ['spec', 'serviceRef', 'name'] as const
const POD_REF_NAMESPACE = ['spec', 'podRef', 'namespace'] as const
const POD_REF_NAME = ['spec', 'podRef', 'name'] as const

function filterNameOption(input: string, option?: { label?: string; value?: string }) {
  const haystack = `${option?.label ?? ''} ${option?.value ?? ''}`.toLowerCase()
  return haystack.includes(input.toLowerCase())
}

export function SearchableNameSelect({
  names,
  loading,
  disabled,
  placeholder,
  notFoundContent,
  extra,
  value,
  onChange,
  id,
  'aria-label': ariaLabel,
}: {
  names: string[]
  loading?: boolean
  disabled?: boolean
  placeholder?: string
  notFoundContent?: ReactNode
  extra?: ReactNode
  value?: string
  onChange?: (value: string | undefined) => void
  id?: string
  'aria-label'?: string
}) {
  const [search, setSearch] = useState('')
  const options = useMemo(() => {
    const seen = new Set(names)
    const items = names.map((name) => ({ value: name, label: name }))
    const typed = search.trim()
    if (typed && !seen.has(typed)) {
      items.push({ value: typed, label: `Use "${typed}"` })
    }
    return items
  }, [names, search])

  return (
    <>
      <Select
        id={id}
        aria-label={ariaLabel}
        allowClear
        showSearch
        virtual
        disabled={disabled}
        loading={loading}
        options={options}
        value={value || undefined}
        onChange={(next) => onChange?.(next)}
        onSearch={setSearch}
        onBlur={() => setSearch('')}
        filterOption={filterNameOption}
        placeholder={placeholder}
        notFoundContent={notFoundContent}
        style={{ width: '100%' }}
      />
      {extra}
    </>
  )
}

export function NamespaceSelect({
  disabled,
  placeholder,
  value,
  onChange,
  id,
  'aria-label': ariaLabel,
}: {
  disabled?: boolean
  placeholder?: string
  value?: string
  onChange?: (value: string | undefined) => void
  id?: string
  'aria-label'?: string
}) {
  const query = useQuery({ queryKey: queryKeys.namespaces, queryFn: api.namespaces })
  const failed = query.isError
  const empty = query.isSuccess && (query.data?.length ?? 0) === 0
  return (
    <SearchableNameSelect
      id={id}
      aria-label={ariaLabel}
      names={query.data ?? []}
      loading={query.isLoading}
      disabled={disabled}
      value={value}
      onChange={onChange}
      placeholder={
        failed ? 'Could not load namespaces' : empty ? 'No namespaces found' : placeholder || 'Search namespace'
      }
      notFoundContent={failed ? errorMessage(query.error) : empty ? 'No namespaces found' : undefined}
      extra={
        failed ? (
          <Typography.Text type="danger" style={{ display: 'block', marginTop: 4 }}>
            {errorMessage(query.error)}
          </Typography.Text>
        ) : null
      }
    />
  )
}

export function ClusterResourceNameSelect({
  kind,
  namespace,
  disabled,
  placeholder,
  value,
  onChange,
  id,
  'aria-label': ariaLabel,
}: {
  kind: ClusterNameKind
  namespace?: string
  disabled?: boolean
  placeholder?: string
  value?: string
  onChange?: (value: string | undefined) => void
  id?: string
  'aria-label'?: string
}) {
  const query = useQuery({
    queryKey: kind === 'services' ? queryKeys.services(namespace ?? '') : queryKeys.pods(namespace ?? ''),
    queryFn: () => (kind === 'services' ? api.services(namespace ?? '') : api.pods(namespace ?? '')),
    enabled: Boolean(namespace),
  })
  const noun = kind === 'services' ? 'service' : 'pod'
  const failed = Boolean(namespace) && query.isError
  const empty = Boolean(namespace) && query.isSuccess && (query.data?.length ?? 0) === 0
  const waiting = !namespace
  return (
    <SearchableNameSelect
      id={id}
      aria-label={ariaLabel}
      names={query.data ?? []}
      loading={Boolean(namespace) && query.isLoading}
      disabled={disabled || waiting}
      value={value}
      onChange={onChange}
      placeholder={
        waiting
          ? 'Select a namespace first'
          : failed
            ? `Could not load ${noun}s`
            : empty
              ? `No ${noun}s found`
              : placeholder || `Search ${noun} name`
      }
      notFoundContent={
        failed ? errorMessage(query.error) : empty ? `No ${noun}s in this namespace` : undefined
      }
      extra={
        failed ? (
          <Typography.Text type="danger" style={{ display: 'block', marginTop: 4 }}>
            {errorMessage(query.error)}
          </Typography.Text>
        ) : empty ? (
          <Typography.Text type="secondary" style={{ display: 'block', marginTop: 4 }}>
            Type a {noun} name if it does not exist yet.
          </Typography.Text>
        ) : null
      }
    />
  )
}

function NamespaceSelectField({
  namePath,
  'aria-label': ariaLabel,
  value,
  onChange,
  id,
}: {
  namePath: readonly string[]
  'aria-label': string
  value?: string
  onChange?: (value: string | undefined) => void
  id?: string
}) {
  const form = Form.useFormInstance()
  return (
    <NamespaceSelect
      id={id}
      aria-label={ariaLabel}
      value={value}
      onChange={(next) => {
        if (next !== value) {
          form.setFieldValue([...namePath], undefined)
        }
        onChange?.(next)
      }}
    />
  )
}

export function NamespacedRefFields({
  kind,
  namespaceLabel,
  nameLabel,
  required,
}: {
  kind: 'service' | 'pod'
  namespaceLabel: string
  nameLabel: string
  required?: boolean
}) {
  const namespacePath = kind === 'service' ? SERVICE_REF_NAMESPACE : POD_REF_NAMESPACE
  const namePath = kind === 'service' ? SERVICE_REF_NAME : POD_REF_NAME
  const selectedNamespace = Form.useWatch(namespacePath) as string | undefined

  return (
    <Row gutter={12}>
      <Col span={12}>
        <Form.Item
          name={[...namespacePath]}
          label={namespaceLabel}
          rules={required ? [{ required: true, message: `${namespaceLabel} is required` }] : undefined}
        >
          <NamespaceSelectField aria-label={namespaceLabel} namePath={namePath} />
        </Form.Item>
      </Col>
      <Col span={12}>
        <Form.Item
          name={[...namePath]}
          label={nameLabel}
          rules={required ? [{ required: true, message: `${nameLabel} is required` }] : undefined}
        >
          <ClusterResourceNameSelect
            aria-label={nameLabel}
            kind={kind === 'service' ? 'services' : 'pods'}
            namespace={selectedNamespace}
          />
        </Form.Item>
      </Col>
    </Row>
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
  id,
}: {
  namespace?: string
  disabled?: boolean
  autoSelect?: boolean
  value?: string
  onChange?: (value: string | undefined) => void
  id?: string
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

  const empty = query.isSuccess && options.length === 0
  const failed = query.isError

  return (
    <>
      <Select
        id={id}
        allowClear
        showSearch
        optionFilterProp="label"
        disabled={disabled}
        loading={query.isLoading}
        options={options}
        value={value || undefined}
        onChange={onChange}
        placeholder={failed ? 'Could not load routers' : empty ? 'No routers found' : 'Select a router'}
        notFoundContent={
          failed ? errorMessage(query.error) : empty ? 'No MikroTikRouter resources found' : undefined
        }
        style={{ width: '100%' }}
      />
      {failed ? (
        <Typography.Text type="danger" style={{ display: 'block', marginTop: 4 }}>
          {errorMessage(query.error)}
        </Typography.Text>
      ) : empty ? (
        <Typography.Text type="secondary" style={{ display: 'block', marginTop: 4 }}>
          No MikroTikRouters found. Create a Router first.
        </Typography.Text>
      ) : null}
    </>
  )
}
