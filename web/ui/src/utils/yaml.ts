import { dump, load } from 'js-yaml'
import type { ResourceObject } from '../api/types'

export function toYAML(value: unknown): string {
  return dump(value, {
    indent: 2,
    lineWidth: 120,
    noRefs: true,
    sortKeys: false,
    skipInvalid: true,
  }).trimEnd()
}

export function fromYAML(text: string): ResourceObject {
  const parsed = load(text)
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new Error('YAML must be a Kubernetes resource object')
  }
  const rec = parsed as Record<string, unknown>
  const metadata = rec.metadata
  if (!metadata || typeof metadata !== 'object' || Array.isArray(metadata)) {
    throw new Error('YAML is missing metadata')
  }
  const spec = rec.spec
  if (!spec || typeof spec !== 'object' || Array.isArray(spec)) {
    throw new Error('YAML is missing spec')
  }
  return rec as ResourceObject
}
