import { chromium } from 'playwright'
import fs from 'node:fs/promises'
import assert from 'node:assert/strict'

const base=process.env.QA_BASE_URL||'http://127.0.0.1:18080'
const browser=await chromium.launch({headless:true,executablePath:process.env.BROWSER_PATH||'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe'})
const context=await browser.newContext({viewport:{width:1280,height:960},colorScheme:'light',acceptDownloads:true})
await context.grantPermissions(['clipboard-read','clipboard-write'],{origin:base})
const page=await context.newPage(),errors=[]
page.on('pageerror',e=>errors.push(e.message))
await fs.mkdir('../../.cache/screenshots',{recursive:true})
async function downloadText(download){const stream=await download.createReadStream(),chunks=[];for await(const chunk of stream)chunks.push(chunk);return Buffer.concat(chunks).toString('utf8')}
async function download(){const [file]=await Promise.all([page.waitForEvent('download'),page.getByRole('button',{name:'下载全部邮箱',exact:true}).click()]);await page.locator('.n-modal').waitFor({state:'hidden'});return file}
function accountRow(email,width){return page.locator(width<768?'.mobile-card':'.n-data-table-tr').filter({hasText:email}).filter({visible:true})}
async function openCredentials(email,width){await accountRow(email,width).getByRole('button',{name:'详情',exact:true}).click();await page.locator('[data-credential="'+(email.endsWith('@icloud.com')?'应用专用密码':'Refresh Token')+'"]').waitFor();await page.waitForFunction(()=>{const el=document.querySelector('.credentials-drawer');if(!el)return false;const r=el.getBoundingClientRect();return Math.abs(r.right-innerWidth)<1&&(innerWidth>=768||Math.abs(r.left)<1)})}
async function closeCredentials(){await page.locator('.credentials-drawer .n-drawer-header__close').click();await page.locator('.credentials-drawer').waitFor({state:'hidden'})}
try{
 await page.goto(base+'/admin/accounts')
 await page.getByPlaceholder('输入管理员密码').fill('test-password-12345')
 await page.getByRole('button',{name:'登录管理台'}).click()
 await page.getByRole('heading',{name:'邮箱管理',exact:true}).waitFor()
 for(const width of [360,390,768,1280,1920]){
  await page.setViewportSize({width,height:960})
  for(const theme of ['light','dark','system']){
   await page.evaluate(theme=>localStorage.setItem('mail-lite-theme',theme),theme)
   await page.goto(base+'/admin/accounts');await page.getByRole('heading',{name:'邮箱管理',exact:true}).waitFor()
   await accountRow('personal@outlook.com',width).waitFor()
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1),false,`account overflow ${width}/${theme}`)
   assert.ok(!(await page.locator('main').textContent()).includes('secret-refresh'),'list rendered a raw token')
   assert.equal(await accountRow('personal@outlook.com',width).getByText('Token 已配置',{exact:true}).count(),0,'list rendered token status')
   assert.equal(await accountRow('personal@outlook.com',width).getByText('client',{exact:true}).count(),0,'list rendered client id')
   await page.screenshot({path:`../../.cache/screenshots/accounts-updated-${width}-${theme}.png`,fullPage:true})
   await openCredentials('personal@outlook.com',width)
   assert.equal(await page.locator('.credentials-drawer .account-detail-title h3').filter({hasText:'账号信息'}).count(),1)
   assert.equal(await page.locator('.credentials-drawer .detail-note').filter({hasText:'QA Outlook detail'}).count(),1)
   assert.ok((await page.locator('.credentials-drawer').last().textContent()).includes('代理配置'))
   assert.ok((await page.locator('.credentials-drawer').last().textContent()).includes('proxy-pass'))
   assert.equal(await page.locator('[data-credential="Refresh Token"]').textContent(),'secret-refresh')
   assert.ok((await page.locator('[data-credential="Access Token · Graph 读取"]').textContent()).startsWith('qa-access-'))
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1),false,`credential overflow ${width}/${theme}`)
   const widthInside=await page.locator('.credentials-drawer').evaluate(el=>el.scrollWidth<=el.clientWidth+1)
   assert.equal(widthInside,true,`drawer overflow ${width}/${theme}`)
   await page.screenshot({path:`../../.cache/screenshots/credentials-${width}-${theme}.png`,fullPage:false})
   await page.getByRole('button',{name:'隐藏 Refresh Token',exact:true}).click()
   assert.ok(!(await page.locator('[data-credential="Refresh Token"]').textContent()).includes('secret-refresh'))
   await page.getByRole('button',{name:'显示 Refresh Token',exact:true}).click()
   await page.getByRole('button',{name:'复制 Refresh Token',exact:true}).click()
   assert.equal(await page.evaluate(()=>navigator.clipboard.readText()),'secret-refresh')
   await closeCredentials()
   assert.equal(await page.locator('[data-credential]').count(),0,'closed drawer retained secrets')
  }
 }
 await page.setViewportSize({width:390,height:844})
 await page.goto(base+'/admin/accounts')
 await openCredentials('direct@icloud.com',390)
 assert.equal(await page.locator('[data-credential="应用专用密码"]').textContent(),'qa-app-password')
 await page.getByRole('button',{name:'复制导入行',exact:true}).click()
 assert.equal(await page.evaluate(()=>navigator.clipboard.readText()),'direct@icloud.com----qa-app-password')
 await closeCredentials()
 await page.getByRole('button',{name:'导出邮箱',exact:true}).click()
 assert.ok((await page.locator('.n-modal').textContent()).includes('导出全部邮箱'))
 assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+1),false,'export overflow 390')
 await page.screenshot({path:'../../.cache/screenshots/export-390.png',fullPage:true})
 const txt=await download()
 assert.ok(txt.suggestedFilename().endsWith('.txt'))
 const exportedLines=(await downloadText(txt)).trim().split(/\r?\n/)
 assert.ok(exportedLines.includes('personal@outlook.com--------client----secret-refresh'))
 assert.ok(exportedLines.includes('direct@icloud.com----qa-app-password'))
 // A failed export keeps the error visible for retry.
 await page.getByRole('button',{name:'导出邮箱',exact:true}).click()
 await page.route('**/api/admin/accounts/export',route=>route.fulfill({status:500,contentType:'application/json',body:JSON.stringify({error:'模拟导出失败'})}))
 await page.getByRole('button',{name:'下载全部邮箱',exact:true}).click()
 await page.getByText('模拟导出失败',{exact:true}).waitFor()
 assert.equal(await page.locator('.n-modal').isVisible(),true)
 await page.unroute('**/api/admin/accounts/export')
 await download()
 await accountRow('personal@outlook.com',390).getByRole('button',{name:'编辑',exact:true}).click()
 await page.getByPlaceholder('选填',{exact:true}).fill('QA edited without changing credentials')
 const saveResponse=page.waitForResponse(r=>r.url().endsWith('/api/admin/accounts')&&r.request().method()==='POST')
 await page.getByRole('button',{name:'保存邮箱',exact:true}).click()
 assert.equal((await saveResponse).status(),200)
 await page.locator('.n-modal').waitFor({state:'hidden'})
 await openCredentials('personal@outlook.com',390)
 assert.equal(await page.locator('[data-credential="Refresh Token"]').textContent(),'secret-refresh')
 await closeCredentials()
 assert.deepEqual(errors,[])
 console.log('PASS: 15 account/credential viewport-theme combinations; no overflow; token reveal/hide/copy and removal on close; iCloud password/copy; full four-field TXT export, failed export retry, account editing preserves credentials.')
}finally{await browser.close()}
