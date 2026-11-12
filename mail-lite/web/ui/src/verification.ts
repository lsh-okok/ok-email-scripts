export interface VerificationPage<T> { items: T[]; total: number; page: number; limit: number }
const statusNames: Record<string, string> = { queued: '排队中', running: '运行中', completed: '已完成', stopped: '已停止', failed: '失败', passed: '通过', pending: '待确认', unmeasured: '未检测' }
export const verificationStatus = (value: string) => statusNames[value] || value
export function downloadVerificationFile(file: { filename: string; content: string }) {
 const url = URL.createObjectURL(new Blob([file.content], { type: 'text/plain;charset=utf-8' }))
 const link = document.createElement('a')
 link.href = url; link.download = file.filename; document.body.append(link); link.click(); link.remove()
 window.setTimeout(() => URL.revokeObjectURL(url), 1000)
}
