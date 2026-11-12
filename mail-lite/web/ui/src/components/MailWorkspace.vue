<script setup lang="ts">
import { computed, nextTick, onActivated, onDeactivated, onUnmounted, ref, watch } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { NAlert, NButton, NCheckbox, NSpin, NTag, useDialog, useMessage } from 'naive-ui'
import { ArrowLeft, ArrowDownUp, Inbox, MailOpen, RefreshCw, Trash2, UserRound, FileText, Pencil, Download } from 'lucide-vue-next'
import { api, date, errorText, safeEmail, type Account, type Mail } from '../api'
import AccountDetails from './AccountDetails.vue'
import { fetchMailDetail, fetchMailPage, getMailDetail, getMailSnapshot, getMailView, invalidateAccountMail, onAllMailInvalidated, rememberMailPresentation, rememberMailView, type MailFolder, type MailOrder, type MailPage, type MailQuery, type MailSnapshot } from '../mailSession'

const props = defineProps<{ account:Account|null; pane:'accounts'|'messages'|'detail'; removing?:boolean }>()
const emit = defineEmits<{ read:[]; back:[]; refresh:[id:string]; edit:[Account]; remove:[Account]; export:[Account]; changed:[] }>()
const message = useMessage(), dialog = useDialog()
const items = ref<Mail[]>([]), checked = ref<string[]>([]), folder = ref<MailFolder>(''), order = ref<MailOrder>('latest'), busy = ref(false), loadingMore = ref(false), deleting = ref(false), error = ref(''), moreError = ref('')
const failures = ref<{ref:string;error:string}[]>([]), detail = ref<Mail|null>(null), detailBusy = ref(false), detailError = ref('')
const nextCursor = ref(''), hasMore = ref(false), listElement = ref<HTMLElement|null>(null), readerElement = ref<HTMLElement|null>(null)
const tab = ref<'message'|'account'>('message')
const visible = computed(() => items.value)
const fetching = computed(() => busy.value || loadingMore.value)
const allChecked = computed(() => visible.value.length > 0 && visible.value.slice(0,100).every(m => checked.value.includes(m.ref)))
let listSerial = 0, detailSerial = 0, currentQuery:MailQuery|null = null, active = true, restoring = false, deletionFailureAccount = ''
const folders:{label:string;value:MailFolder}[] = [{label:'全部邮件',value:''},{label:'收件箱',value:'inbox'},{label:'垃圾箱',value:'junkemail'}]
const sameQuery = (left:MailQuery|null,right:MailQuery) => !!left && left.accountID===right.accountID && left.generation===right.generation && left.order===right.order && left.folder===right.folder
function savePresentation() {
 if(!currentQuery||restoring||!active)return
 rememberMailView(currentQuery)
 const state:Partial<MailSnapshot>={selectedRef:detail.value?.ref||'',tab:tab.value}
 // KeepAlive detaches the DOM before deactivation. Hidden panels must not
 // replace their last visible scroll position with the browser's zero.
 if(listElement.value?.isConnected&&listElement.value.clientHeight>0)state.scrollTop=listElement.value.scrollTop
 if(readerElement.value?.isConnected&&readerElement.value.clientHeight>0)state.readerScrollTop=readerElement.value.scrollTop
 rememberMailPresentation(currentQuery,state)
}
function clearView(preserveFailures=false) {
 listSerial++; detailSerial++; items.value=[]; checked.value=[]; if(!preserveFailures)failures.value=[]; detail.value=null
 error.value=''; moreError.value=''; detailError.value=''; detailBusy.value=false; busy.value=false; loadingMore.value=false; tab.value='message'; nextCursor.value=''; hasMore.value=false
}
async function restoreSnapshot(snapshot:MailSnapshot, query:MailQuery) {
 if(!sameQuery(currentQuery,query))return
 const serial=listSerial
 items.value=snapshot.items; nextCursor.value=snapshot.nextCursor; hasMore.value=snapshot.hasMore; tab.value=snapshot.tab
 const selected=snapshot.items.find(mail=>mail.ref===snapshot.selectedRef)
 detail.value=selected?(getMailDetail(query,selected.ref)||selected):null
 restoring=true
 await nextTick()
 if(serial===listSerial&&sameQuery(currentQuery,query)) {
  if(listElement.value)listElement.value.scrollTop=snapshot.scrollTop
  if(readerElement.value)readerElement.value.scrollTop=snapshot.readerScrollTop
 }
 restoring=false
 if(selected&&snapshot.tab==='message'&&!getMailDetail(query,selected.ref)&&serial===listSerial&&sameQuery(currentQuery,query))void readDetail(selected,false)
}
function chooseQuery(nextOrder:MailOrder, nextFolder:MailFolder, preserveFailures=false) {
 savePresentation(); clearView(preserveFailures)
 const account=props.account
 if(!account){currentQuery=null;return}
 order.value=nextOrder; folder.value=nextFolder
 const query:MailQuery={accountID:account.id,generation:account.generation,order:nextOrder,folder:nextFolder}
 currentQuery=query; rememberMailView(query)
 const snapshot=getMailSnapshot(query)
 if(snapshot)void restoreSnapshot(snapshot,query)
 else void load()
}
watch([() => props.account?.id,() => props.account?.generation], () => {
 const account=props.account, view=account?getMailView(account):null
 const preserveFailures=!!account&&currentQuery?.accountID===account.id&&deletionFailureAccount===account.id
 deletionFailureAccount=''
 chooseQuery(view?.order||'latest',view?.folder||'',preserveFailures)
}, {immediate:true})
watch(tab,savePresentation)
watch(() => props.pane, () => { if(props.pane==='messages')void nextTick(checkFill) })
const stopInvalidation = onAllMailInvalidated(() => {
 if(!currentQuery)return
 if(active)chooseQuery(order.value,folder.value)
 else clearView()
})
onBeforeRouteLeave(() => { savePresentation() })
onDeactivated(() => { active=false })
onActivated(() => {
 active=true
 if(currentQuery&&!getMailSnapshot(currentQuery))chooseQuery(order.value,folder.value)
 else if(currentQuery){const snapshot=getMailSnapshot(currentQuery);if(snapshot)void restoreSnapshot(snapshot,currentQuery)}
})
onUnmounted(() => { stopInvalidation(); savePresentation(); listSerial++; detailSerial++; active=false })
function toggle(value:string, selected:boolean) { checked.value=selected ? [...checked.value,value] : checked.value.filter(x=>x!==value) }
function toggleAll(selected:boolean) { checked.value=selected ? visible.value.slice(0,100).map(m=>m.ref) : [] }

