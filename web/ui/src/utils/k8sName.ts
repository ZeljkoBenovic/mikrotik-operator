/** Kubernetes metadata.name uses DNS-1123 subdomain rules (RFC 1123). */
export const KUBERNETES_NAME_MAX_LENGTH = 253

const DNS1123_SUBDOMAIN =
  /^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$/

export type SanitizeNameOptions = {
  /** Strip trailing hyphens/dots and re-trim after truncation. Use on blur and submit. */
  finalize?: boolean
}

export function sanitizeKubernetesName(raw: string, options: SanitizeNameOptions = {}): string {
  const finalize = options.finalize === true
  let value = raw.toLowerCase().replace(/[^a-z0-9.-]+/g, '-')
  value = value.replace(/-{2,}/g, '-').replace(/\.{2,}/g, '.')
  value = value.replace(/-+\./g, '.').replace(/\.-+/g, '.')
  value = value.replace(/^[-.]+/, '')
  if (finalize) {
    value = value.replace(/[-.]+$/, '')
  }
  if (value.length > KUBERNETES_NAME_MAX_LENGTH) {
    value = value.slice(0, KUBERNETES_NAME_MAX_LENGTH)
    if (finalize) {
      value = value.replace(/[-.]+$/, '')
    }
  }
  return value
}

export function nameWasAdjusted(raw: string, sanitized: string): boolean {
  return raw.toLowerCase() !== sanitized
}

export function kubernetesNameError(name: string | undefined): string | undefined {
  if (!name) {
    return 'Name is required'
  }
  if (name.length > KUBERNETES_NAME_MAX_LENGTH) {
    return `Name must be at most ${KUBERNETES_NAME_MAX_LENGTH} characters`
  }
  if (!DNS1123_SUBDOMAIN.test(name)) {
    return 'Use lowercase letters, numbers, hyphens, and dots; start and end with a letter or number'
  }
  return undefined
}

export function isKubernetesName(name: string): boolean {
  return kubernetesNameError(name) === undefined
}
