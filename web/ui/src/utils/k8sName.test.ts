import { describe, expect, it } from 'vitest'
import {
  KUBERNETES_NAME_MAX_LENGTH,
  isKubernetesName,
  kubernetesNameError,
  nameWasAdjusted,
  sanitizeKubernetesName,
} from './k8sName'

describe('sanitizeKubernetesName', () => {
  it('lowercases and replaces invalid characters', () => {
    expect(sanitizeKubernetesName('My Port Forward')).toBe('my-port-forward')
    expect(sanitizeKubernetesName('Web_App')).toBe('web-app')
    expect(sanitizeKubernetesName('Foo.Bar')).toBe('foo.bar')
  })

  it('collapses repeated separators and strips a leading hyphen', () => {
    expect(sanitizeKubernetesName('--Foo---Bar--')).toBe('foo-bar-')
    expect(sanitizeKubernetesName('foo-.bar')).toBe('foo.bar')
  })

  it('keeps a trailing hyphen while typing and strips it on finalize', () => {
    expect(sanitizeKubernetesName('my-')).toBe('my-')
    expect(sanitizeKubernetesName('my-', { finalize: true })).toBe('my')
    expect(sanitizeKubernetesName('foo.', { finalize: true })).toBe('foo')
    expect(sanitizeKubernetesName('-', { finalize: true })).toBe('')
  })

  it('truncates to the DNS-1123 subdomain limit', () => {
    const raw = `A${'b'.repeat(KUBERNETES_NAME_MAX_LENGTH)}`
    const sanitized = sanitizeKubernetesName(raw, { finalize: true })
    expect(sanitized).toHaveLength(KUBERNETES_NAME_MAX_LENGTH)
    expect(isKubernetesName(sanitized)).toBe(true)
  })
})

describe('kubernetesNameError', () => {
  it('accepts DNS-1123 subdomains', () => {
    expect(kubernetesNameError('app')).toBeUndefined()
    expect(kubernetesNameError('web.frontend.svc')).toBeUndefined()
    expect(kubernetesNameError('kube-system')).toBeUndefined()
    expect(isKubernetesName('a')).toBe(true)
  })

  it('rejects empty, uppercase, underscore, and trailing hyphen', () => {
    expect(kubernetesNameError('')).toBe('Name is required')
    expect(kubernetesNameError('App')).toMatch(/lowercase/)
    expect(kubernetesNameError('web_app')).toMatch(/lowercase/)
    expect(kubernetesNameError('web-')).toMatch(/start and end/)
  })
})

describe('nameWasAdjusted', () => {
  it('ignores case-only changes and flags replaced characters', () => {
    expect(nameWasAdjusted('MyApp', 'myapp')).toBe(false)
    expect(nameWasAdjusted('My App', 'my-app')).toBe(true)
  })
})
