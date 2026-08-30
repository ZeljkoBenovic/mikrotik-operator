import { ApiError } from '../api/client'

export function errorMessage(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 409) {
      return error.message || 'This resource is owned by another object and cannot be changed here.'
    }
    return error.message
  }
  if (error instanceof Error) {
    return error.message
  }
  return 'Unexpected error'
}
