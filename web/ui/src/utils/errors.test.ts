import { describe, expect, it } from 'vitest'
import { ApiError } from '../api/client'
import { errorMessage } from './errors'

describe('errorMessage', () => {
  it('uses the 409 owned-resource wording', () => {
    expect(errorMessage(new ApiError('resource is owned by Service/web in namespace app', 409))).toContain(
      'Service/web',
    )
    expect(errorMessage(new ApiError('', 409))).toContain('owned by another object')
  })

  it('passes through other API and generic errors', () => {
    expect(errorMessage(new ApiError('unknown resource kind', 400))).toBe('unknown resource kind')
    expect(errorMessage(new Error('boom'))).toBe('boom')
    expect(errorMessage('nope')).toBe('Unexpected error')
  })
})
