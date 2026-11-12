import type { Account, Mail } from './api'

export type MailOrder = 'latest' | 'oldest'
export type MailFolder = '' | 'inbox' | 'junkemail'
export type MailQuery = { accountID:string; generation:number; order:MailOrder; folder:MailFolder }
export type MailPage = { items:Mail[]; order:MailOrder; folder:MailFolder; next_cursor:string; has_more:boolean; scan_limit?:number }
export type MailSnapshot = { items:Mail[]; nextCursor:string; hasMore:boolean; scrollTop:number; readerScrollTop:number; selectedRef:string; tab:'message'|'account' }
type MailView = { generation:number; order:MailOrder; folder:MailFolder }

// These caches belong to the current login in this tab. No mail is persisted in
// localStorage, sessionStorage, or IndexedDB.
const snapshots = new Map<string, MailSnapshot>(), details = new Map<string, Mail>()
const views = new Map<string, MailView>(), generations = new Map<string, number>()
const pendingPages = new Map<string, Promise<MailSnapshot>>(), pendingDetails = new Map<string, Promise<Mail>>()
const epochs = new Map<string, number>()
const invalidationListeners = new Set<()=>void>()
let sessionEpoch = 0
const queryKey = (q:MailQuery) => JSON.stringify([q.accountID,q.generation,q.order,q.folder])
const detailKey = (q:MailQuery, ref:string) => JSON.stringify([q.accountID,q.generation,ref])
const belongsTo = (key:string, id:string) => JSON.parse(key)[0] === id

function remember<K,V>(cache:Map<K,V>, key:K, value:V, limit:number) {
 cache.delete(key); cache.set(key,value)
 while(cache.size>limit) cache.delete(cache.keys().next().value!)
 return value
}
export function invalidateAccountMail(id:string, forget=false) {
 epochs.set(id,(epochs.get(id)||0)+1)
 for(const cache of [snapshots,details]) for(const key of cache.keys()) if(belongsTo(key,id)) cache.delete(key)
 if(forget){ views.delete(id); generations.delete(id) }
}
export function clearMailSession() {
 sessionEpoch++; snapshots.clear(); details.clear(); views.clear(); generations.clear(); epochs.clear(); pendingPages.clear(); pendingDetails.clear()
}
export function invalidateAllMail() {
 sessionEpoch++; snapshots.clear(); details.clear(); pendingPages.clear(); pendingDetails.clear()
 for(const listener of invalidationListeners) listener()
}
export function onAllMailInvalidated(listener:()=>void) { invalidationListeners.add(listener); return ()=>{invalidationListeners.delete(listener)} }
export function reconcileMailAccount(account:Account) {
 const old=generations.get(account.id)
 if(old!==undefined&&old!==account.generation) invalidateAccountMail(account.id)
 generations.set(account.id,account.generation)
 const view=views.get(account.id)
 if(view) view.generation=account.generation
}
export function getMailView(account:Account):MailView {
 reconcileMailAccount(account)
 return views.get(account.id)||{generation:account.generation,order:'latest',folder:''}
}
export function rememberMailView(q:MailQuery) { if(generations.get(q.accountID)===q.generation)views.set(q.accountID,{generation:q.generation,order:q.order,folder:q.folder}) }
export function getMailSnapshot(q:MailQuery) {
 const key=queryKey(q), snapshot=snapshots.get(key)
 return snapshot ? remember(snapshots,key,snapshot,60) : undefined
}
export function rememberMailPresentation(q:MailQuery, state:Partial<Pick<MailSnapshot,'scrollTop'|'readerScrollTop'|'selectedRef'|'tab'>>) {
 const snapshot=getMailSnapshot(q)
 if(snapshot) Object.assign(snapshot,state)
}
export function getMailDetail(q:MailQuery, ref:string) {
 const key=detailKey(q,ref), detail=details.get(key)
 return detail ? remember(details,key,detail,160) : undefined
}
export function fetchMailPage(q:MailQuery, cursor:string, loader:()=>Promise<MailPage>):Promise<MailSnapshot> {
 const key=queryKey(q), epoch=epochs.get(q.accountID)||0, session=sessionEpoch
 const requestKey=JSON.stringify([key,cursor,session,epoch]), existing=pendingPages.get(requestKey)
 if(existing) return existing
 const promise=loader().then(page=>{
  if(session!==sessionEpoch||epoch!==(epochs.get(q.accountID)||0)) throw new Error('邮件缓存已更新')
  const previous=snapshots.get(key)
  const items:Mail[]=[], seen=new Set<string>()
  for(const item of [...(cursor?previous?.items||[]:[]),...(page.items||[])]) {
   if(!item.ref||seen.has(item.ref)) continue
   seen.add(item.ref); items.push(item)
  }
  const state:MailSnapshot={items,nextCursor:page.next_cursor||'',hasMore:!!page.has_more,scrollTop:previous?.scrollTop||0,readerScrollTop:previous?.readerScrollTop||0,selectedRef:previous?.selectedRef||'',tab:previous?.tab||'message'}
  if(state.selectedRef&&!items.some(item=>item.ref===state.selectedRef)) { state.selectedRef=''; state.readerScrollTop=0 }
  return remember(snapshots,key,state,60)
 }).finally(()=>{if(pendingPages.get(requestKey)===promise)pendingPages.delete(requestKey)})
 pendingPages.set(requestKey,promise)
 return promise
}
export function fetchMailDetail(q:MailQuery, ref:string, loader:()=>Promise<Mail>):Promise<Mail> {
 const cached=getMailDetail(q,ref)
 if(cached)return Promise.resolve(cached)
 const key=detailKey(q,ref), epoch=epochs.get(q.accountID)||0, session=sessionEpoch
 const requestKey=JSON.stringify([key,session,epoch]), existing=pendingDetails.get(requestKey)
 if(existing)return existing
 const promise=loader().then(mail=>{
  if(session!==sessionEpoch||epoch!==(epochs.get(q.accountID)||0))throw new Error('邮件缓存已更新')
  return remember(details,key,mail,160)
 }).finally(()=>{if(pendingDetails.get(requestKey)===promise)pendingDetails.delete(requestKey)})
 pendingDetails.set(requestKey,promise)
 return promise
}
