<script setup lang="ts">
import { computed, onActivated, onDeactivated, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { NButton, NInput, NInputNumber, NSelect, NPagination, NCheckbox, NModal, NCard, NForm, NFormItem, NAlert, NEmpty, useMessage, useDialog } from 'naive-ui'
import { Plus, Upload, Download, Search, SlidersHorizontal, Mail, RefreshCw, Copy, CircleAlert, Trash2 } from 'lucide-vue-next'
import { api, query, providerName, errorText, lines, splitLines, groupOptions, copyText, type Account, type AccountCredentials, type Group, type Page, type Job } from '../api'
import ImportForm from '../components/ImportForm.vue'
import MailWorkspace from '../components/MailWorkspace.vue'
import AccountExport from '../components/AccountExport.vue'
import { reconcileMailAccount } from '../mailSession'

defineOptions({name:'Accounts'})

const message = useMessage(), dialog = useDialog()
const route = useRoute(), router = useRouter()
const items = ref<Account[]>([]), groups = ref<Group[]>([]), total = ref(0), page = ref(1), size = ref(20)
const search = ref(''), provider = ref(''), status = ref('')
const routeGroup=ref(typeof route.query.group==='string'?route.query.group:'')
watch(() => [route.path,route.query.group],()=>{if(route.path==='/accounts')routeGroup.value=typeof route.query.group==='string'?route.query.group:''})
const group = computed({ get: () => routeGroup.value, set: (value: string) => { router.replace({ query: { ...route.query, group: value || undefined } }) } })
const loading = ref(false), error = ref(''), selected = ref<string[]>([]), filters = ref(false)
const importing = ref(false), editing = ref(false), saving = ref(false), credentialLoading = ref(false), credentialError = ref('')
const deletingAccounts = ref(false), deleteProgress = ref({done:0,total:0})
const deleteReport = ref<{total:number;success:number;failures:{id:string;email:string;error:string}[]}|null>(null)
const activeID = ref(''), pane = ref<'accounts'|'messages'|'detail'>('accounts'), proxy = ref('')
const activeAccount = computed(() => items.value.find(a=>a.id===activeID.value)||null)
const exporting = ref(false)
const stats = ref({accounts:0,links:0,outlook_accounts:0,icloud_accounts:0,active_accounts:0,disabled_accounts:0})
function emptyAccount(): Account { return {id:'',email:'',provider:'outlook',mx_domain:'',mx_retention_days:30,group_id:'',status:'active',client_id:'',refresh_token:'',password:'',proxies:[],note:'',version:0,generation:0} }
const form = reactive<Account>(emptyAccount())
const groupOpts = computed(() => [{label:'未分组',value:''}, ...groupOptions(groups.value)])
const groupFilterOpts = computed(() => [{label:'全部邮箱',value:''},{label:'未分组',value:'__ungrouped__'},...groupOptions(groups.value)])
const selectedOutlook = computed(() => items.value.filter(a => selected.value.includes(a.id) && a.provider==='outlook' && a.status==='active').map(a => a.id))
const hasFilters = computed(() => !!(search.value || provider.value || group.value || status.value))
let searchTimer: ReturnType<typeof setTimeout>, serial = 0
let credentialSerial = 0
const params = () => ({q:search.value,provider:provider.value||'',group_id:group.value||'',status:status.value||''})
async function load() {
 const seq=++serial; loading.value=true; error.value=''
 try { const data=await api<Page<Account>>('admin/accounts?'+query({...params(),page:page.value,page_size:size.value})); if(seq!==serial)return; data.items.forEach(reconcileMailAccount);items.value=data.items;total.value=data.total;selected.value=selected.value.filter(id=>data.items.some(a=>a.id===id));if(!data.items.some(a=>a.id===activeID.value))activeID.value=data.items[0]?.id||'' }
 catch(e){if(seq===serial)error.value=errorText(e)}finally{if(seq===serial)loading.value=false}
}
async function loadExtras() {
 try { const [g,s]=await Promise.all([api<{items:Group[]}>('admin/groups'),api<typeof stats.value>('admin/stats')]);groups.value=g.items;stats.value=s }
 catch(e){message.error(errorText(e))}
}
async function reload(){await Promise.all([load(),loadExtras()])}
function resetPage(){if(page.value===1)load();else page.value=1}
watch(search,()=>{clearTimeout(searchTimer);searchTimer=setTimeout(resetPage,300)})
watch([provider,group,status,size],resetPage);watch(page,load)
watch(group,()=>{pane.value='accounts'})
const importLine = computed(() => {
 const email = form.provider==='mx' ? `postmaster@${form.mx_domain || ''}` : form.email || ''
 if(form.provider==='mx')return `${email}----MX----${form.mx_retention_days ?? 30}`
 if (form.provider === 'outlook') return `${email}----${form.password || ''}----${form.client_id || ''}----${form.refresh_token || ''}`
 return `${email}----${form.password || ''}`
})
async function loadEditCredentials(id: string) {
 const current = ++credentialSerial
 credentialLoading.value = true
 credentialError.value = ''
 try {
  const data = await api<AccountCredentials>(`admin/accounts/${encodeURIComponent(id)}/credentials`, 'POST', {})
  if (current !== credentialSerial || form.id !== id) return
  Object.assign(form, { email: data.email, provider: data.provider, mx_domain:data.mx_domain, mx_retention_days:data.mx_retention_days ?? 30, group_id: data.group_id, status: data.status, client_id: data.client_id, refresh_token: data.refresh_token, password: data.password, note: data.note, version: data.version, generation: data.generation })
  proxy.value = splitLines(data.proxies)
 } catch (e) {
  if (current !== credentialSerial || form.id !== id) return
  credentialError.value = errorText(e)
 } finally {
  if (current === credentialSerial) credentialLoading.value = false
 }
}
function edit(a?:Account){
 credentialSerial++
 credentialLoading.value = false
 credentialError.value = ''
 Object.assign(form,emptyAccount(),a?{...a,refresh_token:'',password:''}:{})
 proxy.value=splitLines(a?.proxies)
 editing.value=true
 if (a?.id) void loadEditCredentials(a.id)
}
async function save(){
 if (saving.value || credentialLoading.value) return
 saving.value=true
 try {
  const payload={id:form.id,email:form.provider==='mx'?`postmaster@${form.mx_domain || ''}`:form.email,provider:form.provider,mx_domain:form.provider==='mx'?form.mx_domain:undefined,mx_retention_days:form.provider==='mx'?form.mx_retention_days:undefined,status:form.status,group_id:form.group_id||'',client_id:form.client_id,refresh_token:form.refresh_token,password:form.password,note:form.note,version:form.version,generation:form.generation,proxies:lines(proxy.value)}
  const saved=await api<Account>('admin/accounts','POST',payload);reconcileMailAccount(saved);items.value=items.value.map(account=>account.id===saved.id?saved:account);message.success('邮箱已保存');editing.value=false;dataChanged()
 }catch(e){message.error(errorText(e))}finally{saving.value=false}
}
function remove(value:Account|Account[]){
 const accounts=Array.isArray(value)?[...value]:[value]
 if(!accounts.length||deletingAccounts.value)return
 const scope=accounts.length===1?accounts[0]!.email:`当前页选中的 ${accounts.length} 个邮箱`
 dialog.warning({title:accounts.length===1?'删除邮箱':`删除 ${accounts.length} 个邮箱`,content:`将删除 ${scope} 及其子邮箱、访问链接和统计，操作无法撤销。`,positiveText:'确认删除',negativeText:'取消',onPositiveClick:()=>deleteAccounts(accounts)})
}
async function deleteAccounts(accounts:Account[]){
 if(deletingAccounts.value)return
 deletingAccounts.value=true;deleteReport.value=null;deleteProgress.value={done:0,total:accounts.length};serial++;loading.value=false
 const failures:{id:string;email:string;error:string}[]=[]
 let success=0
 try {
  for(const account of accounts){
   try {
    await api('admin/accounts/'+encodeURIComponent(account.id),'DELETE')
    success++;serial++;loading.value=false
    items.value=items.value.filter(a=>a.id!==account.id);selected.value=selected.value.filter(id=>id!==account.id);total.value=Math.max(0,total.value-1)
    if(activeID.value===account.id)activeID.value=items.value[0]?.id||''
   }catch(e){
    failures.push({id:account.id,email:account.email,error:errorText(e)})
   }finally{deleteProgress.value.done++}
  }
  selected.value=[...new Set([...selected.value,...failures.map(f=>f.id)])].filter(id=>items.value.some(a=>a.id===id))
  deleteReport.value={total:accounts.length,success,failures}
  if(success)message.success(`已删除 ${success} 个主邮箱及其关联子邮箱和访问链接`)
  if(failures.length)message.warning(`${failures.length} 个邮箱未删除，已保留并显示原因`)
  if(success){page.value=Math.min(page.value,Math.max(1,Math.ceil(total.value/size.value)));dataChanged()}
 }finally{deletingAccounts.value=false}
}
function removeSelected(){remove(items.value.filter(a=>selected.value.includes(a.id)))}
async function refresh(ids:string[]){try{const job=await api<Job>('admin/refresh','POST',{ids});message.success(ids.length?`已提交 ${ids.length} 个邮箱的刷新任务`:'已提交全部 Outlook 邮箱的刷新任务');window.dispatchEvent(new CustomEvent('jobs-changed',{detail:job}))}catch(e){message.error(errorText(e))}}
function refreshAll(){dialog.info({title:'刷新全部 Token',content:`将刷新全部 ${stats.value.outlook_accounts} 个 Outlook 邮箱中已启用的账号，并在后台执行。`,positiveText:'开始刷新',negativeText:'取消',onPositiveClick:()=>refresh([])})}
function openExport(){exporting.value=true}
function setSelected(id:string,v:boolean){selected.value=v?[...selected.value,id]:selected.value.filter(k=>k!==id)}
function clearFilters(){search.value='';provider.value='';group.value='';status.value=''}
async function copyActiveEmail(){const email=activeAccount.value?.email;if(!email)return;try{await copyText(email);message.success('邮箱地址已复制')}catch(e){message.error(errorText(e))}}
function dataChanged(){window.dispatchEvent(new Event('data-changed'))}
function chooseAccount(a:Account){activeID.value=a.id;pane.value='messages'}
function accountStatus(a:Account){return a.status==='disabled'?'已停用':a.refresh_error?'授权异常':'已启用'}
onMounted(()=>{reload();window.addEventListener('data-changed',reload)})
let activatedOnce=false
onActivated(()=>{if(activatedOnce)void load();activatedOnce=true})
onDeactivated(()=>{credentialSerial++;credentialLoading.value=false;credentialError.value='';editing.value=false;importing.value=false;exporting.value=false;Object.assign(form,emptyAccount());proxy.value=''})
onUnmounted(()=>{serial++;clearTimeout(searchTimer);window.removeEventListener('data-changed',reload)})
</script>
<template>
 <div class="inbox-page">
  <div class="page-title inbox-page-title"><h1>邮箱管理</h1><div class="current-mailbox"><button v-if="activeAccount" type="button" class="current-mailbox-copy" :aria-label="'复制当前邮箱 '+activeAccount.email" :title="'点击复制邮箱：'+activeAccount.email" @click="copyActiveEmail"><span>{{ activeAccount.email }}</span><Copy :size="16" /></button></div><div class="actions"><NButton @click="openExport()"><Download :size="16" class="icon-before" />导出邮箱</NButton><NButton :disabled="!stats.outlook_accounts" @click="refreshAll"><RefreshCw :size="16" class="icon-before" />刷新全部 Token</NButton><NButton @click="importing=true"><Upload :size="16" class="icon-before" />批量导入</NButton><NButton type="primary" @click="edit()"><Plus :size="17" class="icon-before" />添加邮箱</NButton></div></div>
  <div class="inbox-mobile-tabs" role="tablist" aria-label="邮箱工作区"><button role="tab" :aria-selected="pane==='accounts'" :class="{active:pane==='accounts'}" @click="pane='accounts'">邮箱 <span>{{ total }}</span></button><button role="tab" :aria-selected="pane==='messages'" :class="{active:pane==='messages'}" @click="pane='messages'">邮件</button><button role="tab" :aria-selected="pane==='detail'" :class="{active:pane==='detail'}" @click="pane='detail'">详情</button></div>
  <NAlert v-if="deleteReport" class="account-delete-report" :type="deleteReport.failures.length?'warning':'success'" :show-icon="false" closable @close="deleteReport=null"><p role="status">共 {{ deleteReport.total }} 个主邮箱：已删除 {{ deleteReport.success }} 个主邮箱及其关联子邮箱和访问链接，失败 {{ deleteReport.failures.length }} 个。</p><ul v-if="deleteReport.failures.length"><li v-for="failure in deleteReport.failures" :key="failure.id"><strong>{{ failure.email }}</strong><span>{{ failure.error }}</span></li></ul></NAlert>
  <section class="surface inbox-workbench" aria-label="邮箱邮件详情工作区">
   <aside class="mail-lane account-lane" :class="{'lane-active':pane==='accounts'}" aria-label="邮箱">
    <header class="lane-heading"><div class="lane-heading-title"><Mail :size="17" /><h2>邮箱</h2><span class="lane-count">{{ total.toLocaleString() }}</span></div><div class="row"><NButton quaternary type="error" :disabled="!activeAccount||deletingAccounts" aria-label="删除当前邮箱" title="删除当前邮箱" @click="activeAccount&&remove(activeAccount)"><Trash2 :size="15" class="icon-before" />删除</NButton><NButton quaternary aria-label="筛选邮箱" :type="filters?'primary':'default'" @click="filters=!filters"><SlidersHorizontal :size="16" /></NButton></div></header>
    <div class="inbox-account-search"><NInput v-model:value="search" :disabled="deletingAccounts" clearable placeholder="搜索邮箱地址" aria-label="搜索邮箱"><template #prefix><Search :size="15" /></template></NInput><NButton quaternary aria-label="刷新邮箱列表" :disabled="deletingAccounts||loading" :aria-busy="loading" @click="reload"><RefreshCw :size="15" class="refresh-icon" :class="{spinning:loading}" /></NButton></div>
    <div v-if="filters" class="inbox-account-filters"><NSelect v-model:value="provider" :disabled="deletingAccounts" clearable aria-label="邮箱类型" :options="[{label:'全部类型',value:''},{label:'Outlook',value:'outlook'},{label:'iCloud',value:'icloud'},{label:'MX 域名收信',value:'mx'}]" placeholder="全部类型" /><NSelect v-model:value="status" :disabled="deletingAccounts" clearable aria-label="邮箱状态" :options="[{label:'全部状态',value:''},{label:'已启用',value:'active'},{label:'已停用',value:'disabled'}]" placeholder="全部状态" /><NSelect v-model:value="group" :disabled="deletingAccounts" :options="groupFilterOpts" aria-label="邮箱分组" /><NSelect v-model:value="size" :disabled="deletingAccounts" aria-label="每页邮箱数量" :options="[{label:'20 / 页',value:20},{label:'50 / 页',value:50},{label:'100 / 页',value:100}]" /><NButton v-if="hasFilters" text :disabled="deletingAccounts" @click="clearFilters">重置筛选</NButton></div>
    <div class="account-selection-row"><NCheckbox :checked="!!items.length&&selected.length===items.length" :indeterminate="!!selected.length&&selected.length!==items.length" :disabled="!items.length||loading||deletingAccounts" @update:checked="v=>selected=v?items.map(a=>a.id):[]">{{ selected.length?'本页已选 '+selected.length+' 个':'选择当前页' }}</NCheckbox><div class="row" v-if="selected.length"><NButton quaternary :disabled="!selectedOutlook.length||deletingAccounts" aria-label="刷新所选 Token" @click="refresh(selectedOutlook)"><RefreshCw :size="15" /></NButton></div><span v-else class="muted">{{ items.length }} 个邮箱</span></div>
    <div v-if="selected.length||deletingAccounts" class="account-bulk-delete-row"><span>{{ deletingAccounts?'正在删除 '+deleteProgress.done+' / '+deleteProgress.total:'仅处理当前页所选邮箱' }}</span><NButton secondary type="error" :loading="deletingAccounts" :disabled="!selected.length||deletingAccounts" @click="removeSelected"><Trash2 :size="15" class="icon-before" />删除所选</NButton></div>
    <div class="lane-scroll account-rail-list" :aria-busy="loading">
     <NAlert v-if="error" type="error" :show-icon="false">{{ error }}<NButton text @click="load">重试</NButton></NAlert>
     <article v-for="a in items" :key="a.id" class="inbox-account" :class="{active:activeID===a.id}"><NCheckbox :checked="selected.includes(a.id)" :disabled="deletingAccounts" :aria-label="'选择邮箱 '+a.email" @update:checked="v=>setSelected(a.id,v)" /><button class="inbox-account-open" :aria-label="'打开邮箱 '+a.email" :aria-pressed="activeID===a.id" @click="chooseAccount(a)"><span class="inbox-account-email">{{ a.email }}</span><span class="inbox-account-meta"><span class="provider-label" :class="a.provider">{{ providerName(a.provider) }}</span><span>{{ groupOpts.find(g=>g.value===a.group_id)?.label||'未分组' }}</span><span>{{ (a.link_count||0).toLocaleString() }} 链接</span></span><span class="inbox-account-status" :class="{warning:!!a.refresh_error,disabled:a.status==='disabled'}"><CircleAlert v-if="a.refresh_error" :size="12" /><span v-else class="status-dot" />{{ accountStatus(a) }}<span class="account-note" v-if="a.note">· {{ a.note }}</span></span></button></article>
     <NEmpty v-if="!items.length&&!loading&&!error" description="没有匹配的邮箱" class="account-rail-empty"><template #extra><NButton v-if="hasFilters" @click="clearFilters">清空筛选</NButton><NButton v-else type="primary" @click="importing=true">导入邮箱</NButton></template></NEmpty>
    </div>
    <footer class="account-rail-footer"><NPagination v-model:page="page" :disabled="deletingAccounts" :item-count="total" :page-size="size" simple /></footer>
   </aside>
   <MailWorkspace :account="activeAccount" :pane="pane" :removing="deletingAccounts" @read="pane='detail'" @back="pane='messages'" @refresh="id=>refresh([id])" @edit="edit" @remove="remove" @export="openExport" @changed="reload" />
  </section>
 </div>
 <AccountExport v-model:show="exporting" />
 <ImportForm v-model:show="importing" kind="accounts" :groups="groups" @submitted="reload" />
 <NModal v-model:show="editing" :mask-closable="!saving&&!credentialLoading"><NCard class="form-modal" :title="form.id?'编辑邮箱':'添加邮箱'" :closable="!saving&&!credentialLoading" @close="editing=false"><NForm :disabled="credentialLoading" @submit.prevent="save">
  <div class="form-grid"><NFormItem label="邮箱类型"><NSelect v-model:value="form.provider" :disabled="!!form.id && form.provider==='mx'" :options="[{label:'Outlook',value:'outlook'},{label:'iCloud',value:'icloud'},{label:'MX 域名收信',value:'mx',disabled:!!form.id}]" /></NFormItem><NFormItem label="状态"><NSelect v-model:value="form.status" :options="[{label:'启用',value:'active'},{label:'停用',value:'disabled'}]" /></NFormItem></div>
  <template v-if="form.provider==='mx'"><NFormItem label="收信域名"><NInput v-model:value="form.mx_domain" :disabled="!!form.id" aria-label="收信域名" placeholder="example.com" /></NFormItem><NFormItem label="邮件保留天数（0 为永久保存）"><NInputNumber v-model:value="form.mx_retention_days" aria-label="邮件保留天数" :min="0" :max="36500" :precision="0" style="width:100%" /></NFormItem><NAlert type="info" :show-icon="false">该域名下任意地址均可收信，默认管理地址为 postmaster@{{ form.mx_domain || '域名' }}。保存后在详情中查看 DNS 配置。</NAlert></template><NFormItem v-else label="邮箱地址"><NInput v-model:value="form.email" placeholder="name@outlook.com" /></NFormItem>
  <template v-if="form.provider==='outlook'"><NFormItem label="Client ID"><NInput v-model:value="form.client_id" placeholder="Microsoft 应用 Client ID" /></NFormItem><NFormItem :label="form.id?'Refresh Token（留空保留）':'Refresh Token'"><NInput v-model:value="form.refresh_token" autocomplete="off" placeholder="输入 Refresh Token" /></NFormItem></template>
  <NFormItem v-else-if="form.provider==='icloud'" :label="form.id?'应用专用密码（留空保留）':'应用专用密码'"><NInput v-model:value="form.password" type="password" show-password-on="click" autocomplete="off" placeholder="xxxx-xxxx-xxxx-xxxx" /></NFormItem>
  <NAlert v-if="credentialError" type="error" :show-icon="false">无法加载已有完整授权信息：{{ credentialError }}。可手动填写后保存。</NAlert>
  <NFormItem label="完整导入信息"><NInput :value="credentialLoading?'正在加载完整授权信息…':importLine" readonly placeholder="邮箱----密码----Client ID----Refresh Token" /></NFormItem>
  <NFormItem label="分组"><NSelect v-model:value="form.group_id" clearable :options="groupOpts" placeholder="未分组" /></NFormItem><NFormItem v-if="form.provider!=='mx'" label="账号代理（每行一个，留空继承）"><NInput v-model:value="proxy" type="textarea" :rows="3" placeholder="socks5://host:1080&#10;http://host:8080" /></NFormItem><NFormItem label="备注"><NInput v-model:value="form.note" placeholder="选填" maxlength="500" /></NFormItem>
  <div class="form-actions"><NButton :disabled="saving||credentialLoading" @click="editing=false">取消</NButton><NButton type="primary" attr-type="submit" :loading="saving" :disabled="credentialLoading">保存邮箱</NButton></div>
 </NForm></NCard></NModal>
</template>
<style src="../inbox.css"></style>
