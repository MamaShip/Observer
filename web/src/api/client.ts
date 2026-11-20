import createClient from 'openapi-fetch'
import type { components, paths } from './schema'

export type Schemas = components['schemas']
export type User = Schemas['User']
/** The current account with this month's notification count. */
export type Me = Schemas['Me']
export type Site = Schemas['Site']
export type Watch = Schemas['Watch']
export type Article = Schemas['Article']
export type WatchDetail = Schemas['WatchDetail']
export type Snapshot = Schemas['Snapshot']
export type Check = Schemas['Check']
export type AdminOverview = Schemas['AdminOverview']
export type AdminDay = Schemas['AdminDay']
export type AdminUser = Schemas['AdminUser']
export type AdminArticle = Schemas['AdminArticle']
export type AdminArticleDetail = Schemas['AdminArticleDetail']
export type AdminSnapshot = Schemas['AdminSnapshot']

/** The API client. Paths are absolute (/api/v1/...); the session cookie is sent automatically. */
export const api = createClient<paths>({ baseUrl: '' })

/** A failed API call, with a message fit to show the user. */
export class ApiError extends Error {
  readonly status: number
  /** Seconds, from a Retry-After header. */
  readonly retryAfter?: number

  constructor(status: number, message: string, retryAfter?: number) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.retryAfter = retryAfter
  }
}

const fallback: Record<number, string> = {
  0: '网络连接失败，请检查网络后重试。',
  401: '请先登录。',
  403: '没有权限进行这个操作。',
  404: '没有找到。',
  429: '操作过于频繁，请稍后再试。',
  503: '服务暂时不可用，请稍后再试。',
}

/** errorFrom builds an ApiError from a response and its parsed problem body. */
export function errorFrom(response: Response, body: unknown): ApiError {
  const detail =
    typeof body === 'object' && body !== null && 'detail' in body && typeof body.detail === 'string'
      ? body.detail
      : ''
  const message =
    detail ||
    fallback[response.status] ||
    (response.status >= 500 ? '服务器出错了，请稍后再试。' : `请求失败（${response.status}）。`)
  const ra = Number.parseInt(response.headers.get('Retry-After') ?? '', 10)
  return new ApiError(response.status, message, Number.isFinite(ra) ? ra : undefined)
}

interface Result<D> {
  data?: D
  error?: unknown
  response: Response
}

/** call awaits an api.* request and returns its data, or throws an ApiError. */
export async function call<D>(request: Promise<Result<D>>): Promise<D> {
  let r: Result<D>
  try {
    r = await request
  } catch {
    throw new ApiError(0, fallback[0]!)
  }
  if (!r.response.ok) {
    throw errorFrom(r.response, r.error)
  }
  return r.data as D
}

/** messageOf turns anything thrown into a message for the user. */
export function messageOf(e: unknown): string {
  if (e instanceof ApiError) return e.message
  return '出了点问题，请刷新页面后重试。'
}
