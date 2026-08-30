export const CONNECTION_STATES = ['new', 'established', 'related', 'invalid', 'untracked']
export const CONNECTION_NAT_STATES = ['srcnat', 'dstnat']
export const FIREWALL_CHAINS = ['input', 'forward', 'output']
export const FIREWALL_ACTIONS = [
  'accept',
  'drop',
  'reject',
  'log',
  'passthrough',
  'fasttrack-connection',
  'jump',
  'return',
]
