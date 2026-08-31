import { AutoComplete, Button, Checkbox, Col, Form, Input, InputNumber, Radio, Row, Space, Switch, Typography } from 'antd'
import { MinusCircleOutlined, PlusOutlined } from '@ant-design/icons'
import {
  NamespacedRefFields,
  ResourceNameInput,
  RouterRefSelect,
  SecretNameSelect,
} from './shared'
import {
  CONNECTION_NAT_STATES,
  CONNECTION_STATES,
  FIREWALL_ACTIONS,
  FIREWALL_CHAINS,
} from './options'

type FormCommonProps = {
  createMode: boolean
}

function MetaFields({ createMode }: FormCommonProps) {
  return (
    <>
      <ResourceNameInput disabled={!createMode} />
      <Form.Item name="namespace" hidden>
        <Input />
      </Form.Item>
    </>
  )
}

export function RouterForm({ createMode }: FormCommonProps) {
  const namespace = Form.useWatch('namespace') as string | undefined
  const mode = Form.useWatch(['spec', 'endpointMode']) as string | undefined
  return (
    <>
      <MetaFields createMode={createMode} />
      <Form.Item name={['spec', 'endpointMode']} label="Endpoints" initialValue="single">
        <Radio.Group>
          <Radio.Button value="single">Single</Radio.Button>
          <Radio.Button value="multi">Multiple</Radio.Button>
        </Radio.Group>
      </Form.Item>
      {mode !== 'multi' ? (
        <>
          <Form.Item
            name={['spec', 'address']}
            label="Address"
            rules={[{ required: mode !== 'multi', message: 'Router address is required' }]}
          >
            <Input placeholder="192.168.88.1" />
          </Form.Item>
          <Row gutter={12}>
            <Col span={12}>
              <Form.Item name={['spec', 'port']} label="Port">
                <InputNumber min={1} max={65535} style={{ width: '100%' }} placeholder="8728 / 8729" />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name={['spec', 'tls']} label="TLS" valuePropName="checked">
                <Switch />
              </Form.Item>
            </Col>
          </Row>
          <Form.Item
            name={['spec', 'credentialsSecret', 'name']}
            label="Credentials secret"
            extra="Secret name only. Username and password are never displayed."
            rules={[{ required: mode !== 'multi', message: 'Credentials secret is required' }]}
          >
            <SecretNameSelect namespace={namespace} />
          </Form.Item>
          <Form.Item name={['spec', 'routeGateway']} label="Route gateway">
            <Input placeholder="optional default gateway for generated routes" />
          </Form.Item>
        </>
      ) : (
        <Form.List name={['spec', 'routers']}>
          {(fields, { add, remove }) => (
            <Space direction="vertical" style={{ width: '100%' }} size="middle">
              {fields.map((field) => (
                <div key={field.key} style={{ border: '1px solid #f0f0f0', padding: 12, borderRadius: 6 }}>
                  <Row gutter={12}>
                    <Col span={11}>
                      <Form.Item
                        name={[field.name, 'name']}
                        label="Name"
                        rules={[{ required: true, message: 'Endpoint name is required' }]}
                      >
                        <Input placeholder="primary" />
                      </Form.Item>
                    </Col>
                    <Col span={11}>
                      <Form.Item
                        name={[field.name, 'address']}
                        label="Address"
                        rules={[{ required: true, message: 'Address is required' }]}
                      >
                        <Input placeholder="10.0.20.254" />
                      </Form.Item>
                    </Col>
                    <Col span={2}>
                      <Button
                        type="text"
                        danger
                        icon={<MinusCircleOutlined />}
                        onClick={() => remove(field.name)}
                        style={{ marginTop: 30 }}
                      />
                    </Col>
                  </Row>
                  <Row gutter={12}>
                    <Col span={8}>
                      <Form.Item name={[field.name, 'port']} label="Port">
                        <InputNumber min={1} max={65535} style={{ width: '100%' }} />
                      </Form.Item>
                    </Col>
                    <Col span={8}>
                      <Form.Item name={[field.name, 'tls']} label="TLS" valuePropName="checked">
                        <Switch />
                      </Form.Item>
                    </Col>
                    <Col span={8}>
                      <Form.Item name={[field.name, 'routeGateway']} label="Route gateway">
                        <Input />
                      </Form.Item>
                    </Col>
                  </Row>
                  <Form.Item
                    name={[field.name, 'credentialsSecret', 'name']}
                    label="Credentials secret"
                    extra="Secret name only. Credentials are never displayed."
                    rules={[{ required: true, message: 'Credentials secret is required' }]}
                  >
                    <SecretNameSelect namespace={namespace} />
                  </Form.Item>
                </div>
              ))}
              <Button type="dashed" onClick={() => add()} icon={<PlusOutlined />} block>
                Add endpoint
              </Button>
            </Space>
          )}
        </Form.List>
      )}
    </>
  )
}

