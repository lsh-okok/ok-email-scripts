<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { NAlert, NButton } from 'naive-ui'
import { ArrowUpRight, Mail, PackageCheck, ShoppingCart, Users } from 'lucide-vue-next'
import { api, errorText } from '../api'

type MailStats = { masters:number; children:number; sellable:number; reserved:number; sold:number; flagged:number; orphans:number; incomplete:number }
const props = defineProps<{revision:number}>()
const stats = ref<MailStats>({masters:0,children:0,sellable:0,reserved:0,sold:0,flagged:0,orphans:0,incomplete:0})
const loading = ref(false), error = ref(''), updatedAt = ref('')
let sequence = 0
const cards = computed(() => [
 {label:'销售主邮箱',value:stats.value.masters,icon:Mail,tone:'',tab:'inventory'},
 {label:'销售子邮箱',value:stats.value.children,icon:Users,tone:'neutral',tab:'inventory'},
 {label:'可售库存',value:stats.value.sellable,icon:PackageCheck,tone:'success',tab:'inventory'},
 {label:'预留 / 已售',value:`${stats.value.reserved.toLocaleString()} / ${stats.value.sold.toLocaleString()}`,icon:ShoppingCart,tone:'warning',tab:'outbound'},
])
const distribution = computed(() => [
 {label:'可售',value:stats.value.sellable,tone:'available'},
 {label:'预留',value:stats.value.reserved,tone:'reserved'},
 {label:'已售',value:stats.value.sold,tone:'sold'},
 {label:'其他',value:Math.max(0,stats.value.children-stats.value.sellable-stats.value.reserved-stats.value.sold),tone:'other'},
])
const issues = computed(() => [
 {label:'主邮箱资料待补全',value:stats.value.incomplete},
 {label:'异常或售后标记',value:stats.value.flagged},
 {label:'孤立子邮箱',value:stats.value.orphans},
])
async function load() {
 const current = ++sequence
 loading.value = true; error.value = ''
 try {
  const data = await api<MailStats>('admin/mail/stats')
  if (current === sequence) { stats.value=data; updatedAt.value=new Date().toLocaleTimeString('zh-CN',{hour12:false}) }
 } catch(e) { if (current===sequence) error.value=errorText(e) }
 finally { if(current===sequence) loading.value=false }
}
watch(() => props.revision, load)
onMounted(() => { load(); window.addEventListener('data-changed',load) })
onUnmounted(() => { sequence++; window.removeEventListener('data-changed',load) })
</script>

<template>
 <section class="sales-summary" aria-labelledby="sales-summary-title" :aria-busy="loading">
  <div class="summary-heading"><div><h2 id="sales-summary-title">销售库存概览</h2><p>查看销售库存、交付状态与待处理项。</p></div><div class="summary-heading-actions"><span class="updated-at">{{ loading?'正在更新…':updatedAt?`更新于 ${updatedAt}`:'等待加载' }}</span><RouterLink to="/sales?tab=inventory">管理销售库存 <ArrowUpRight :size="14" /></RouterLink></div></div>
  <NAlert v-if="error" type="error" :show-icon="false" class="summary-error">销售统计加载失败：{{ error }} <NButton text type="error" :loading="loading" @click="load">重试销售统计</NButton></NAlert>
  <div class="sales-summary-stats"><RouterLink v-for="card in cards" :key="card.label" :to="{path:'/sales',query:{tab:card.tab}}" class="stat surface"><div><div class="stat-label">{{ card.label }}</div><div class="stat-number">{{ updatedAt ? typeof card.value==='number' ? card.value.toLocaleString() : card.value : '—' }}</div></div><div class="stat-icon" :class="card.tone"><component :is="card.icon" :size="20" /></div></RouterLink></div>
  <div class="sales-summary-details">
   <section class="surface summary-detail"><div class="summary-detail-heading"><h3>子邮箱库存分布</h3><span>共 {{ updatedAt?stats.children.toLocaleString():'—' }} 个</span></div><div class="stock-bar" aria-hidden="true"><span v-for="item in distribution" :key="item.tone" v-show="item.value>0" :class="item.tone" :style="{flexGrow:item.value}" /></div><div class="stock-legend"><div v-for="item in distribution" :key="item.tone"><span><i :class="item.tone" />{{ item.label }}</span><strong>{{ updatedAt?item.value.toLocaleString():'—' }}</strong></div></div><p class="summary-hint">可售数量已排除来源失效、禁用等不可出库库存。</p></section>
   <section class="surface summary-detail"><div class="summary-detail-heading"><h3>库存待处理</h3><RouterLink to="/sales?tab=inventory">查看库存 <ArrowUpRight :size="14" /></RouterLink></div><div class="inventory-issues"><div v-for="item in issues" :key="item.label"><span>{{ item.label }}</span><strong :class="{attention:item.value>0}">{{ updatedAt?item.value.toLocaleString():'—' }}</strong></div></div><p class="summary-hint">先补全资料与链接，再按需要检测后出库。</p></section>
  </div>
 </section>
