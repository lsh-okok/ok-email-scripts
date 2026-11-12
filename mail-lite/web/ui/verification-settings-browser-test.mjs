// Use an isolated TestBrowserFixture; this test never starts an inspection.
// QA_BASE_URL=http://127.0.0.1:18094 node web/ui/verification-settings-browser-test.mjs
import { chromium } from 'playwright-core'
import assert from 'node:assert/strict'
import path from 'node:path'

const base = process.env.QA_BASE_URL || 'http://127.0.0.1:18094'
const browser = await chromium.launch({ headless: true, executablePath: process.env.BROWSER_PATH || 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe' })
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
const page = await context.newPage(), errors = []
page.setDefaultTimeout(15000)
page.on('pageerror', error => errors.push(error.message))
let failNext = true, mutations = 0, sends = 0
try {
 if (process.env.QA_DIST_FILES === '1') {
  const dist = path.resolve(import.meta.dirname, '../dist')
  await context.route(base + '/admin/**', async route => {
   const pathname = decodeURIComponent(new URL(route.request().url()).pathname)
   const filename = pathname.startsWith('/admin/assets/') ? path.resolve(dist, pathname.slice('/admin/'.length)) : path.join(dist, 'index.html')
   assert.ok(filename.startsWith(dist + path.sep)); await route.fulfill({ path: filename })
  })
 }
 await page.goto(base + '/admin/cards')
 await page.getByPlaceholder('输入管理员密码').fill('test-password-12345')
 await page.getByRole('button', { name: '登录管理台' }).click()
 await page.getByRole('heading', { name: '卡密管理', exact: true }).waitFor()
 const session = await (await context.request.get(base + '/api/session')).json()
 const plan = { enabled: false, run_at: '13:37', scope: 'all', folder_id: 0, sample: 3, include_sold: false, options: { timeout: 17, concurrency: 3, wait: 60, interval: 1000 } }
 const seed = await context.request.put(base + '/api/admin/mail/inspection-schedule', { headers: { 'X-CSRF-Token': session.csrf }, data: plan })
 assert.equal(seed.status(), 200)
 await context.route('**/api/admin/mail/**', async route => {
  const request = route.request(), pathname = new URL(request.url()).pathname
  if (request.method() === 'POST' && (pathname.includes('/inspections') || pathname.endsWith('/inspection-schedule/run'))) { sends++; return route.abort('blockedbyclient') }
  if (request.method() !== 'GET' && pathname !== '/api/admin/mail/mapping') mutations++
  if (request.method() === 'GET' && pathname.endsWith('/senders') && failNext) { failNext = false; return route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: 'QA 配置暂时不可用' }) }) }
  return route.continue()
 })
 await page.goto(base + '/admin/verification?tab=settings')
 await page.getByRole('heading', { name: '检测配置', exact: true }).waitFor()
 const panel = page.locator('.verification-settings')
 await panel.getByText('QA 配置暂时不可用', { exact: false }).waitFor()
 async function disabledActions() {
  for (const label of ['保存定时检测', '保存默认 SMTP', '立即执行已保存的计划', '新增发件账号']) assert.equal(await panel.getByRole('button', { name: label, exact: true }).isDisabled(), true, label + ' must be disabled after a failed configuration load')
  assert.equal(mutations, 0, 'Loading failure must not write defaults to the server')
 }
 await disabledActions()
 await panel.getByRole('button', { name: '重新加载配置', exact: true }).click()
 const savePlan = panel.getByRole('button', { name: '保存定时检测', exact: true })
 await page.waitForFunction(() => [...document.querySelectorAll('.verification-settings button')].some(button => button.textContent.trim() === '保存定时检测' && !button.disabled))
 const daily = panel.locator('.settings-box').filter({ has: page.getByRole('heading', { name: '每日收信检测', exact: true }) })
 const time = daily.locator('.n-form-item').filter({ hasText: '每日执行时间' }).locator('input')
 assert.equal(await time.inputValue(), plan.run_at, 'Retry must load the actual saved plan')
 const savedResponse = page.waitForResponse(response => response.request().method() === 'PUT' && response.url().endsWith('/inspection-schedule'))
 await savePlan.click()
 const saved = await savedResponse
 assert.equal(saved.status(), 200)
 const payload = saved.request().postDataJSON()
 assert.equal(payload.run_at, plan.run_at); assert.equal(payload.sample, plan.sample); assert.equal(payload.options.timeout, plan.options.timeout)
 assert.equal(mutations, 1)
 mutations = 0; failNext = true
 await panel.getByRole('button', { name: '刷新配置', exact: true }).click()
 await panel.getByText('QA 配置暂时不可用', { exact: false }).waitFor()
 await disabledActions()
 const persisted = await (await context.request.get(base + '/api/admin/mail/inspection-schedule')).json()
 assert.equal(persisted.run_at, plan.run_at); assert.equal(persisted.sample, plan.sample)
 assert.equal(sends, 0)
 assert.deepEqual(errors, [])
 console.log('PASS: initial and refresh configuration failures disable all writes/run actions; retry restores saved values and preserves the actual schedule; no inspection or SMTP action started')
} finally { await browser.close() }
