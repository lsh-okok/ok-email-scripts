<script setup lang="ts">
import { computed, h, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NButton, NDropdown, useDialog, useMessage, type DropdownOption } from 'naive-ui'
import { ChevronRight, Ellipsis, Folder, Mail, Pencil, Plus, RefreshCw, Trash2 } from 'lucide-vue-next'
import { api, errorText, type Group } from '../api'
import GroupEditor from './GroupEditor.vue'

const route = useRoute(), router = useRouter(), dialog = useDialog(), message = useMessage()
const groups = ref<Group[]>([]), unassigned = ref(0), error = ref(''), loading = ref(false)
const editorOpen = ref(false), editingGroup = ref<Group|null>(null), deleting = ref('')
const menuOpen = ref(false), menuGroup = ref<Group|null>(null), menuX = ref(0), menuY = ref(0)
const expansionKey = 'mail-lite-sidebar-accounts-expanded', collapsedKey = 'mail-lite-sidebar-collapsed-groups'
const expanded = ref(true), collapsedGroups = ref<string[]>([])
try {
 expanded.value = localStorage.getItem(expansionKey) !== 'false'
 const saved: unknown = JSON.parse(localStorage.getItem(collapsedKey) || '[]')
 if (Array.isArray(saved)) collapsedGroups.value = saved.filter((id): id is string => typeof id === 'string')
} catch { /* Use the default expansion when browser storage is unavailable. */ }
watch(expanded, value => { try { localStorage.setItem(expansionKey, String(value)) } catch {} })
watch(collapsedGroups, value => { try { localStorage.setItem(collapsedKey, JSON.stringify(value)) } catch {} })
let serial = 0
const selected = computed(() => route.path === '/accounts' ? route.query.group : null)
const parents = computed(() => new Set(groups.value.map(group => group.parent_id).filter(Boolean)))
const entries = computed(() => {
 const result: { group: Group; depth: number }[] = [], visited = new Set<string>()
 const walk = (parent: string, depth: number) => groups.value.filter(g => g.parent_id === parent).forEach(group => {
  if (visited.has(group.id)) return
  visited.add(group.id); result.push({ group, depth })
  if (!collapsedGroups.value.includes(group.id)) walk(group.id, depth + 1)
 })
 walk('', 0)
 return result
})
const menuOptions = computed<DropdownOption[]>(() => [
 ...(!menuGroup.value ? [{ label:'未分组为系统分类', key:'system', disabled:true }, { type:'divider' as const, key:'divider' }] : []),
 { label:'修改分组', key:'edit', disabled:!menuGroup.value || !!deleting.value, icon:() => h(Pencil,{size:16}) },
 { label:'删除分组', key:'delete', disabled:!menuGroup.value || !!deleting.value, icon:() => h(Trash2,{size:16}) }
])
function closeMenu(){ menuOpen.value = false }
function toggleExpanded(){ closeMenu(); expanded.value = !expanded.value }
function toggleGroup(id:string){
 closeMenu()
 collapsedGroups.value = collapsedGroups.value.includes(id) ? collapsedGroups.value.filter(value => value !== id) : [...collapsedGroups.value,id]
}
function revealSelected(){
 if (typeof selected.value !== 'string') return
 expanded.value = true
 const byId = new Map(groups.value.map(group => [group.id,group])), ancestors = new Set<string>()
 let current = byId.get(selected.value)
 while (current?.parent_id && !ancestors.has(current.parent_id)) {
  ancestors.add(current.parent_id); current = byId.get(current.parent_id)
 }
 collapsedGroups.value = collapsedGroups.value.filter(id => !ancestors.has(id))
}
function openMenu(event:MouseEvent, group:Group|null){
 const rect = (event.currentTarget as HTMLElement).getBoundingClientRect()
 const atPointer = event.type === 'contextmenu' && (event.clientX !== 0 || event.clientY !== 0)
 menuX.value = atPointer ? event.clientX : rect.left
 menuY.value = atPointer ? event.clientY : rect.bottom
 menuGroup.value = group; menuOpen.value = true
}
function createGroup(){ closeMenu(); expanded.value = true; editingGroup.value = null; editorOpen.value = true }
function changed(){ window.dispatchEvent(new Event('data-changed')) }
function chooseMenu(key:string){
 const group = menuGroup.value
 closeMenu()
 if (!group || deleting.value) return
 if (key === 'edit'){ editingGroup.value = group; editorOpen.value = true }
 if (key === 'delete'){
  if ((group.account_count || 0) > 0 || groups.value.some(g => g.parent_id === group.id)){
   dialog.info({ title:'暂时无法删除分组', content:`“${group.name}”中还有邮箱或下级分组，请先移出后再删除。`, positiveText:'知道了' })
   return
  }
  dialog.warning({
   title:'删除分组', content:`确定删除“${group.name}”？`, positiveText:'删除', negativeText:'取消',
   onPositiveClick:async()=>{
    if (deleting.value) return false
    deleting.value = group.id
    try {
     await api('admin/groups/'+encodeURIComponent(group.id),'DELETE')
     if (route.path === '/accounts' && route.query.group === group.id) await router.replace({ query:{ ...route.query, group:undefined } })
     message.success('分组已删除'); changed(); return true
    } catch(e){ message.error(errorText(e)); return false }
    finally { deleting.value = '' }
   }
  })
 }
}
async function load(){
 const seq = ++serial; loading.value = true
 try {
  const data = await api<{ items:Group[]; unassigned_count:number }>('admin/groups')
  if (seq !== serial) return
  groups.value = data.items; unassigned.value = data.unassigned_count || 0; error.value = ''
  if (menuGroup.value && !groups.value.some(g => g.id === menuGroup.value?.id)) closeMenu()
 } catch(e){ if (seq === serial) error.value = errorText(e) }
 finally { if (seq === serial) loading.value = false }
}
function onKeydown(event:KeyboardEvent){ if (event.key === 'Escape') closeMenu() }
watch(() => route.fullPath, () => { closeMenu(); revealSelected() }, {immediate:true})
watch(() => groups.value.length > 0, available => { if (available) revealSelected() })
onMounted(() => { load(); window.addEventListener('data-changed',load); window.addEventListener('keydown',onKeydown) })
onUnmounted(() => { serial++; window.removeEventListener('data-changed',load); window.removeEventListener('keydown',onKeydown) })
</script>