</template>

<style scoped>
.sales-summary{margin-bottom:24px;flex-shrink:0;min-width:0}.summary-heading{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:16px}.summary-heading h2{font-size:18px;margin:0 0 5px}.summary-heading p{font-size:12px;color:var(--muted);margin:0}.summary-heading-actions{display:flex;align-items:center;gap:16px;flex-wrap:wrap}.summary-heading a,.summary-detail-heading a{display:inline-flex;align-items:center;gap:4px;font-size:12px;min-height:44px}.updated-at{font-size:11px;color:var(--muted)}.summary-error{margin-bottom:16px}.sales-summary-stats{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:14px;margin-bottom:14px}.sales-summary-stats>.stat{padding:18px 20px;color:var(--text);min-width:0}.sales-summary-stats>.stat:hover{border-color:#3478f680}.sales-summary-stats .stat-number{font-size:28px;font-variant-numeric:tabular-nums;letter-spacing:-.5px;margin-top:5px}.stat-icon{width:40px;height:40px;border-radius:11px;background:#3478f615;color:var(--blue);flex-shrink:0}.stat-icon.neutral{background:var(--hover);color:var(--muted)}.stat-icon.success{background:#2db58a15;color:#239f77}.stat-icon.warning{background:#e9903215;color:#c07820}.sales-summary-details{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:14px}.summary-detail{padding:18px 20px;min-width:0}.summary-detail-heading{display:flex;align-items:center;justify-content:space-between;gap:12px;min-height:32px}.summary-detail-heading h3{font-size:14px;margin:0}.summary-detail-heading>span{font-size:11px;color:var(--muted)}.stock-bar{display:flex;gap:3px;height:12px;border-radius:5px;overflow:hidden;background:var(--hover);margin:20px 0}.stock-bar>span{min-width:0;flex-basis:0}.available{background:#2db58a}.reserved{background:#e6a044}.sold{background:#5987ec}.other{background:#a4afc1}.stock-legend{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:15px 25px}.stock-legend>div{display:flex;align-items:center;justify-content:space-between;gap:8px;font-size:12px}.stock-legend span{display:flex;align-items:center;gap:7px;color:var(--muted)}.stock-legend i{display:block;width:7px;height:7px;border-radius:2px}.stock-legend strong{font-weight:550;font-variant-numeric:tabular-nums}.summary-hint{font-size:11px;color:var(--muted);line-height:1.7;margin:18px 0 0}.inventory-issues{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));margin-top:12px}.inventory-issues>div{display:grid;align-content:start;gap:12px;padding:14px 12px;border-right:1px solid var(--border)}.inventory-issues>div:first-child{padding-left:0}.inventory-issues>div:last-child{border:0}.inventory-issues span{font-size:12px;color:var(--muted);line-height:1.6}.inventory-issues strong{font-size:23px;font-weight:550;font-variant-numeric:tabular-nums}.inventory-issues .attention{color:#c58029}
@media(max-width:1200px){.sales-summary-stats>.stat{padding:16px}.sales-summary-stats .stat-icon{display:none}.sales-summary-details{grid-template-columns:1fr}.inventory-issues span{font-size:12px}}
@media(max-width:767px){.summary-heading{align-items:flex-start;flex-direction:column;gap:6px}.summary-heading-actions{width:100%;justify-content:space-between;gap:8px}.sales-summary-stats{grid-template-columns:repeat(2,minmax(0,1fr));gap:10px}.sales-summary-stats>.stat{padding:14px 16px}.sales-summary-stats .stat-number{font-size:25px}.summary-detail{padding:14px 16px}.inventory-issues>div{padding:10px 8px}.inventory-issues span{font-size:11px}.inventory-issues strong{font-size:21px}}
</style>
