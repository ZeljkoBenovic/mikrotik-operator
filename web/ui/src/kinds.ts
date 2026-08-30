import {
  ApiOutlined,
  ClusterOutlined,
  GlobalOutlined,
  SafetyCertificateOutlined,
  SwapOutlined,
} from '@ant-design/icons'
import type { ComponentType, CSSProperties } from 'react'
import type { ApiKindName, ApiKindSlug } from './api/types'

export type KindConfig = {
  slug: ApiKindSlug
  apiKind: ApiKindName
  path: string
  label: string
  singular: string
  description: string
  icon: ComponentType<{ style?: CSSProperties }>
}

export const KINDS: KindConfig[] = [
  {
    slug: 'mikrotikrouters',
    apiKind: 'MikroTikRouter',
    path: '/routers',
    label: 'Routers',
    singular: 'Router',
    description: 'RouterOS connection endpoints and credentials',
    icon: ClusterOutlined,
  },
  {
    slug: 'mikrotikdnsrecords',
    apiKind: 'MikroTikDNSRecord',
    path: '/dns-records',
    label: 'DNS Records',
    singular: 'DNS Record',
    description: 'Static DNS names applied to RouterOS',
    icon: GlobalOutlined,
  },
  {
    slug: 'mikrotikroutes',
    apiKind: 'MikroTikRoute',
    path: '/routes',
    label: 'Routes',
    singular: 'Route',
    description: 'Static routes on connected routers',
    icon: SwapOutlined,
  },
  {
    slug: 'mikrotikportforwards',
    apiKind: 'MikroTikPortForward',
    path: '/port-forwards',
    label: 'Port Forwards',
    singular: 'Port Forward',
    description: 'Destination NAT, masquerade, and forward rules',
    icon: ApiOutlined,
  },
  {
    slug: 'mikrotikfirewallrules',
    apiKind: 'MikroTikFirewallRule',
    path: '/firewall-rules',
    label: 'Firewall Rules',
    singular: 'Firewall Rule',
    description: 'Filter table rules with optional placement',
    icon: SafetyCertificateOutlined,
  },
]

export function kindFromPath(pathname: string): KindConfig | undefined {
  return KINDS.find((kind) => pathname === kind.path || pathname.startsWith(`${kind.path}/`))
}