export function DNSRecordForm({ createMode }: FormCommonProps) {
  const namespace = Form.useWatch('namespace') as string | undefined
  return (
    <>
      <MetaFields createMode={createMode} />
      <Form.Item name={['spec', 'routerRef']} label="Router">
        <RouterRefSelect namespace={namespace} autoSelect={createMode} />
      </Form.Item>
      <Form.Item
        name={['spec', 'name']}
        label="DNS name"
        rules={[{ required: true, message: 'DNS name is required' }]}
      >
        <Input placeholder="api.home.arpa" />
      </Form.Item>
      <Form.Item
        name={['spec', 'address']}
        label="Address"
        rules={[{ required: true, message: 'Address is required' }]}
      >
        <Input placeholder="10.0.0.20" />
      </Form.Item>
      <Form.Item name={['spec', 'ttl']} label="TTL">
        <Input placeholder="1h" />
      </Form.Item>
      <NamespacedRefFields
        kind="service"
        namespaceLabel="Service namespace"
        nameLabel="Service name"
      />
    </>
  )
}

export function RouteForm({ createMode }: FormCommonProps) {
  const namespace = Form.useWatch('namespace') as string | undefined
  return (
    <>
      <MetaFields createMode={createMode} />
      <Form.Item name={['spec', 'routerRef']} label="Router">
        <RouterRefSelect namespace={namespace} autoSelect={createMode} />
      </Form.Item>
      <Form.Item
        name={['spec', 'destination']}
        label="Destination"
        rules={[{ required: true, message: 'Destination is required' }]}
      >
        <Input placeholder="10.50.50.1/32" />
      </Form.Item>
      <Form.Item
        name={['spec', 'gateway']}
        label="Gateway"
        rules={[{ required: true, message: 'Gateway is required' }]}
      >
        <Input placeholder="10.0.20.10" />
      </Form.Item>
      <Form.Item name={['spec', 'distance']} label="Distance">
        <InputNumber min={1} max={255} style={{ width: '100%' }} />
      </Form.Item>
    </>
  )
}

export function PortForwardForm({ createMode }: FormCommonProps) {
  const namespace = Form.useWatch('namespace') as string | undefined
  const targetType = Form.useWatch(['spec', 'targetType']) as string | undefined
  return (
    <>
      <MetaFields createMode={createMode} />
      <Form.Item
        name={['spec', 'routerRef']}
        label="Router"
        rules={[{ required: true, message: 'Router is required' }]}
      >
        <RouterRefSelect namespace={namespace} autoSelect={createMode} />
      </Form.Item>
      <Form.Item
        name={['spec', 'protocol']}
        label="Protocol"
        rules={[{ required: true, message: 'Protocol is required' }]}
        initialValue="tcp"
      >
        <Radio.Group>
          <Radio.Button value="tcp">TCP</Radio.Button>
          <Radio.Button value="udp">UDP</Radio.Button>
        </Radio.Group>
      </Form.Item>
      <Row gutter={12}>
        <Col span={12}>
          <Form.Item
            name={['spec', 'externalPort']}
            label="External port"
            rules={[{ required: true, message: 'External port is required' }]}
          >
            <InputNumber min={1} max={65535} style={{ width: '100%' }} />
          </Form.Item>
        </Col>
        <Col span={12}>
          <Form.Item
            name={['spec', 'targetPort']}
            label="Target port"
            rules={[{ required: true, message: 'Target port is required' }]}
          >
            <InputNumber min={1} max={65535} style={{ width: '100%' }} />
          </Form.Item>
        </Col>
      </Row>
      <Form.Item
        name={['spec', 'destinationAddress']}
        label="Destination address"
        extra="Optional. RouterOS dst-address: the IP that initially receives the traffic. Leave empty to match any destination."
      >
        <Input placeholder="203.0.113.10" />
      </Form.Item>
      <Form.Item
        name={['spec', 'targetType']}
        label="Target"
        extra="Exactly one of target address, Service, or Pod."
        initialValue="address"
      >
        <Radio.Group>
          <Radio.Button value="address">Address</Radio.Button>
          <Radio.Button value="service">Service</Radio.Button>
          <Radio.Button value="pod">Pod</Radio.Button>
        </Radio.Group>
      </Form.Item>
      {targetType === 'service' ? (
        <NamespacedRefFields
          kind="service"
          namespaceLabel="Service namespace"
          nameLabel="Service name"
          required
        />
      ) : null}
      {targetType === 'pod' ? (
        <NamespacedRefFields
          kind="pod"
          namespaceLabel="Pod namespace"
          nameLabel="Pod name"
          required
        />
      ) : null}
      {targetType !== 'service' && targetType !== 'pod' ? (
        <Form.Item
          name={['spec', 'targetAddress']}
          label="Target address"
          rules={[{ required: true, message: 'Target address is required' }]}
        >
          <Input placeholder="10.0.0.20" />
        </Form.Item>
      ) : null}
    </>
  )
}