<template>
 <div class="sidebar-account-nav" :class="{active:route.path==='/accounts'}">
  <RouterLink to="/accounts"><Mail :size="19" /><span>邮箱管理</span></RouterLink>
  <NButton quaternary aria-label="添加分组" title="添加分组" :disabled="loading" @click="createGroup"><Plus :size="17" /></NButton>
  <NButton class="sidebar-menu-toggle" quaternary :aria-label="expanded?'收起邮箱分组':'展开邮箱分组'" :title="expanded?'收起邮箱分组':'展开邮箱分组'" :aria-expanded="expanded" aria-controls="sidebar-mail-groups" @click="toggleExpanded"><ChevronRight :size="16" class="sidebar-chevron" :class="{'is-expanded':expanded}" /></NButton>
 </div>
 <Transition name="sidebar-fold">
 <div v-show="expanded" id="sidebar-mail-groups" class="sidebar-groups" aria-label="邮箱分组">
  <div v-if="error" class="sidebar-groups-error"><span :title="error">分组加载失败</span><NButton quaternary aria-label="重试加载分组" :loading="loading" @click="load"><RefreshCw :size="14" /></NButton></div>
  <div v-else class="sidebar-group-list">
   <div class="sidebar-group-row" :class="{active:selected==='__ungrouped__'}" @contextmenu.prevent.stop="openMenu($event,null)">
    <RouterLink :to="{path:'/accounts',query:{group:'__ungrouped__'}}" custom v-slot="{href,navigate}"><a :href="href" :aria-current="selected==='__ungrouped__'?'page':undefined" @click="navigate"><Folder :size="14" /><span>未分组</span></a></RouterLink>
    <NButton class="sidebar-group-menu" quaternary aria-label="未分组菜单" title="分组菜单" aria-haspopup="menu" :aria-expanded="menuOpen&&!menuGroup" @click.stop="openMenu($event,null)"><Ellipsis :size="15" /></NButton>
    <small>{{ unassigned.toLocaleString() }}</small>
   </div>
   <div v-for="entry in entries" :key="entry.group.id" class="sidebar-group-row" :class="{active:selected===entry.group.id,'has-children':parents.has(entry.group.id)}" @contextmenu.prevent.stop="openMenu($event,entry.group)">
    <NButton v-if="parents.has(entry.group.id)" class="sidebar-group-toggle" quaternary :style="{left:(4+entry.depth*8)+'px'}" :aria-label="(collapsedGroups.includes(entry.group.id)?'展开':'收起')+'分组 '+entry.group.name" :title="(collapsedGroups.includes(entry.group.id)?'展开':'收起')+'分组 '+entry.group.name" :aria-expanded="!collapsedGroups.includes(entry.group.id)" @click.stop="toggleGroup(entry.group.id)"><ChevronRight :size="13" class="sidebar-chevron" :class="{'is-expanded':!collapsedGroups.includes(entry.group.id)}" /></NButton>
    <RouterLink :to="{path:'/accounts',query:{group:entry.group.id}}" custom v-slot="{href,navigate}"><a :href="href" :aria-current="selected===entry.group.id?'page':undefined" :style="{paddingLeft:(8+entry.depth*8)+'px'}" :title="entry.group.name" @click="navigate"><Folder :size="14" /><span>{{ entry.group.name }}</span></a></RouterLink>
    <NButton class="sidebar-group-menu" quaternary :aria-label="'分组菜单 '+entry.group.name" title="分组菜单" aria-haspopup="menu" :aria-expanded="menuOpen&&menuGroup?.id===entry.group.id" @click.stop="openMenu($event,entry.group)"><Ellipsis :size="15" /></NButton>
    <small>{{ (entry.group.account_count||0).toLocaleString() }}</small>
   </div>
  </div>
 </div>
 </Transition>
 <NDropdown trigger="manual" :show="menuOpen" :x="menuX" :y="menuY" :options="menuOptions" placement="bottom-start" @select="chooseMenu" @clickoutside="closeMenu" @update:show="v=>menuOpen=v" />
 <GroupEditor v-model:show="editorOpen" :group="editingGroup" :groups="groups" @saved="changed" />
