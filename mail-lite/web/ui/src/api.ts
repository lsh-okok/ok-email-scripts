import { reactive } from 'vue'
import { clearMailSession, invalidateAccountMail, invalidateAllMail } from './mailSession'

export interface Account { id: string; email: string; provider: string; mx_domain?: string; mx_retention_days?: number; group_id: string; status: string; client_id?: string; refresh_token?: string; password?: string; proxies?: string[]; note: string; last_refresh?: string; refresh_error?: string; has_credential?: boolean; link_count?: number; version: number; generation: number }
export interface AccountCredentials { id: string; email: string; provider: string; mx_domain?: string; mx_retention_days?: number; group_id: string; group_name: string; status: string; client_id: string; refresh_token: string; password: string; proxies: string[]; effective_proxies: string[]; note: string; last_refresh?: string; refresh_error?: string; link_count: number; version: number; generation: number; access_tokens: { kind: string; access_token: string; expires_at: string; scope?: string }[] }
export interface Group { id: string; name: string; parent_id: string; proxies: string[]; account_count?: number }
export interface Platform { id: string; name: string; sender_emails: string[]; sender_domains: string[]; subject_keywords: string[] }
export interface Link { account_email?: string; account_code?: string; platform_name?: string; json_url?: string; id: string; account_id: string; recipient: string; platform_id: string; expires_at: string; created_at: string; url: string; count: number; page_count: number; json_count: number; last_access_at: string }
export interface Mail { ref: string; subject: string; from: string; to: string; folder: string; received_at: string; preview: string; html?: string; text?: string }
export interface Job { id: string; kind: string; status: string; total: number; done: number; failed: number; errors: string[]; created_at: string }
export interface Node { syncing?: boolean; snapshot_done?: number; snapshot_total?: number; id: string; name: string; token?: string; ip?: string; revoked: boolean; disabled?: boolean; cursor: number; last_seen: string; error: string }
export interface Settings { scan_limit: number; refresh_enabled: boolean; refresh_cron: string; refresh_days: number; public_base_url: string; time_zone: string }
export interface Page<T> { items: T[]; total: number; page: number; page_size: number }
export interface RequestStat { link_id: string; recipient: string; url: string; json_url?: string; account_email?: string; account_code?: string; platform_name?: string; count: number; page_count: number; json_count: number; last_access_at: string }
export const session = reactive({ authenticated: false, loaded: false, csrf: '' })
export async function api<T = { ok: boolean }>(path: string, method = 'GET', data?: unknown): Promise<T> {
 const response = await fetch('/api/' + path, { method, credentials: 'same-origin', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': session.csrf }, body: data === undefined ? undefined : JSON.stringify(data) })
 const body = await response.json().catch(() => ({ error: '服务响应异常' }))
 if (!response.ok) { if (response.status === 401 && path !== 'login') { clearMailSession(); session.authenticated = false }; throw new Error(body.error || `请求失败 (${response.status})`) }
 if (path === 'logout' || path === 'admin/password' && method === 'PUT') { clearMailSession(); session.csrf = ''; session.authenticated = false }
 if (method === 'POST' && path === 'admin/accounts' && body.id) invalidateAccountMail(body.id)
 if (method === 'DELETE' && /^admin\/accounts\/[^/]+$/.test(path)) invalidateAccountMail(decodeURIComponent(path.split('/')[2]!), true)
 if ((method === 'PUT' && path === 'admin/settings') || (method !== 'GET' && /^admin\/groups(?:\/[^/]+)?$/.test(path))) invalidateAllMail()
 return body as T
}
export async function loadSession() { const s = await api<{ authenticated: boolean; csrf: string }>('session'); Object.assign(session, s, { loaded: true }) }
export function query(values: Record<string, string | number | null | undefined>) { const params = new URLSearchParams(); Object.entries(values).forEach(([k, v]) => { if (v !== '' && v !== undefined && v !== null) params.set(k, String(v)) }); return params.toString() }
export const providerName = (provider?: string) => ({outlook:'Outlook',icloud:'iCloud',mx:'MX 域名'}[provider || ''] || provider || '')
export const date = (s?: string) => s ? new Date(s).toLocaleString('zh-CN', { hour12: false }) : '—'
export const lines = (s: string) => s.split(/[\n,，]+/).map(v => v.trim()).filter(Boolean)
export const splitLines = (s?: string[]) => (s || []).join('\n')
export const errorText = (e: unknown) => e instanceof Error ? e.message : '操作失败，请重试'
export function mailboxJSONURL(link: { url: string; json_url?: string }) {
 if (link.json_url) return link.json_url
 // Older fixture responses only have the single-token page URL.
 try { const url = new URL(link.url, window.location.origin); const match = url.pathname.match(/^\/m\/([^/]+)$/); if (match) return `${url.origin}/api/mailboxes/${match[1]}/messages` } catch {}
 return ''
}
export async function copyText(value: string) {
 if (navigator.clipboard) { await navigator.clipboard.writeText(value); return }
 const input = document.createElement('textarea'); input.value = value; input.style.position = 'fixed'; input.style.opacity = '0'; document.body.append(input)
 try { input.select(); if (!document.execCommand('copy')) throw new Error('复制失败，请手动选择文本复制') } finally { input.remove() }
}
export function groupOptions(groups: Group[]) { const out: { label: string; value: string }[] = []; const walk = (parent: string, prefix: string) => groups.filter(g => g.parent_id === parent).forEach(g => { const label = prefix + g.name; out.push({ label, value: g.id }); walk(g.id, label + ' / ') }); walk('', ''); return out }
export function safeEmail(html?: string, text?: string) { const content = html || `<pre style="white-space:pre-wrap;font:15px/1.7 system-ui">${(text || '暂无正文').replace(/[&<>"']/g, c => ({ '&':'&amp;', '<':'&lt;', '>':'&gt;', '"':'&quot;', "'":'&#39;' }[c]!))}</pre>`; return `<!doctype html><html><head><meta name="viewport" content="width=device-width, initial-scale=1"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src 'none'; form-action 'none'; base-uri 'none'"><style>body{font:15px/1.7 system-ui;color:#20242c;background:#fff;padding:12px;overflow-wrap:anywhere}img,table{max-width:100%}a{color:#2463eb}</style></head><body>${content}</body></html>` }
