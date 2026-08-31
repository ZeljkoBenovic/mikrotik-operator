export const API_VERSION = 'mikrotik.operator.io/v1alpha1'

export type ApiKindSlug =
  | 'mikrotikrouters'
  | 'mikrotikdnsrecords'
  | 'mikrotikroutes'
  | 'mikrotikportforwards'
  | 'mikrotikfirewallrules'

export type ApiKindName =
  | 'MikroTikRouter'
  | 'MikroTikDNSRecord'
  | 'MikroTikRoute'
  | 'MikroTikPortForward'
  | 'MikroTikFirewallRule'

export type ManagedBy = {
  apiVersion: string
  kind: string
  namespace: string
  name: string
}

export type OwnerReference = {
  apiVersion: string
  kind: string
  name: string
  uid?: string
  controller?: boolean
  blockOwnerDeletion?: boolean
}

export type ObjectMeta = {
  name: string
  namespace?: string
  uid?: string
  resourceVersion?: string
  generation?: number
  creationTimestamp?: string
  deletionTimestamp?: string
  labels?: Record<string, string>
  annotations?: Record<string, string>
  ownerReferences?: OwnerReference[]
  finalizers?: string[]
}

export type Condition = {
  type: string
  status: string
  reason?: string
  message?: string
  lastTransitionTime?: string
  observedGeneration?: number
}

export type LocalObjectReference = {
  name: string
}

export type NamespacedName = {
  namespace: string
  name: string
}

export type RouterEndpoint = {
  name: string
  address: string
  port?: number
  tls?: boolean
  credentialsSecret: LocalObjectReference
  routeGateway?: string
}

export type MikroTikRouterSpec = {
  address?: string
  port?: number
  tls?: boolean
  credentialsSecret?: LocalObjectReference
  routeGateway?: string
  routers?: RouterEndpoint[]
}

export type MikroTikRouterStatus = {
  connected?: boolean
  version?: string
  appliedEndpoints?: RouterEndpoint[]
  conditions?: Condition[]
}

export type MikroTikDNSRecordSpec = {
  routerRef?: string
  name: string
  address: string
  ttl?: string
  serviceRef?: NamespacedName
}

export type MikroTikDNSRecordStatus = {
  applied?: boolean
  routerID?: string
  routerRef?: string
  conditions?: Condition[]
}

export type MikroTikRouteSpec = {
  routerRef?: string
  destination: string
  gateway: string
  distance?: number
}

export type MikroTikRouteStatus = {
  applied?: boolean
  routerRef?: string
  conditions?: Condition[]
}

export type MikroTikPortForwardSpec = {
  routerRef: string
  protocol: string
  externalPort: number
  targetPort: number
  serviceRef?: NamespacedName
  podRef?: NamespacedName
  targetAddress?: string
}

export type MikroTikPortForwardStatus = {
  applied?: boolean
  routerID?: string
  routerRef?: string
  targetAddress?: string
  externalAddress?: string
  conditions?: Condition[]
}

export type MikroTikFirewallRuleSpec = {
  routerRef?: string
  chain: string
  action: string
  protocol?: string
  sourceAddress?: string
  destinationAddress?: string
  sourcePort?: string
  destinationPort?: string
  inInterface?: string
  outInterface?: string
  connectionState?: string[]
  connectionNatState?: string[]
  logPrefix?: string
  placeBefore?: boolean
}

export type MikroTikFirewallRuleStatus = {
  applied?: boolean
  routerRef?: string
  conditions?: Condition[]
}

export type KindSpec =
  | MikroTikRouterSpec
  | MikroTikDNSRecordSpec
  | MikroTikRouteSpec
  | MikroTikPortForwardSpec
  | MikroTikFirewallRuleSpec

export type KindStatus =
  | MikroTikRouterStatus
  | MikroTikDNSRecordStatus
  | MikroTikRouteStatus
  | MikroTikPortForwardStatus
  | MikroTikFirewallRuleStatus

export type ResourceObject = {
  apiVersion?: string
  kind?: string
  metadata: ObjectMeta
  spec: Record<string, unknown>
  status?: {
    connected?: boolean
    applied?: boolean
    version?: string
    routerID?: string
    routerRef?: string
    targetAddress?: string
    externalAddress?: string
    appliedEndpoints?: RouterEndpoint[]
    conditions?: Condition[]
    [key: string]: unknown
  }
  managedBy?: ManagedBy
}

export type KindCount = {
  total: number
  ready?: number
  notReady: number
}

export type OverviewKindItem = {
  kind: string
  count: number
  notReady: number
}

export type OverviewResponse = {
  kinds?: OverviewKindItem[] | Record<string, KindCount>
  counts?: Record<string, KindCount>
}

export type ListResponse = {
  items?: ResourceObject[]
}

export type NamespacesResponse = {
  items?: unknown
  namespaces?: unknown
}

export type NameListResponse = {
  items?: unknown
  secrets?: unknown
  services?: unknown
  pods?: unknown
}

export type SecretsResponse = NameListResponse

export type ConfigResponse = {
  namespace?: string
}

export type ApiErrorBody = {
  message?: string
  error?: string
  reason?: string
  code?: number
  status?: string
  details?: { name?: string; kind?: string }
}
