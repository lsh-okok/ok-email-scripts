import { Boxes, CloudUpload, LayoutDashboard, Link2, Send, Settings2, Sparkles } from 'lucide-vue-next'

export const salesSections = [
 { key: 'overview', label: '工作流概览', icon: LayoutDashboard },
 { key: 'inventory', label: '库存管理', icon: Boxes },
 { key: 'mapping', label: '账号映射', icon: Link2 },
 { key: 'outbound', label: '出库记录', icon: Send },
 { key: 'import', label: '导入库存', icon: CloudUpload },
 { key: 'inspection', label: '检测任务', icon: Sparkles },
 { key: 'settings', label: '检测配置', icon: Settings2 },
] as const

export type SalesSection = typeof salesSections[number]['key']
export function resolveSalesSection(value: unknown): SalesSection {
 return salesSections.find(section => section.key === value)?.key || 'overview'
}
export function salesLocation(section: SalesSection) {
 return { path: '/sales', query: section === 'overview' ? {} : { tab: section } }
}
