<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { ChevronRight, ShoppingCart } from 'lucide-vue-next'
import { resolveSalesSection, salesLocation, salesSections } from '../sales-navigation'

const route = useRoute()
const isSales = computed(() => route.path === '/sales')
const active = computed(() => resolveSalesSection(route.query.tab))
const expanded = ref(false)
const storageKey = 'mail-lite-sidebar-sales-expanded'
try { expanded.value = localStorage.getItem(storageKey) === 'true' } catch { /* Storage may be unavailable. */ }
watch(expanded, value => { try { localStorage.setItem(storageKey, String(value)) } catch {} })
watch(isSales, value => { if (value) expanded.value = true }, { immediate: true })
</script>

<template>
 <div class="sidebar-sales">
  <div class="sidebar-sales-heading" :class="{ active: isSales }">
   <RouterLink to="/sales" @click="expanded=true"><ShoppingCart :size="19" /><span>销售工作台</span></RouterLink>
   <button type="button" class="sales-menu-toggle" :aria-label="expanded ? '收起销售工作台子菜单' : '展开销售工作台子菜单'" :aria-expanded="expanded" aria-controls="sidebar-sales-sections" @click="expanded=!expanded"><ChevronRight :size="16" :class="{expanded}" /></button>
  </div>
  <div v-show="expanded" id="sidebar-sales-sections" class="sidebar-sales-sections">
   <RouterLink v-for="section in salesSections" :key="section.key" :to="salesLocation(section.key)" custom v-slot="{href,navigate}">
    <a :href="href" @click="navigate" :class="{selected:isSales && active===section.key}" :aria-current="isSales && active===section.key ? 'page' : undefined"><component :is="section.icon" :size="15" /><span>{{ section.label }}</span></a>
   </RouterLink>
  </div>
 </div>
</template>

<style scoped>
.sidebar-sales{min-width:0}.sidebar-sales-heading{display:flex;align-items:center;border-radius:10px;min-height:48px}.sidebar-sales-heading:hover{background:var(--hover)}.sidebar-sales-heading.active{background:var(--active);color:var(--blue)}.sidebar .sidebar-sales-heading>a{flex:1;min-width:0;padding-right:0;gap:13px;background:transparent}.sidebar-sales-heading>a>svg{flex-shrink:0}.sidebar-sales-heading>a>span{white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.sales-menu-toggle{flex-shrink:0;border:0;background:transparent;color:var(--muted);width:32px;min-height:44px;margin-right:2px;display:grid;place-items:center;border-radius:8px;cursor:pointer}.sales-menu-toggle:hover{background:var(--surface)}.sidebar-sales-heading.active .sales-menu-toggle{color:var(--blue)}.sales-menu-toggle>svg{transition:transform .18s ease}.sales-menu-toggle>svg.expanded{transform:rotate(90deg)}.sidebar-sales-sections{display:grid;gap:2px;margin:6px 0 0 22px;padding-left:7px;border-left:1px solid var(--border)}.sidebar .sidebar-sales-sections>a{min-height:44px;padding:9px 10px;gap:9px;font-size:12px;border-radius:8px}.sidebar-sales-sections>a>svg{flex-shrink:0}.sidebar .sidebar-sales-sections>a.selected{background:var(--active);color:var(--blue);font-weight:600}
@media(prefers-reduced-motion:reduce){.sales-menu-toggle>svg{transition:none}}
</style>
