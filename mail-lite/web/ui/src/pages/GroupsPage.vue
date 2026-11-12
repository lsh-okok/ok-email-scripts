<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { NButton, NEmpty, NTag, useMessage } from 'naive-ui'
import { Folder, Plus, RefreshCw, Settings2 } from 'lucide-vue-next'
import { api, errorText, type Group } from '../api'
import Groups from '../components/Groups.vue'
const message=useMessage(), groups=ref<Group[]>([]), unassigned=ref(0), loading=ref(false), manager=ref(false)
const tree=computed(()=>{const out:{group:Group;depth:number}[]=[];const walk=(parent:string,depth:number)=>groups.value.filter(g=>g.parent_id===parent).forEach(g=>{out.push({group:g,depth});walk(g.id,depth+1)});walk('',0);return out})
async function load(){loading.value=true;try{const data=await api<{items:Group[];unassigned_count?:number}>('admin/groups');groups.value=data.items;unassigned.value=data.unassigned_count||0}catch(e){message.error(errorText(e))}finally{loading.value=false}}
function changed(){window.dispatchEvent(new Event('data-changed'))}
onMounted(()=>{load();window.addEventListener('data-changed',load)})
onUnmounted(()=>window.removeEventListener('data-changed',load))
</script>
<template>
 <div class="groups-page"><div class="page-title"><div><div class="page-kicker">邮箱管理 / 分组</div><h1>分组管理</h1><p>管理邮箱分组、层级关系和代理继承规则。</p></div><div class="actions"><NButton quaternary :loading="loading" @click="load"><RefreshCw :size="16" class="icon-before" />刷新</NButton><NButton type="primary" @click="manager=true"><Plus :size="16" class="icon-before" />管理分组</NButton></div></div>
  <div class="groups-summary"><div class="surface group-summary-card"><span class="muted">分组数量</span><strong>{{ groups.length.toLocaleString() }}</strong></div><div class="surface group-summary-card"><span class="muted">未分组邮箱</span><strong>{{ unassigned.toLocaleString() }}</strong></div></div>
  <section class="surface group-list-panel"><div class="section-title"><div><h2>分组列表</h2><p class="muted">分组数量按当前主邮箱实时统计，支持最多三级层级。</p></div><NButton quaternary @click="manager=true"><Settings2 :size="16" class="icon-before" />分组与代理</NButton></div><div v-if="tree.length" class="group-page-list"><article v-for="entry in tree" :key="entry.group.id" class="group-page-row" :style="{paddingLeft:(16 + entry.depth*24)+'px'}"><span class="group-color" :class="'tone-'+entry.depth%4"><Folder :size="16" /></span><div class="group-page-name"><strong>{{ entry.group.name }}</strong><small>{{ entry.group.parent_id ? '子分组' : '顶级分组' }} · {{ entry.group.proxies?.length ? '已配置代理' : '继承或直连' }}</small></div><NTag size="small" :bordered="false">{{ (entry.group.account_count||0).toLocaleString() }} 个邮箱</NTag></article></div><NEmpty v-else description="还没有分组" style="padding:60px 0" /></section>
  <Groups v-model:show="manager" :groups="groups" @changed="changed" />
 </div>
</template>
