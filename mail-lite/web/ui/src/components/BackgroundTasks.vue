<script setup lang="ts">
import { onMounted, onUnmounted } from 'vue'
import { useMessage, type MessageReactive } from 'naive-ui'
import { api, session, type Job } from '../api'

const message = useMessage()
const names: Record<string, string> = { import_accounts: '邮箱导入', import_links: '链接导入', refresh: 'Token 刷新' }
type Tracked = { job: Job; notice: MessageReactive; missing: number }
const tracked = new Map<string, Tracked>(), completed = new Set<string>()
let alive = false, loading = false, reloadRequested = false, failures = 0
let timer: ReturnType<typeof setTimeout> | undefined
let discoverSince: number | undefined
let discoveryVersion = 0

const active = (job: Job) => job.status === 'queued' || job.status === 'running'
const count = (value: number) => Math.max(0, Number(value) || 0).toLocaleString()
function progress(job: Job) {
 const title = names[job.kind] || '后台任务'
 return job.status === 'queued' ? `${title}：排队中` : `${title}：已处理 ${count(job.done)} / ${count(job.total)}，失败 ${count(job.failed)}`
}
function finish(job: Job) {
 if (completed.has(job.id)) return
 completed.add(job.id)
 tracked.get(job.id)?.notice.destroy()
 tracked.delete(job.id)
 const title = names[job.kind] || '后台任务'
 const summary = `成功 ${count(job.done - job.failed)}，失败 ${count(job.failed)}`
 if (job.status === 'failed') message.error(`${title}失败：${summary}。请检查后重试。`, { duration: 8000, closable: true })
 else if (job.status === 'partial_failed' || job.failed > 0) message.warning(`${title}部分失败：${summary}。请检查失败项后重试。`, { duration: 8000, closable: true })
 else message.success(`${title}已完成：处理 ${count(job.done)} 项。`, { duration: 5000, closable: true })
 window.dispatchEvent(new Event('data-changed'))
}
function track(job: Job) {
 if (completed.has(job.id)) return
 if (!active(job)) { finish(job); return }
 const existing = tracked.get(job.id)
 if (existing) { existing.job = job; existing.missing = 0; existing.notice.content = progress(job) }
 else tracked.set(job.id, { job, missing: 0, notice: message.loading(progress(job), { duration: 0, closable: true }) })
}
function clearTracking() {
 for (const entry of tracked.values()) entry.notice.destroy()
 tracked.clear()
 discoverSince = undefined
 if (timer) clearTimeout(timer)
 timer = undefined
}
function schedule() {
 if (!alive || (!tracked.size && discoverSince === undefined)) return
 if (timer) clearTimeout(timer)
 timer = setTimeout(() => { timer = undefined; void load() }, 2500)
}
async function load() {
 if (!alive) return
 if (loading) { reloadRequested = true; return }
 loading = true
 const since = discoverSince, version = discoveryVersion
 try {
  const result = await api<{ items: Job[] }>('admin/jobs')
  if (!alive || !session.authenticated) return
  failures = 0
  const returned = new Set<string>()
  for (const job of result.items) {
   returned.add(job.id)
   const created = Date.parse(job.created_at)
   if (tracked.has(job.id) || (since !== undefined && names[job.kind] && (active(job) || created >= since - 10000))) track(job)
  }
  if (version === discoveryVersion) discoverSince = undefined
  for (const [id, entry] of tracked) {
   if (!returned.has(id) && ++entry.missing >= 3) {
    entry.notice.destroy()
    tracked.delete(id)
    message.warning(`${names[entry.job.kind] || '后台任务'}暂时无法查询进度，请稍后刷新查看结果。`, { duration: 7000, closable: true })
    window.dispatchEvent(new Event('data-changed'))
   }
  }
 } catch {
  if (!alive || !session.authenticated) return
  if (++failures >= 3) {
   clearTracking()
   message.warning('后台任务进度暂时无法获取，请稍后刷新查看结果。', { duration: 7000, closable: true })
  }
 } finally {
  loading = false
  if (alive) {
   if (reloadRequested) { reloadRequested = false; void load() }
   else schedule()
  }
 }
}
function submitted(event: Event) {
 if (!alive) return
 if (timer) clearTimeout(timer)
 timer = undefined
 failures = 0
 const job = (event as CustomEvent<Job>).detail
 if (job && typeof job.id === 'string' && typeof job.status === 'string') track(job)
 else { discoverSince = Date.now(); discoveryVersion++ }
 void load()
}
onMounted(() => { alive = true; window.addEventListener('jobs-changed', submitted) })
onUnmounted(() => { alive = false; window.removeEventListener('jobs-changed', submitted); clearTracking() })
</script>
<template></template>