</template>

<style>
.sidebar nav{min-height:0;overflow-y:auto;overscroll-behavior:contain;scrollbar-width:thin}
.sidebar-account-nav{display:flex;align-items:center;min-width:0;border-radius:10px}
.sidebar-account-nav.active{background:var(--active)}
.sidebar nav .sidebar-account-nav>a{flex:1;min-width:0;gap:8px;padding:13px 0 13px 12px;white-space:nowrap}
.sidebar-account-nav>a>svg{flex-shrink:0}
.sidebar-account-nav>a>span{overflow:hidden;text-overflow:ellipsis}
.sidebar-account-nav>.n-button{flex-shrink:0;min-width:30px;width:30px;padding:0;color:var(--muted)}
.sidebar-account-nav>.sidebar-menu-toggle{margin-right:2px}
.sidebar-account-nav.active>.n-button{color:var(--blue)}
.sidebar-chevron{transition:transform .18s ease}
.sidebar-chevron.is-expanded{transform:rotate(90deg)}
.sidebar-fold-enter-active,.sidebar-fold-leave-active{transition:opacity .15s ease,transform .15s ease}
.sidebar-fold-enter-from,.sidebar-fold-leave-to{opacity:0;transform:translateY(-4px)}
.sidebar-groups{margin:-7px 0 3px 8px;min-width:0;border-left:1px solid var(--border);padding-left:5px}
.sidebar-groups-error{display:flex;align-items:center;justify-content:space-between;gap:5px;padding-left:12px;font-size:11px;color:var(--muted);min-height:44px}
.sidebar-groups-error .n-button{min-width:44px;padding:0}
.sidebar .sidebar-group-list{display:grid;gap:2px}
.sidebar-group-row{position:relative;display:flex;align-items:center;min-width:0;min-height:44px;padding-right:5px;border-radius:7px}
.sidebar-group-row:hover{background:var(--hover)}
.sidebar-group-row.active{background:var(--active);color:var(--blue)}
.sidebar .sidebar-group-row>a{flex:1;min-width:0;gap:7px;min-height:44px;padding:8px 0 8px 8px;font-size:12px;border-radius:7px}
.sidebar-group-row>a>svg{flex-shrink:0}
.sidebar-group-row>a>span{flex:1;min-width:0;overflow:hidden;white-space:nowrap;text-overflow:ellipsis}
.sidebar-group-row.has-children>a>svg{visibility:hidden}
.sidebar-group-row>.sidebar-group-toggle{position:absolute;top:0;min-width:22px;width:22px;height:44px;padding:0;color:inherit;z-index:1}
.sidebar-group-row>.sidebar-group-menu{flex-shrink:0;min-width:44px;width:44px;padding:0;color:var(--muted)}
.sidebar-group-row>small{flex-shrink:0;min-width:14px;text-align:right;font-size:10px;font-weight:400;font-variant-numeric:tabular-nums;color:var(--muted)}
.sidebar .sidebar-group-row.active>a{color:var(--blue);font-weight:600}
@media(prefers-reduced-motion:reduce){.sidebar-chevron,.sidebar-fold-enter-active,.sidebar-fold-leave-active{transition:none}}
</style>
