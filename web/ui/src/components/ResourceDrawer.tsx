import { App, Button, Drawer, Form, Space, Switch, Typography } from 'antd'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { api, queryKeys } from '../api/client'
import type { ResourceObject } from '../api/types'
import type { KindConfig } from '../kinds'
import { errorMessage } from '../utils/errors'
import { kubernetesNameError, sanitizeKubernetesName } from '../utils/k8sName'
import { isManaged, isReadOnly, toSubmitBody } from '../utils/resource'
import { fromYAML, toYAML } from '../utils/yaml'
import { YamlEditor } from './YamlEditor'
import { emptyForm, emptyResource, formFromResource, resourceFromForm } from './forms/convert'
import {
  DNSRecordForm,
  FirewallRuleForm,
  PortForwardForm,
  RouteForm,
  RouterForm,
  BackupForm,
  RestoreForm,
} from './forms/KindForms'

type ResourceDrawerProps = {
  kind: KindConfig
  open: boolean
  mode: 'create' | 'edit'
  resource?: ResourceObject
  onClose: () => void
  onCreated?: (namespace: string) => void
}

export function ResourceDrawer({
  kind,
  open,
  mode,
  resource,
  onClose,
  onCreated,
}: ResourceDrawerProps) {
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  const [form] = Form.useForm()
  const [yamlMode, setYamlMode] = useState(false)
  const [yamlText, setYamlText] = useState('')
  const config = useQuery({ queryKey: queryKeys.config, queryFn: api.config })
  const createMode = mode === 'create'
  const readOnly = Boolean(resource && isReadOnly(resource))
  const owned = Boolean(resource && isManaged(resource))
  const operatorNamespace = config.data || 'default'
  const sessionKey = !open
    ? 'closed'
    : `${mode}:${resource?.metadata.uid ?? resource?.metadata.name ?? 'create'}`
  const canSeed = open && (!createMode || Boolean(config.data) || config.isError)
  const [appliedSeed, setAppliedSeed] = useState('closed')
  if (!open && appliedSeed !== 'closed') {
    setAppliedSeed('closed')
  } else if (canSeed && appliedSeed !== sessionKey) {
    setAppliedSeed(sessionKey)
    const ns = resource?.metadata.namespace || operatorNamespace
    form.setFieldsValue(resource ? formFromResource(kind, resource) : emptyForm(kind, ns))
    const body = resource
      ? {
          apiVersion: resource.apiVersion,
          kind: resource.kind,
          metadata: {
            name: resource.metadata.name,
            namespace: resource.metadata.namespace,
            labels: resource.metadata.labels,
            annotations: resource.metadata.annotations,
          },
          spec: resource.spec,
        }
      : emptyResource(kind, ns)
    setYamlText(toYAML(body))
    setYamlMode(false)
  } else if (
    createMode &&
    appliedSeed === sessionKey &&
    config.data &&
    form.getFieldValue('namespace') !== config.data
  ) {
    const ns = config.data
    form.setFieldValue('namespace', ns)
    setYamlText((text) => yamlWithNamespace(text, ns))
  }
  const seeded = appliedSeed === sessionKey && sessionKey !== 'closed'
  const formLocked = createMode && !seeded

  const mutation = useMutation({
    mutationFn: async (body: ResourceObject) => {
      const payload = toSubmitBody(body)
      const ns = mode === 'create' ? operatorNamespace : payload.metadata.namespace || operatorNamespace
      payload.metadata.namespace = ns
      const name = payload.metadata.name
      if (mode === 'edit') {
        if (resource?.metadata.resourceVersion) {
          payload.metadata.resourceVersion = resource.metadata.resourceVersion
        }
        return api.updateResource(kind.slug, ns, name, payload)
      }
      return api.createResource(kind.slug, ns, payload)
    },
    onSuccess: async (_data, body) => {
      const ns = mode === 'create' ? operatorNamespace : body.metadata.namespace || operatorNamespace
      await queryClient.invalidateQueries({ queryKey: queryKeys.overview })
      await queryClient.invalidateQueries({ queryKey: ['resources', kind.slug] })
      await queryClient.invalidateQueries({
        queryKey: queryKeys.resource(kind.slug, ns, body.metadata.name),
      })
      if (mode === 'create') {
        message.success(`${kind.singular} created in ${ns}`)
        onCreated?.(ns)
      } else {
        message.success(`${kind.singular} updated`)
      }
      onClose()
    },
    onError: (error) => {
      message.error(errorMessage(error))
    },
  })

  function kindForm() {
    switch (kind.apiKind) {
      case 'MikroTikRouter':
        return <RouterForm createMode={createMode} />
      case 'MikroTikDNSRecord':
        return <DNSRecordForm createMode={createMode} />
      case 'MikroTikRoute':
        return <RouteForm createMode={createMode} />
      case 'MikroTikPortForward':
        return <PortForwardForm createMode={createMode} />
      case 'MikroTikFirewallRule':
        return <FirewallRuleForm createMode={createMode} />
      case 'MikroTikBackup':
        return <BackupForm createMode={createMode} />
      case 'MikroTikRestore':
        return <RestoreForm createMode={createMode} />
      default:
        return null
    }
  }

  async function submit() {
    if (readOnly) {
      message.warning(
        owned
          ? 'Owned resources cannot be edited from this UI.'
          : 'Applied restores cannot be edited from this UI.',
      )
      return
    }
    try {
      if (yamlMode) {
        const parsed = fromYAML(yamlText)
        parsed.apiVersion = parsed.apiVersion || resource?.apiVersion
        parsed.kind = parsed.kind || kind.apiKind
        if (!parsed.metadata?.name) {
          throw new Error('metadata.name is required')
        }
        const nameError = kubernetesNameError(parsed.metadata.name)
        if (nameError) {
          throw new Error(nameError)
        }
        if (createMode) {
          parsed.metadata.namespace = operatorNamespace
        } else if (!parsed.metadata.namespace) {
          parsed.metadata.namespace = resource?.metadata.namespace || operatorNamespace
        }
        if (kind.apiKind === 'MikroTikRestore' && parsed.spec && typeof parsed.spec === 'object') {
          delete (parsed.spec as Record<string, unknown>).confirm
        }
        await mutation.mutateAsync(parsed)
        return
      }
      form.setFieldValue(
        'name',
        sanitizeKubernetesName(String(form.getFieldValue('name') ?? ''), { finalize: true }),
      )
      const values = await form.validateFields()
      const body = resourceFromForm(kind, values)
      if (createMode) {
        body.metadata.namespace = operatorNamespace
      }
      if (mode === 'edit' && resource) {
        body.metadata.labels = resource.metadata.labels
        body.metadata.annotations = resource.metadata.annotations
        body.metadata.resourceVersion = resource.metadata.resourceVersion
      }
      await mutation.mutateAsync(body)
    } catch (error) {
      if (error && typeof error === 'object' && 'errorFields' in error) {
        return
      }
      message.error(errorMessage(error))
    }
  }

  function toggleYaml(next: boolean) {
    try {
      if (next) {
        const values = form.getFieldsValue(true)
        const body = resourceFromForm(kind, values)
        if (mode === 'edit' && resource) {
          body.metadata.labels = resource.metadata.labels
          body.metadata.annotations = resource.metadata.annotations
        }
        setYamlText(toYAML(body))
      } else {
        const parsed = fromYAML(yamlText)
        form.setFieldsValue(formFromResource(kind, parsed))
      }
      setYamlMode(next)
    } catch (error) {
      message.error(errorMessage(error))
    }
  }

  return (
    <Drawer
      title={mode === 'edit' ? `Edit ${kind.singular}` : `Create ${kind.singular}`}
      width={640}
      open={open}
      onClose={onClose}
      destroyOnClose
      extra={
        <Space>
          <Typography.Text type="secondary">YAML</Typography.Text>
          <Switch checked={yamlMode} onChange={toggleYaml} disabled={readOnly || formLocked} />
        </Space>
      }
      footer={
        <Space style={{ display: 'flex', justifyContent: 'flex-end' }}>
          <Button onClick={onClose}>Cancel</Button>
          <Button
            type="primary"
            onClick={() => void submit()}
            loading={mutation.isPending || formLocked}
            disabled={readOnly || formLocked}
          >
            {mode === 'edit' ? 'Save' : 'Create'}
          </Button>
        </Space>
      }
    >
      {yamlMode ? (
        <YamlEditor
          value={yamlText}
          onChange={setYamlText}
          readOnly={readOnly || formLocked}
          height="calc(100vh - 220px)"
        />
      ) : (
        <Form form={form} layout="vertical" requiredMark="optional" disabled={formLocked || readOnly}>
          {kindForm()}
        </Form>
      )}
    </Drawer>
  )
}

function yamlWithNamespace(text: string, namespace: string): string {
  try {
    const parsed = fromYAML(text)
    if (parsed.metadata.namespace === namespace) {
      return text
    }
    parsed.metadata.namespace = namespace
    return toYAML(parsed)
  } catch {
    return text
  }
}