async function load(append=false) {
 if(append&&currentQuery&&!getMailSnapshot(currentQuery)){chooseQuery(order.value,folder.value);return}
 const query=currentQuery, serial=++listSerial
 if(!query)return
 const cursor=append?nextCursor.value:''
 if(append&&(!hasMore.value||!cursor))return
 if(append)loadingMore.value=true;else busy.value=true
 error.value=''; moreError.value=''
 try {
  const params=new URLSearchParams({limit:'20',order:query.order,folder:query.folder});if(cursor)params.set('cursor',cursor)
  const snapshot=await fetchMailPage(query,cursor,()=>api<MailPage>(`admin/accounts/${encodeURIComponent(query.accountID)}/messages?${params}`))
  if(serial!==listSerial||!sameQuery(currentQuery,query))return
  items.value=snapshot.items; nextCursor.value=snapshot.nextCursor; hasMore.value=snapshot.hasMore
  if(!append){checked.value=[];await restoreSnapshot(snapshot,query)}
 } catch(e) { if(serial===listSerial&&sameQuery(currentQuery,query)){if(append)moreError.value=mailError(e);else error.value=mailError(e)} }
 finally { if(serial===listSerial){busy.value=false;loadingMore.value=false;void nextTick(checkFill)} }
}
function refreshList(){if(!currentQuery||fetching.value||deleting.value)return;invalidateAccountMail(currentQuery.accountID);clearView();void load()}
function loadMore(){if(!fetching.value&&!deleting.value&&!moreError.value&&hasMore.value)void load(true)}
function retryMore(){moreError.value='';loadMore()}
function checkFill(){const element=listElement.value;if(active&&!restoring&&element&&element.clientHeight>0&&element.scrollHeight-element.scrollTop-element.clientHeight<180)loadMore()}
function onListScroll(){savePresentation();checkFill()}
function selectFolder(value:MailFolder){if(!deleting.value&&value!==folder.value)chooseQuery(order.value,value)}
function toggleOrder() {
 if(!deleting.value)chooseQuery(order.value==='latest'?'oldest':'latest',folder.value)
}
async function readDetail(mail:Mail, navigate=true) {
 const query=currentQuery, serial=++detailSerial
 if(!query)return
 const cached=getMailDetail(query,mail.ref)
 detail.value=cached||mail; tab.value='message'; detailBusy.value=!cached; detailError.value=''
 if(navigate){if(readerElement.value)readerElement.value.scrollTop=0;emit('read')}
 savePresentation()
 if(cached)return
 try {
  const data=await fetchMailDetail(query,mail.ref,()=>api<Mail>(`admin/accounts/${encodeURIComponent(query.accountID)}/message?ref=${encodeURIComponent(mail.ref)}`))
  if(serial===detailSerial&&sameQuery(currentQuery,query))detail.value=data
 } catch(e) { if(serial===detailSerial&&sameQuery(currentQuery,query))detailError.value=mailError(e) }
 finally { if(serial===detailSerial) detailBusy.value=false }
}
function remove(refs:string[]) {
 const account=props.account
 if(!account || !refs.length || deleting.value) return
 const subjects=refs.map(ref=>items.value.find(m=>m.ref===ref)?.subject||'(无主题)')
 dialog.warning({title:`删除 ${refs.length} 封邮件`,content:`${account.email}：${account.provider==='mx'?'所选邮件将从本地永久删除。':account.provider==='icloud'?'所选邮件将移到 iCloud 废纸篓。':'所选邮件将通过 Outlook 标准接口删除，通常移入“已删除邮件”。'}仅处理本次选中的 ${refs.length} 封${refs.length===1?'：'+subjects[0]+'。':'，不会处理未选中的邮件。'}`,positiveText:'确认删除',negativeText:'取消',onPositiveClick:async()=>{
  deleting.value=true
  try {
   const data=await api<{results:{ref:string;success:boolean;error:string}[]}>(`admin/accounts/${account.id}/messages/delete`,'POST',{refs})
   const done=new Set(data.results.filter(r=>r.success).map(r=>r.ref))
   if(props.account?.id===account.id) {
    listSerial++; busy.value=false
    items.value=items.value.filter(m=>!done.has(m.ref)); checked.value=checked.value.filter(ref=>!done.has(ref))
    if(detail.value && done.has(detail.value.ref)) { detailSerial++; detail.value=null; detailBusy.value=false; detailError.value='' }
    failures.value=data.results.filter(r=>!r.success)
    deletionFailureAccount=failures.value.length?account.id:''
   }
   if(done.size) message.success(`已删除 ${done.size} 封邮件`)
   if(data.results.some(r=>!r.success)) message.warning('部分邮件未删除，失败项已保留，可查看原因后重试')
  } catch(e) { message.error(errorText(e)) }
  finally {
   invalidateAccountMail(account.id)
   if(props.account?.id===account.id){listSerial++;busy.value=false;loadingMore.value=false;nextCursor.value='';hasMore.value=false}
   deleting.value=false;emit('changed')
  }
 }})
}
function mailError(e:unknown) {
 const text=errorText(e)
 if(text.includes('Microsoft token endpoint could not be reached')) return '无法连接 Microsoft 授权服务，请检查网络或邮箱代理后重试。'
 return text
}
</script>
<template>
 <section class="mail-lane message-lane" :class="{'lane-active':pane==='messages'}" aria-label="邮件列表">
  <header class="lane-heading"><div class="lane-heading-title"><Inbox :size="17" /><h2>邮件</h2><span v-if="account" class="lane-count">{{ visible.length }}</span></div><div class="row"><NButton quaternary :disabled="!account||deleting" :title="order==='latest'?'从最早邮件开始':'从最新邮件开始'" :aria-label="order==='latest'?'从最早邮件开始':'从最新邮件开始'" @click="toggleOrder"><ArrowDownUp :size="16" /></NButton><NButton quaternary :disabled="!account||deleting||fetching" :aria-busy="fetching" aria-label="刷新邮件" @click="refreshList"><RefreshCw :size="17" class="refresh-icon" :class="{spinning:fetching}" /></NButton></div></header>
  <div class="folder-switch" role="group" aria-label="邮件文件夹"><button v-for="f in folders" :key="f.value" :class="{active:folder===f.value}" :aria-pressed="folder===f.value" :disabled="deleting" @click="selectFolder(f.value)">{{ f.label }}</button></div>
  <div class="mail-selection-row"><NCheckbox :checked="allChecked" :indeterminate="!!checked.length&&!allChecked" :disabled="!visible.length||deleting||busy" @update:checked="toggleAll">{{ checked.length?'已选 '+checked.length+' 封':'选择邮件' }}</NCheckbox><NButton v-if="checked.length" quaternary type="error" :loading="deleting" @click="remove([...checked])"><Trash2 :size="15" class="icon-before" />删除所选</NButton></div>
  <div ref="listElement" class="lane-scroll messages-scroll" :aria-busy="fetching" @scroll.passive="onListScroll">
   <div v-if="error" class="mail-error-state" role="alert"><span class="empty-lane-icon warning"><Inbox :size="26" /></span><h3>暂时无法获取邮件</h3><p>{{ error }}</p><NButton secondary :disabled="busy" @click="load()">重新取信</NButton><NButton text type="primary" @click="tab='account';emit('read')">查看邮箱授权信息</NButton></div>
   <NAlert v-for="f in failures" :key="f.ref" type="warning" :show-icon="false" class="delete-failure">{{ items.find(m=>m.ref===f.ref)?.subject || '邮件' }}：{{ f.error }}</NAlert>
   <div v-if="busy" class="lane-loading"><NSpin size="medium" /><span>正在获取邮件…</span></div>
   <template v-else-if="visible.length">
    <article v-for="m in visible" :key="m.ref" class="inbox-message" :class="{active:detail?.ref===m.ref}">
     <NCheckbox :checked="checked.includes(m.ref)" :disabled="deleting||(!checked.includes(m.ref)&&checked.length>=100)" :aria-label="'选择邮件 '+m.subject" @update:checked="v=>toggle(m.ref,v)" />
     <button class="inbox-message-open" :aria-label="'查看邮件 '+(m.subject||'(无主题)')" :aria-pressed="detail?.ref===m.ref" @click="readDetail(m)"><span class="inbox-message-top"><strong>{{ m.from }}</strong><time>{{ date(m.received_at) }}</time></span><span class="inbox-message-subject">{{ m.subject || '(无主题)' }}</span><span class="inbox-message-preview">{{ m.preview }}</span><span class="message-folder-label">{{ m.folder==='inbox'?'收件箱':'垃圾箱' }}</span></button>
    </article>
   </template>
   <div v-else-if="!error" class="empty-lane"><span class="empty-lane-icon"><Inbox :size="28" /></span><h3>{{ account?'当前范围内没有邮件':'选择一个邮箱' }}</h3><p>{{ account?'试试切换文件夹或刷新邮件。':'从左侧列表选择邮箱，邮件将在这里显示。' }}</p></div>
   <div v-if="items.length" class="mail-page-status" role="status"><span v-if="loadingMore">正在加载…</span><template v-else-if="moreError"><span>{{ moreError }}</span><NButton text type="primary" @click="retryMore">重试</NButton></template><NButton v-else-if="hasMore" text @click="loadMore">继续加载</NButton><span v-else>已加载全部邮件</span></div>
  </div>
  <footer class="lane-footnote">{{ account?(order==='latest'?'从最新开始':'从最早开始')+' · 已加载 '+items.length+' 封 · 每次加载 20 封':'支持收件箱和垃圾箱' }}</footer>
 </section>
 <section class="mail-lane reader-lane" :class="{'lane-active':pane==='detail'}" aria-label="邮件与邮箱详情">
  <header class="lane-heading reader-heading"><NButton class="reader-back" quaternary aria-label="返回邮件列表" @click="emit('back')"><ArrowLeft :size="18" /></NButton><div class="reader-tabs" role="tablist" aria-label="详情内容"><button role="tab" :aria-selected="tab==='message'" :class="{active:tab==='message'}" @click="tab='message'"><FileText :size="16" />邮件正文</button><button role="tab" :aria-selected="tab==='account'" :class="{active:tab==='account'}" @click="tab='account'"><UserRound :size="16" />邮箱详情</button></div><NButton v-if="tab==='message'&&detail" quaternary type="error" :disabled="deleting" aria-label="删除当前邮件" @click="remove([detail.ref])"><Trash2 :size="17" /></NButton></header>
  <div ref="readerElement" class="lane-scroll reader-scroll" @scroll.passive="savePresentation">
   <template v-if="tab==='account'&&account"><div class="reader-account-actions"><NButton quaternary :disabled="removing" @click="emit('edit',account)"><Pencil :size="15" class="icon-before" />编辑</NButton><NButton quaternary @click="emit('export',account)"><Download :size="15" class="icon-before" />导出</NButton><NButton quaternary type="error" :disabled="removing" @click="emit('remove',account)"><Trash2 :size="15" class="icon-before" />删除邮箱</NButton></div><AccountDetails :account="account" @refresh="emit('refresh',$event)" /></template>
   <template v-else-if="tab==='message'&&detail"><div class="reader-message-header"><NTag size="small" :bordered="false">{{ detail.folder==='inbox'?'收件箱':'垃圾箱' }}</NTag><h2>{{ detail.subject || '(无主题)' }}</h2><dl class="reader-message-meta"><div><dt>发件人</dt><dd>{{ detail.from }}</dd></div><div><dt>收件人</dt><dd>{{ detail.to }}</dd></div><div><dt>时间</dt><dd>{{ date(detail.received_at) }}</dd></div></dl></div><div v-if="detailBusy" class="lane-loading"><NSpin /><span>正在加载正文…</span></div><NAlert v-else-if="detailError" type="error" class="reader-body-error">{{ detailError }}<NButton text @click="readDetail(detail)">重新加载正文</NButton></NAlert><iframe v-else class="reader-mail-body" sandbox="" referrerpolicy="no-referrer" :srcdoc="safeEmail(detail.html,detail.text)" title="邮件正文" /></template>
   <div v-else class="empty-lane reader-empty"><span class="empty-lane-icon"><MailOpen :size="32" /></span><h3>{{ tab==='account'?'选择邮箱查看完整信息':'选择一封邮件开始阅读' }}</h3><p>邮箱、邮件与详情，在同一处查看。</p><NButton v-if="account" secondary type="primary" @click="tab='account'">查看邮箱详情</NButton></div>
  </div>
 </section>
</template>
