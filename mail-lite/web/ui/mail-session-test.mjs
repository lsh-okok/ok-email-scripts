import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import ts from 'typescript'

const source=await fs.readFile(new URL('./src/mailSession.ts',import.meta.url),'utf8')
const compiled=ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ES2022}}).outputText
const cache=await import('data:text/javascript;base64,'+Buffer.from(compiled).toString('base64'))
const account={id:'a',generation:1}
const query={accountID:'a',generation:1,order:'latest',folder:''}
const mail=(ref)=>({ref,subject:ref,from:'sender@example.net',to:'recipient@example.net',folder:'inbox',received_at:'2026-10-25T00:00:00Z',preview:'preview'})
const page=(items,next='',more=false)=>({items:items.map(mail),order:'latest',folder:'',next_cursor:next,has_more:more})
cache.reconcileMailAccount(account)
let listCalls=0, resolvePage
const loader=()=>{listCalls++;return new Promise(resolve=>{resolvePage=resolve})}
const first=cache.fetchMailPage(query,'',loader), same=cache.fetchMailPage({...query},'',loader)
assert.equal(listCalls,1,'same in-flight page must share the request')
resolvePage(page(['1','2'],'next',true))
assert.equal(await first,await same)
cache.rememberMailPresentation(query,{scrollTop:240,selectedRef:'2',tab:'message'})
const second=await cache.fetchMailPage(query,'next',async()=>page(['2','3']))
assert.deepEqual(second.items.map(item=>item.ref),['1','2','3'],'overlapping pages must deduplicate refs')
assert.equal(second.scrollTop,240)
assert.equal(second.selectedRef,'2')
assert.equal(cache.getMailSnapshot({...query,folder:'inbox'}),undefined,'folder snapshots must be isolated')
assert.equal(cache.getMailSnapshot({...query,order:'oldest'}),undefined,'sort snapshots must be isolated')
let detailCalls=0
const body=await cache.fetchMailDetail(query,'2',async()=>{detailCalls++;return {...mail('2'),text:'body'}})
assert.equal(await cache.fetchMailDetail(query,'2',async()=>{throw Error('cached detail requested again')}),body)
assert.equal(detailCalls,1)
assert.equal(cache.getMailDetail({...query,folder:'junkemail'},'2'),body,'body cache may be shared across folder/sort views')
await assert.rejects(cache.fetchMailPage(query,'failed',async()=>{throw Error('network')}),/network/)
assert.equal(cache.getMailSnapshot(query).items.length,3,'failure must retain loaded pages')
cache.reconcileMailAccount({...account,generation:2})
assert.equal(cache.getMailSnapshot(query),undefined,'generation changes invalidate old pages')
assert.equal(cache.getMailDetail(query,'2'),undefined,'generation changes invalidate old bodies')
const newer={...query,generation:2}
let finishLate
const pending=cache.fetchMailPage(newer,'',()=>new Promise(resolve=>{finishLate=resolve}))
cache.invalidateAccountMail('a')
finishLate(page(['late']))
await assert.rejects(pending,/邮件缓存已更新/,'invalidated requests must not repopulate the cache')
assert.equal(cache.getMailSnapshot(newer),undefined)
await cache.fetchMailPage(newer,'',async()=>page(['new']))
cache.rememberMailView({...newer,order:'oldest',folder:'inbox'})
assert.equal(cache.getMailView({...account,generation:2}).folder,'inbox')
cache.clearMailSession()
cache.rememberMailView(newer)
assert.equal(cache.getMailSnapshot(newer),undefined)
assert.equal(cache.getMailView({...account,generation:2}).folder,'','logout must clear saved view preferences')
console.log('PASS: page coalescing, ref deduplication, folder/order isolation, view restoration, lazy body reuse, retry preservation, generation invalidation, pending invalidation and login-session clearing')
