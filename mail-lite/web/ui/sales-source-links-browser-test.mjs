// MAIL_LITE_QA_SOURCE_LINKS=1 MAIL_LITE_QA_ADDR=127.0.0.1:18094 go test ./internal/core -run '^TestBrowserFixture$' -v -count=1 -timeout=20m
// node web/ui/sales-source-links-browser-test.mjs
// Isolated Go test database and fake mailbox only. No SMTP or external HTTP.
import { chromium } from 'playwright-core'
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import path from 'node:path'
import os from 'node:os'

const base = process.env.QA_BASE_URL || 'http://127.0.0.1:18094'
const output = path.join(os.tmpdir(), 'mail-lite-source-links-browser')
const browser = await chromium.launch({ headless: true, executablePath: process.env.BROWSER_PATH || 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe' })
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, permissions: ['clipboard-read', 'clipboard-write'] })
const page = await context.newPage(), errors = []
page.on('pageerror', e => errors.push(e.message))
page.setDefaultTimeout(15000)
await fs.mkdir(output, { recursive: true })
let csrf = ''
const pane = () => page.locator('.n-tab-pane').filter({ visible: true }).last()
const modal = () => page.locator('.n-modal').filter({ visible: true }).last()
async function tab(name) {
 const key={'工作流概览':'overview','库存管理':'inventory','账号映射':'mapping','出库记录':'outbound','导入库存':'import','检测任务':'inspection','检测配置':'settings'}[name]
 if (page.viewportSize().width >= 768) {
  const toggle=page.getByRole('button',{name:'展开销售工作台子菜单',exact:true})
  if(await toggle.isVisible()) await toggle.click()
  await page.locator('.sidebar-sales-sections').getByRole('link',{name,exact:true}).click()
 } else {
  await page.locator('.sales-mobile-navigation .n-base-selection').click()
  await page.locator('.n-base-select-option').filter({hasText:new RegExp('^'+name+'$')}).filter({visible:true}).last().click()
 }
 await page.waitForURL(url => (url.searchParams.get('tab')||'overview')===key)
 await pane().waitFor()
}
async function api(route, method = 'GET', data) {
 return page.evaluate(async ({ route, method, data, csrf }) => {
  const r = await fetch('/api/' + route, { method, headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf }, body: data === undefined ? undefined : JSON.stringify(data) })
  const body = await r.json(); if (!r.ok) throw new Error(`${r.status} ${body.error}`); return body
 }, { route, method, data, csrf })
}
async function choose(label, value, root = pane()) {
 await root.locator('.n-form-item').filter({ has: page.getByText(label, { exact: true }) }).locator('.n-base-selection').click()
 await page.locator('.n-base-select-option').filter({ hasText: value }).filter({ visible: true }).last().click()
}
async function search(email) {
 await tab('库存管理'); await pane().locator('.inventory-switch').getByRole('button', { name: /^子邮箱 / }).click()
 await pane().getByPlaceholder('搜索邮箱或备注').fill(email)
 await pane().getByRole('button', { name: '查询', exact: true }).click(); await page.waitForTimeout(300)
 return pane().locator('.n-data-table-tr').filter({ has: page.getByText(email, { exact: true }) })
}
async function noOverflow(label) { assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false, label) }
async function closeModal() { await modal().getByRole('button', { name: '取消', exact: true }).click(); await page.waitForFunction(() => !document.querySelector('.n-modal')) }
async function screenshot(name) {
 await page.waitForTimeout(400); await page.waitForFunction(() => !document.querySelector('.n-message'), null, { timeout: 10000 })
 await page.evaluate(() => document.querySelectorAll('.n-data-table *').forEach(el => { if (el.scrollWidth > el.clientWidth) el.scrollLeft = 0 }))
 await page.screenshot({ path: path.join(output, name), fullPage: true })
}
try {
 await page.goto(base + '/admin/sales'); await page.getByPlaceholder('输入管理员密码').fill('test-password-12345'); await page.getByRole('button', { name: '登录管理台' }).click()
 await page.getByRole('heading', { name: '销售工作台', exact: true }).waitFor(); csrf = (await api('session')).csrf
 // Reject unintended external requests before any network operation takes place.
 await page.route('**/api/admin/mail/inspection-schedule/run', route => route.abort())
 await page.route('**/api/admin/mail/inspections', async route => {
  if (route.request().method() === 'POST') { const data = route.request().postDataJSON(); assert.equal(data.mode, 'link'); assert.equal(data.scope, 'selected') }
  await route.continue()
 })
 for (const width of [360, 390, 768, 1440]) for (const theme of ['light', 'dark']) {
  await page.setViewportSize({ width, height: 1000 }); await page.evaluate(theme => localStorage.setItem('mail-lite-theme', theme), theme); await page.goto(base + '/admin/sales')
  let row = await search('recipient000@example.net')
  await row.getByText('系统关联', { exact: true }).waitFor(); await row.getByText('仅本机可访问', { exact: true }).waitFor(); await noOverflow(`${width}/${theme}/inventory`)
  await row.getByRole('button', { name: '编辑', exact: true }).click(); await modal().getByText('系统关联（自动生成）', { exact: true }).waitFor()
  assert.equal(await modal().locator('input[readonly]').count(), 1); await noOverflow(`${width}/${theme}/source-modal`); await closeModal()
  await pane().getByRole('button', { name: '一键补齐链接', exact: true }).click(); await modal().getByText('可关联 2', { exact: true }).waitFor(); await modal().getByText('跳过 3', { exact: true }).waitFor(); await noOverflow(`${width}/${theme}/sync-preview`)
  if (width === 360 || width === 1440) await screenshot(`sync-${width}-${theme}.png`)
  await closeModal()
 }
 await page.setViewportSize({ width: 1440, height: 1000 })
 await pane().getByRole('button', { name: '一键补齐链接', exact: true }).click(); await modal().getByRole('button', { name: '确认补齐', exact: true }).click(); await page.waitForFunction(() => !document.querySelector('.n-modal'))
 let all = await api('admin/mail/children?limit=100')
 for (const email of ['recipient001@example.net', 'recipient002@example.net']) assert.equal(all.items.find(v => v.email === email).link_mode, 'source')
 assert.equal(all.items.find(v => v.email === 'recipient003@example.net').link, 'https://custom.example.net/existing')
 assert.equal(all.items.find(v => v.email === 'recipient004@example.net').link_mode, 'custom')
 await pane().getByRole('button', { name: '一键补齐链接', exact: true }).click(); await modal().getByText('可关联 0', { exact: true }).waitFor(); assert.equal(await modal().getByRole('button', { name: '确认补齐', exact: true }).isDisabled(), true); await closeModal()
 let row = await search('recipient000@example.net')
 await row.getByRole('button', { name: '复制链接', exact: true }).click(); const copied = await page.evaluate(() => navigator.clipboard.readText()); assert.ok(copied.startsWith(base + '/show/'))
 const opened = context.waitForEvent('page'); await row.getByRole('link', { name: '打开链接', exact: true }).click(); const reader = await opened; await reader.getByRole('heading', { name: '最新邮件', exact: true }).waitFor(); assert.ok((await reader.locator('body').innerText()).includes('recipient000@example.net')); await reader.close()
 await row.getByRole('button', { name: '编辑', exact: true }).click(); await choose('链接模式', '自定义链接', modal()); await modal().getByPlaceholder('https://…').fill('https://custom.example.net/override'); await modal().getByRole('button', { name: '保存', exact: true }).click(); await page.waitForFunction(() => !document.querySelector('.n-modal'))
 all = await api('admin/mail/children?limit=100'); assert.equal(all.items.find(v => v.email === 'recipient000@example.net').link_mode, 'custom')
 row = await search('recipient000@example.net'); await row.getByRole('button', { name: '编辑', exact: true }).click(); await choose('链接模式', '系统关联（自动生成）', modal()); await modal().getByRole('button', { name: '保存', exact: true }).click(); await page.waitForFunction(() => !document.querySelector('.n-modal'))
 row = await search('recipient000@example.net'); await row.getByRole('checkbox').check(); await tab('检测任务'); await choose('检测范围', '选中子邮箱 (1)')
 await pane().getByRole('button', { name: '提交链接检测', exact: true }).click()
 let task
 for (let i = 0; i < 40; i++) { task = (await api('admin/mail/inspections')).items[0]; if (task?.status === 'completed') break; await page.waitForTimeout(150) }
 assert.equal(task.passed, 1); const detail = await api(`admin/mail/inspections/${task.id}`); assert.ok(detail.results[0].message.includes('内部取信'))
 await modal().locator('.n-card-header__close').click(); await page.waitForFunction(() => !document.querySelector('.n-modal'))
 row = await search('recipient000@example.net'); await row.getByText('链接 通过', { exact: true }).waitFor(); await screenshot('source-inventory-1440.png')
 await page.setViewportSize({ width: 360, height: 844 }); await noOverflow('final mobile inventory'); await screenshot('source-inventory-360.png')
 assert.deepEqual(errors, [])
 console.log('PASS: 8 width/theme combinations; automatic source URL and local-only hint; read-only source editor; backfill preview/commit/idempotency preserving custom/reserved; copy/open reader; manual override and restore; internal link inspection; no SMTP or external HTTP. Screenshots: ' + output)
} catch (error) { await page.screenshot({ path: path.join(output, 'failure.png'), fullPage: true }).catch(() => {}); throw error }
finally { await browser.close() }