export function FirewallRuleForm({ createMode }: FormCommonProps) {
  const namespace = Form.useWatch('namespace') as string | undefined
  return (
    <>
      <MetaFields createMode={createMode} />
      <Form.Item name={['spec', 'routerRef']} label="Router">
        <RouterRefSelect namespace={namespace} autoSelect={createMode} />
      </Form.Item>
      <Form.Item name={['spec', 'chain']} label="Chain" rules={[{ required: true, message: 'Chain is required' }]}>
        <AutoComplete options={FIREWALL_CHAINS.map((value) => ({ value }))} placeholder="forward" />
      </Form.Item>
      <Form.Item name={['spec', 'action']} label="Action" rules={[{ required: true, message: 'Action is required' }]}>
        <AutoComplete options={FIREWALL_ACTIONS.map((value) => ({ value }))} placeholder="accept" />
      </Form.Item>
      <Form.Item name={['spec', 'protocol']} label="Protocol">
        <Input placeholder="tcp, udp, icmp…" />
      </Form.Item>
      <Row gutter={12}>
        <Col span={12}>
          <Form.Item name={['spec', 'sourceAddress']} label="Source address">
            <Input placeholder="10.0.20.0/24" />
          </Form.Item>
        </Col>
        <Col span={12}>
          <Form.Item name={['spec', 'destinationAddress']} label="Destination address">
            <Input placeholder="10.43.0.0/16" />
          </Form.Item>
        </Col>
      </Row>
      <Row gutter={12}>
        <Col span={12}>
          <Form.Item name={['spec', 'sourcePort']} label="Source port">
            <Input />
          </Form.Item>
        </Col>
        <Col span={12}>
          <Form.Item name={['spec', 'destinationPort']} label="Destination port">
            <Input />
          </Form.Item>
        </Col>
      </Row>
      <Row gutter={12}>
        <Col span={12}>
          <Form.Item name={['spec', 'inInterface']} label="In interface">
            <Input />
          </Form.Item>
        </Col>
        <Col span={12}>
          <Form.Item name={['spec', 'outInterface']} label="Out interface">
            <Input />
          </Form.Item>
        </Col>
      </Row>
      <Form.Item name={['spec', 'connectionState']} label="Connection state">
        <Checkbox.Group options={CONNECTION_STATES} />
      </Form.Item>
      <Form.Item name={['spec', 'connectionNatState']} label="Connection NAT state">
        <Checkbox.Group options={CONNECTION_NAT_STATES} />
      </Form.Item>
      <Form.Item name={['spec', 'logPrefix']} label="Log prefix">
        <Input />
      </Form.Item>
      <Form.Item name={['spec', 'placeBefore']} label="Place before" valuePropName="checked">
        <Switch />
      </Form.Item>
    </>
  )
}

