<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { NCollapse, NCollapseItem, NProgress, NTag, NAlert } from 'naive-ui'
import { api, type Job } from '../api'
const completed=new Set<string>();
const jobs = ref<Job[]>([])
const names: Record<string,string> = { import_accounts:'邮箱导入',import_links:'链接导入',refresh:'Token 刷新',scheduled_refresh:'定时刷新' }
const statuses: Record<string,string> = {queued:'排队中',running:'进行中',completed:'已完成',failed:'失败',partial_failed:'部分失败'}
let timer: ReturnType<typeof setInterval>
async function load() { try { jobs.value = (await api<{items:Job[]}>('admin/jobs')).items.slice(0, 5); for(const j of jobs.value){if(!['queued','running'].includes(j.status)&&!completed.has(j.id)){completed.add(j.id);window.dispatchEvent(new Event('data-changed'))}} } catch { /* Session errors handled centrally. */ } }
onMounted(() => { load(); timer=setInterval(load, 2500); window.addEventListener('jobs-changed',load) });onUnmounted(() => {clearInterval(timer);window.removeEventListener('jobs-changed',load)})
</script>
<template><section v-if="jobs.length" class="jobs surface"><NCollapse><NCollapseItem name="jobs"><template #header><span>最近任务 <span class="muted">{{ jobs.filter(j=>j.status==='running'||j.status==='queued').length ? '· 有任务正在执行' : '· 查看处理结果' }}</span></span></template><div v-for="j in jobs" :key="j.id" class="job"><div class="row between"><strong>{{ names[j.kind] || j.kind }}</strong><NTag size="small" :bordered="false" :type="j.failed?'warning':j.status==='completed'?'success':'info'">{{ statuses[j.status] }}</NTag></div><NProgress type="line" :percentage="j.total?Math.round(j.done/j.total*100):0" :processing="j.status==='running'" :show-indicator="false" /><small class="muted">已处理 {{ j.done.toLocaleString() }} / {{ j.total.toLocaleString() }} 行 / 项，失败 {{ j.failed }}</small><NAlert v-if="j.errors?.length" type="warning" :show-icon="false"><div v-for="(e,i) in j.errors" :key="i">{{ e }}</div></NAlert></div></NCollapseItem></NCollapse></section></template>
