export interface SalesFolder { id: number; name: string; masters: number; children: number }
export interface SalesMaster { id: number; email: string; folder_id: number; folder: string; note: string; status: string; incomplete: boolean; children: number; sellable: number; reserved: number; sold: number; flagged: number; created_at: string }
export interface SalesChild { id: number; email: string; parent_id: number; parent_email: string; folder: string; link?: string; link_mode: 'source' | 'custom'; source_mapped: boolean; link_available: boolean; link_reason: string; link_platform: string; link_expires_at: string; link_local_only: boolean; note: string; status: string; abnormal: boolean; disabled: boolean; after_sale: boolean; sellable: boolean; position: number; created_at: string; link_check: string; inbox_check: string; outbound_id: number }
export interface SalesPage<T> { items: T[]; total: number; page: number; limit: number }

const statusNames: Record<string, string> = { available: '在库', reserved: '预留', sold: '已售', cancelled: '已取消', partial_return: '部分退库', returned: '已退库', queued: '排队中', running: '运行中', completed: '已完成', stopped: '已停止', failed: '失败', passed: '通过', pending: '待确认', unmeasured: '未检测' }
export const salesStatus = (value: string) => statusNames[value] || value

export function downloadSalesFile(file: { filename: string; content: string }) {
 const url = URL.createObjectURL(new Blob([file.content], { type: 'text/plain;charset=utf-8' }))
 const link = document.createElement('a')
 link.href = url; link.download = file.filename; document.body.append(link); link.click(); link.remove()
 window.setTimeout(() => URL.revokeObjectURL(url), 1000)
}

export function salesRequestKey() {
 const values = new Uint8Array(16)
 globalThis.crypto.getRandomValues(values)
 return Array.from(values, value => value.toString(16).padStart(2, '0')).join('')
}