export function BackupForm({ createMode }: FormCommonProps) {
  const namespace = Form.useWatch('namespace') as string | undefined
  const mode = Form.useWatch(['spec', 'backupMode']) as string | undefined
  return (
    <>
      <MetaFields createMode={createMode} />
      <Form.Item
        name={['spec', 'routerRef']}
        label="Router"
        rules={[{ required: true, message: 'Router is required' }]}
      >
        <RouterRefSelect namespace={namespace} autoSelect={createMode} />
      </Form.Item>
      <Form.Item name={['spec', 'backupMode']} label="Trigger" initialValue="once">
        <Radio.Group>
          <Radio.Button value="once">Run once</Radio.Button>
          <Radio.Button value="schedule">Schedule</Radio.Button>
        </Radio.Group>
      </Form.Item>
      {mode === 'schedule' ? (
        <>
          <Form.Item
            name={['spec', 'schedule']}
            label="Cron schedule"
            extra="Five-field cron. The operator creates snapshot children and prunes by retention."
            rules={[{ required: true, message: 'Schedule is required' }]}
          >
            <Input placeholder="0 2 * * *" />
          </Form.Item>
          <Form.Item name={['spec', 'retention']} label="Retention" extra="Snapshots kept for this policy.">
            <InputNumber min={1} max={100} style={{ width: '100%' }} />
          </Form.Item>
        </>
      ) : (
        <Typography.Paragraph type="secondary">
          Creating this object captures one RouterOS <code>/export</code> into status. Exports live in
          etcd and should stay well under 1.5MiB.
        </Typography.Paragraph>
      )}
    </>
  )
}

export function RestoreForm({ createMode }: FormCommonProps) {
  const namespace = Form.useWatch('namespace') as string | undefined
  const targetType = Form.useWatch(['spec', 'targetType']) as string | undefined
  return (
    <>
      <MetaFields createMode={createMode} />
      <Form.Item
        name={['spec', 'backupRef', 'name']}
        label="Source backup"
        extra="Name of a MikroTikBackup snapshot in this namespace (or set namespace in YAML)."
        rules={[{ required: true, message: 'Backup name is required' }]}
      >
        <Input placeholder="once" />
      </Form.Item>
      <Form.Item name={['spec', 'backupRef', 'namespace']} label="Backup namespace">
        <Input placeholder="same as this restore" />
      </Form.Item>
      <Form.Item name={['spec', 'targetType']} label="Target" initialValue="router">
        <Radio.Group>
          <Radio.Button value="router">Existing router CR</Radio.Button>
          <Radio.Button value="connection">New router (IP + secret)</Radio.Button>
        </Radio.Group>
      </Form.Item>
      {targetType === 'connection' ? (
        <>
          <Form.Item
            name={['spec', 'connection', 'address']}
            label="Address"
            rules={[{ required: true, message: 'Address is required' }]}
          >
            <Input placeholder="192.168.88.1" />
          </Form.Item>
          <Row gutter={12}>
            <Col span={12}>
              <Form.Item name={['spec', 'connection', 'port']} label="Port">
                <InputNumber min={1} max={65535} style={{ width: '100%' }} placeholder="8728 / 8729" />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item
                name={['spec', 'connection', 'tls']}
                label="TLS"
                valuePropName="checked"
                initialValue={createMode ? true : undefined}
              >
                <Switch />
              </Form.Item>
            </Col>
          </Row>
          <Form.Item
            name={['spec', 'connection', 'credentialsSecret', 'name']}
            label="Credentials secret"
            extra="Secret in this restore's namespace. Username and password are never displayed."
            rules={[{ required: true, message: 'Credentials secret is required' }]}
          >
            <SecretNameSelect namespace={namespace} />
          </Form.Item>
        </>
      ) : (
        <Form.Item
          name={['spec', 'routerRef']}
          label="Router"
          rules={[{ required: true, message: 'Router is required' }]}
        >
          <RouterRefSelect namespace={namespace} autoSelect={createMode} />
        </Form.Item>
      )}
      <Typography.Paragraph type="warning">
        Restore does not run until you confirm on the resource page. That step sets{' '}
        <code>spec.confirm: RESTORE</code> and the operator then runs <code>/import</code>. Import
        does not wipe the device; it is best used on an empty router. YAML create/edit in this UI
        cannot set <code>spec.confirm</code> — use the confirm dialog or kubectl.
      </Typography.Paragraph>
    </>
  )
}
