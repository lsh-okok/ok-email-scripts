<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { NAlert, NButton, NCard, NForm, NFormItem, NInput, NModal, NSelect, useMessage } from 'naive-ui'
import { api, errorText, lines, splitLines, type Group } from '../api'

const props = defineProps<{ show:boolean; group:Group|null; groups:Group[] }>()
const emit = defineEmits<{ 'update:show':[boolean]; saved:[Group] }>()
const message = useMessage(), busy = ref(false), error = ref(''), proxy = ref('')
const form = reactive<Group>({ id:'', name:'', parent_id:'', proxies:[] })
watch(() => props.show, show => {
 if (!show) return
 const group = props.group
 Object.assign(form, { id:group?.id || '', name:group?.name || '', parent_id:group?.parent_id || '', proxies:[...(group?.proxies || [])] })
 proxy.value = splitLines(group?.proxies); error.value = ''
})
const parentOptions = computed(() => {
 const byId = new Map(props.groups.map(group => [group.id, group]))
 const descendants = new Set<string>()
 const height = (id:string, visited = new Set<string>()):number => {
  if (visited.has(id)) return 4
  visited.add(id); descendants.add(id)
  const children = props.groups.filter(group => group.parent_id === id)
  return 1 + Math.max(0, ...children.map(group => height(group.id, new Set(visited))))
 }
 const subtreeHeight = form.id ? height(form.id) : 1
 const path = (group:Group):string[]|null => {
  const names:string[] = [], seen = new Set<string>()
  let current:Group|undefined = group
  while (current) {
   if (seen.has(current.id)) return null
   seen.add(current.id); names.unshift(current.name)
   if (!current.parent_id) return names
   current = byId.get(current.parent_id)
  }
  return null
 }
 return props.groups.flatMap(group => {
  const names = path(group)
  return !descendants.has(group.id) && names && names.length + subtreeHeight <= 3
   ? [{ label:names.join(' / '), value:group.id }] : []
 })
})
function close(){ if (!busy.value) emit('update:show', false) }
async function save(){
 if (busy.value) return
 const name = form.name.trim(), proxies = lines(proxy.value)
 if (!name){ error.value = '请输入分组名称'; return }
 if (Array.from(name).length > 80){ error.value = '分组名称最多 80 个字符'; return }
 if (proxies.length > 3){ error.value = '代理最多填写 3 个'; return }
 busy.value = true; error.value = ''
 try {
  const saved = await api<Group>('admin/groups', 'POST', { id:form.id, name, parent_id:form.parent_id || '', proxies })
  message.success(form.id ? '分组已修改' : '分组已添加')
  emit('saved', saved); emit('update:show', false)
 } catch(e){ error.value = errorText(e) }
 finally { busy.value = false }
}
</script>

<template>
 <NModal :show="show" :mask-closable="!busy" :close-on-esc="!busy" @update:show="v=>{if(!v)close()}">
  <NCard class="form-modal group-editor" :title="form.id?'修改分组':'添加分组'" :closable="!busy" @close="close">
   <NForm @submit.prevent="save">
    <NFormItem label="分组名称" label-for="group-editor-name" required><NInput v-model:value="form.name" :input-props="{id:'group-editor-name'}" placeholder="例如：业务邮箱" :maxlength="80" :disabled="busy" autofocus /></NFormItem>
    <NFormItem label="上级分组"><NSelect :value="form.parent_id||null" aria-label="上级分组" clearable :options="parentOptions" placeholder="顶级分组" :disabled="busy" @update:value="v=>form.parent_id=v||''" /></NFormItem>
    <NFormItem label="代理（每行一个，留空继承）" label-for="group-editor-proxies"><NInput v-model:value="proxy" :input-props="{id:'group-editor-proxies'}" type="textarea" :rows="3" placeholder="socks5://user:pass@host:1080 或 https://host:8080" :disabled="busy" /></NFormItem>
    <p class="muted group-editor-hint">最多三级分组。邮箱优先使用自己的代理，其次继承分组代理；均为空时直连。代理按顺序尝试，最多 3 个。</p>
    <NAlert v-if="error" type="error" :show-icon="false" class="group-editor-error" role="alert">{{ error }}</NAlert>
    <div class="form-actions"><NButton :disabled="busy" @click="close">取消</NButton><NButton type="primary" attr-type="submit" :loading="busy">保存分组</NButton></div>
   </NForm>
  </NCard>
 </NModal>
</template>

<style>
.group-editor-hint{font-size:12px;line-height:1.7;margin:0 0 16px}
.group-editor-error{margin-bottom:16px}
</style>
