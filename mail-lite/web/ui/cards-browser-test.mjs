// Build web/dist, then start the opt-in TestBrowserFixture on an isolated port.
// QA_BASE_URL=http://127.0.0.1:18094 node web/ui/cards-browser-test.mjs
// Uses fake fixture accounts only; no SMTP or detection task is started.
import { chromium } from 'playwright-core'
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import path from 'node:path'

const base = process.env.QA_BASE_URL || 'http://127.0.0.1:18094'
const output = process.env.QA_OUTPUT_DIR || path.resolve(import.meta.dirname, '../../.cache/cards-qa')
const browser = await chromium.launch({ headless: true, executablePath: process.env.BROWSER_PATH || 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe' })
const admin = await browser.newContext({ viewport: { width: 1440, height: 1000 }, colorScheme: 'light', acceptDownloads: true })
const publicContext = await browser.newContext({ viewport: { width: 1440, height: 1000 }, colorScheme: 'light', acceptDownloads: true })
await admin.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: base })
await publicContext.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: base })
const page = await admin.newPage(), redeem = await publicContext.newPage(), errors = []
let csrf = '', forbiddenSends = 0
for (const p of [page, redeem]) { p.on('pageerror', error => errors.push(error.message)); p.setDefaultTimeout(15000) }
await fs.mkdir(output, { recursive: true })
async function api(route, method = 'GET', data) {
 const response = await page.evaluate(async ({ route, method, data, csrf }) => {
  const result = await fetch('/api/' + route, { method, headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf }, body: data === undefined ? undefined : JSON.stringify(data) })
  return { status: result.status, body: await result.json() }
 }, { route, method, data, csrf })
 assert.ok(response.status < 400, `${method} ${route}: ${response.status}: ${JSON.stringify(response.body)}`)
 return response.body
}
async function noOverflow(p, label) {
 const dimensions = await p.evaluate(() => ({ width: innerWidth, scroll: document.documentElement.scrollWidth }))
 assert.ok(dimensions.scroll <= dimensions.width + 1, `${label}: ${JSON.stringify(dimensions)}`)
}
async function downloadText(p, button) {
 const [file] = await Promise.all([p.waitForEvent('download'), button.click()])
 const chunks = [], stream = await file.createReadStream()
 for await (const chunk of stream) chunks.push(chunk)
 return { name: file.suggestedFilename(), text: Buffer.concat(chunks).toString('utf8').replace(/^\uFEFF/, '') }
}
function assertRedemptionExport(file, results) {
 assert.match(file.name, /^\d{8}-[0-9A-F]{8}\.txt$/, 'Filename must contain the date and a random suffix')
 const expected = results.filter(item => item.success).flatMap(item => (item.links || []).map(link => `${link.recipient.trim()}----${link.url.trim()}`)).join('\n')
 assert.equal(file.text, expected, 'Export must contain only consecutive child mailbox/link rows')
 assert.doesNotMatch(file.text, /主邮箱/)
 for (const line of file.text.split('\n')) {
  assert.equal(line, line.trim(), 'Export rows must not contain surrounding whitespace')
  assert.match(line, /^\S+----https?:\/\/\S+$/, 'Every export row must contain an address and URL without spaces or blank lines')
 }
}
async function submitCodes(codes) {
 await redeem.locator('#codes').fill(codes)
 const response = redeem.waitForResponse(result => new URL(result.url()).pathname === '/api/redeem' && result.request().method() === 'POST')
 await redeem.getByRole('button', { name: '批量兑换', exact: true }).click()
 const result = await response
 assert.equal(result.status(), 200)
 await redeem.getByRole('button', { name: '批量兑换', exact: true }).waitFor()
 return result.json()
}
async function waitForJob(id) {
 for (let index = 0; index < 50; index++) {
  const jobs = await api('admin/jobs'), job = jobs.items.find(item => item.id === id)
  if (job && !['running', 'queued'].includes(job.status)) { assert.equal(job.failed, 0, JSON.stringify(job)); return }
  await page.waitForTimeout(100)
 }
 assert.fail('Import job did not finish: ' + id)
}
async function addLink(accountID, recipient) {
 const job = await api('admin/import', 'POST', { kind: 'links', account_id: accountID, text: recipient })
 await waitForJob(job.id)
}
function cardRow(email) { return page.locator('.n-data-table-tr').filter({ has: page.getByText(email, { exact: true }) }) }
async function screenshot(p, name) {
 await p.waitForFunction(() => !document.querySelector('main .n-spin-content--spinning,main .n-button--loading') && [...document.querySelectorAll('main .n-spin-content')].every(element => Number(getComputedStyle(element).opacity) >= 0.99), null, { timeout: 10000 })
 await p.screenshot({ path: path.join(output, name), fullPage: true })
}

try {
 // Block every possible send/start action before it can reach the fixture.
 await admin.route('**/api/admin/mail/**', async route => {
  const request = route.request(), pathname = new URL(request.url()).pathname
  if (request.method() === 'POST' && (/\/inspections(?:\/|$)/.test(pathname) || pathname.endsWith('/inspection-schedule/run'))) { forbiddenSends++; await route.abort('blockedbyclient') }
  else await route.continue()
 })
 if (process.env.QA_DIST_FILES === '1') {
  const dist = path.resolve(import.meta.dirname, '../dist')
  await admin.route(base + '/admin/**', async route => {
   const pathname = decodeURIComponent(new URL(route.request().url()).pathname)
   const filename = pathname.startsWith('/admin/assets/') ? path.resolve(dist, pathname.slice('/admin/'.length)) : path.join(dist, 'index.html')
   assert.ok(filename.startsWith(dist + path.sep)); await route.fulfill({ path: filename })
  })
 }
 await page.goto(base + '/admin/cards')
 await page.getByPlaceholder('输入管理员密码').fill('test-password-12345')
 await page.getByRole('button', { name: '登录管理台' }).click()
 await page.getByRole('heading', { name: '卡密管理', exact: true }).waitFor()
 csrf = (await api('session')).csrf
 assert.equal(await page.locator('.sidebar').getByRole('link', { name: /销售/ }).count(), 0)
 let main = (await api('admin/cards?page_size=100')).items.find(row => row.account_id === 'personal')
 let empty = (await api('admin/cards?page_size=100')).items.find(row => row.account_id === 'work')
 assert.ok(main && empty, 'Missing fixture accounts')
 // Repeated runs use equivalent fake accounts, retaining prior generated cards.
 if (main.code || empty.code) {
  const suffix = Date.now().toString()
  const added = []
  for (const name of ['personal', 'work']) {
   const account = await api('admin/accounts', 'POST', { email: `qa-card-${name}-${suffix}@outlook.com`, provider: 'outlook', status: 'active', client_id: 'qa-client', refresh_token: 'qa-secret-refresh' })
   added.push(account.id)
  }
  await addLink(added[0], `qa-child-${suffix}@example.net`)
  const rows = (await api('admin/cards?page_size=100')).items
  main = rows.find(row => row.account_id === added[0]); empty = rows.find(row => row.account_id === added[1])
 }
 await page.getByRole('button', { name: '刷新', exact: true }).click()
 await cardRow(main.email).getByRole('checkbox').check()
 await cardRow(empty.email).getByRole('checkbox').check()
 await page.getByRole('button', { name: '生成所选卡密（2）', exact: true }).click()
 const modal = page.getByRole('dialog').filter({ has: page.getByText('卡密已生成', { exact: true }) })
 await modal.waitFor()
 const exportedCodes = await downloadText(page, modal.getByRole('button', { name: '下载 TXT', exact: true }))
 assert.match(exportedCodes.name, /\.txt$/)
 assert.equal(exportedCodes.text.trim().split(/\r?\n/).length, 2)
 const after = (await api('admin/cards?page_size=100')).items
 main = after.find(row => row.account_id === main.account_id); empty = after.find(row => row.account_id === empty.account_id)
 assert.deepEqual(new Set(exportedCodes.text.trim().split(/\r?\n/)), new Set([main.code, empty.code]))
 assert.equal(await modal.getByRole('textbox', { name: '生成的卡密' }).inputValue(), exportedCodes.text.trim())
 await modal.getByRole('button', { name: '复制全部卡密', exact: true }).click()
 assert.equal((await page.evaluate(() => navigator.clipboard.readText())).replace(/\r\n/g, '\n'), exportedCodes.text.trim())
 await noOverflow(page, 'Generated modal at 1440px'); await screenshot(page, 'cards-generated-1440.png')
 for (const width of [390, 360]) {
  await page.setViewportSize({ width, height: 844 })
  await noOverflow(page, `Generated modal at ${width}px`); await screenshot(page, `cards-generated-${width}.png`)
 }
 await page.setViewportSize({ width: 1440, height: 1000 })
 await modal.locator('.n-card-header__close').click(); await modal.waitFor({ state: 'hidden' })
 assert.equal(await cardRow(main.email).locator('.card-identity').evaluate(element => getComputedStyle(element).display), 'grid', 'Table email and provider must have separate lines')
 assert.equal(await cardRow(main.email).locator('.card-code').evaluate(element => getComputedStyle(element).overflowWrap), 'anywhere', 'Table card codes must wrap inside their column')
 const reused = await api('admin/cards', 'POST', { account_ids: [main.account_id, empty.account_id] })
 assert.equal(reused.items.find(row => row.account_id === main.account_id).code, main.code, 'Generation must retain the fixed binding')
 assert.equal(reused.items.find(row => row.account_id === empty.account_id).code, empty.code)

 // A fresh context is deliberately unauthenticated.
 assert.deepEqual(await publicContext.cookies(), [])
 await redeem.goto(base + '/redeem')
 await redeem.getByRole('heading', { name: '兑换卡密', exact: true }).waitFor()
 await redeem.locator('#codes').fill(Array.from({ length: 101 }, (_, index) => `too-many-${index}`).join('\n'))
 await redeem.getByRole('button', { name: '批量兑换', exact: true }).click()
 await redeem.locator('#feedback').getByText('一次最多兑换 100 个卡密，请分批提交。', { exact: true }).waitFor()
 assert.equal(await redeem.locator('#results-section').isVisible(), false)
 await redeem.locator('#codes').fill('')
 const [chooser] = await Promise.all([redeem.waitForEvent('filechooser'), redeem.getByRole('button', { name: '导入 TXT', exact: true }).click()])
 await chooser.setFiles({ name: 'cards.txt', mimeType: 'text/plain', buffer: Buffer.from(exportedCodes.text) })
 await redeem.locator('#feedback').getByText('已导入卡密，请点击批量兑换。', { exact: true }).waitFor()
 assert.equal((await redeem.locator('#codes').inputValue()).trim(), exportedCodes.text.trim())
 const invalid = 'invalid-card-for-browser-qa'
 const batch = await submitCodes(`${main.code}\n${empty.code}, ${invalid}\n${main.code}`)
 assert.equal(batch.items.length, 3, 'Duplicate codes must be removed')
 assert.equal(batch.items.filter(item => item.success).length, 1)
 assert.equal(batch.items.find(item => item.code === main.code).account_email, main.email)
 assert.ok(batch.items.find(item => item.code === main.code).links.length >= 1)
 assert.equal(batch.items.find(item => item.code === empty.code).success, false)
 assert.equal(batch.items.find(item => item.code === invalid).success, false)
 await redeem.locator('#summary').getByText('1 个成功 · 2 个失败', { exact: false }).waitFor()
 assert.equal(await redeem.locator('#count').textContent(), '已输入 3 个')
 const serialized = JSON.stringify(batch)
 assert.doesNotMatch(serialized, /secret-refresh|qa-secret-refresh|qa-app-password|proxy-pass|refresh_token|client_id|"password"/)
 assert.doesNotMatch(await redeem.locator('main').textContent(), /secret-refresh|qa-app-password|proxy-pass/)
 const content = await downloadText(redeem, redeem.getByRole('button', { name: '下载 TXT', exact: true }))
 assertRedemptionExport(content, batch.items)
 assert.ok(!content.text.includes(main.email))
 assert.ok(!content.text.includes(empty.email)); assert.ok(!content.text.includes(main.code)); assert.ok(!content.text.includes(invalid))
 const repeatedDownload = await downloadText(redeem, redeem.getByRole('button', { name: '下载 TXT', exact: true }))
 assertRedemptionExport(repeatedDownload, batch.items)
 assert.notEqual(repeatedDownload.name, content.name, 'Each download must generate a new random filename')
 await redeem.getByRole('button', { name: '复制全部结果', exact: true }).click()
 assert.equal((await redeem.evaluate(() => navigator.clipboard.readText())).replace(/\r\n/g, '\n'), content.text)
 await screenshot(redeem, 'redeem-batch-1440.png')
 const again = await submitCodes(main.code)
 assert.equal(again.items[0].success, true); assert.equal(again.items[0].reused, true)
 await redeem.getByText('已兑换 · 结果已找回', { exact: true }).waitFor()

 // A failed empty account remains unredeemed. Adding a link makes retry succeed.
 assert.equal((await api('admin/cards?page_size=100')).items.find(row => row.account_id === empty.account_id).status, 'unused')
 await submitCodes(`${main.code}\n${empty.code}\n${invalid}`)
 await addLink(empty.account_id, `work-child-${Date.now()}@example.net`)
 const retryResponse = redeem.waitForResponse(result => new URL(result.url()).pathname === '/api/redeem')
 await redeem.getByRole('button', { name: '重试失败卡密', exact: true }).click()
 const retry = await retryResponse
 assert.deepEqual(JSON.parse(retry.request().postData()).codes, [empty.code, invalid], 'Retry must submit only failed codes')
 const retryItems = (await retry.json()).items
 assert.equal(retryItems.find(item => item.code === empty.code).success, true)
 await redeem.locator('#summary').getByText('2 个成功 · 1 个失败', { exact: false }).waitFor()
 assert.equal(await redeem.locator('#results .card').count(), 3, 'Retry must retain the previous successful result')
 const combinedExport = await downloadText(redeem, redeem.getByRole('button', { name: '下载 TXT', exact: true }))
 assertRedemptionExport(combinedExport, [batch.items.find(item => item.code === main.code), retryItems.find(item => item.code === empty.code)])
 assert.ok(!combinedExport.text.includes(main.email)); assert.ok(!combinedExport.text.includes(empty.email))
 await redeem.getByRole('button', { name: '复制全部结果', exact: true }).click()
 assert.equal((await redeem.evaluate(() => navigator.clipboard.readText())).replace(/\r\n/g, '\n'), combinedExport.text)
 await screenshot(redeem, 'redeem-retry-1440.png')

 // Disable/recover through the real admin page, then redeem in the public context.
 await page.getByRole('button', { name: '刷新', exact: true }).click()
 await cardRow(main.email).getByRole('button', { name: '停用', exact: true }).click()
 await cardRow(main.email).getByRole('button', { name: '恢复', exact: true }).waitFor()
 const disabled = await submitCodes(main.code)
 assert.equal(disabled.items[0].success, false)
 await cardRow(main.email).getByRole('button', { name: '恢复', exact: true }).click()
 await cardRow(main.email).getByRole('button', { name: '停用', exact: true }).waitFor()
 assert.equal((await submitCodes(main.code)).items[0].success, true)

 // The public page handles transport failure and permits recovery with the same code.
 await redeem.route('**/api/redeem', route => route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: '模拟临时故障' }) }), { times: 1 })
 await redeem.getByRole('button', { name: '批量兑换', exact: true }).click()
 await redeem.locator('#feedback').getByText('模拟临时故障', { exact: true }).waitFor()
 assert.equal(await redeem.locator('#submit').isEnabled(), true)
 assert.equal((await submitCodes(main.code)).items[0].success, true)

 for (const width of [1440, 390, 360]) {
  await page.setViewportSize({ width, height: width === 1440 ? 1000 : 844 })
  await page.goto(base + '/admin/cards'); await page.getByRole('heading', { name: '卡密管理', exact: true }).waitFor()
  await noOverflow(page, `cards ${width}`); await screenshot(page, `cards-${width}.png`)
  await redeem.setViewportSize({ width, height: width === 1440 ? 1000 : 844 })
  await submitCodes(`${main.code}\n${empty.code}\n${invalid}`)
  await noOverflow(redeem, `redeem ${width}`); await screenshot(redeem, `redeem-${width}.png`)
  await page.goto(base + '/admin/verification'); await page.getByRole('heading', { name: '邮箱验证', exact: true }).waitFor()
  await page.getByRole('button', { name: '发件邮箱与定时检测', exact: true }).click()
  await page.getByRole('heading', { name: '检测配置', exact: true }).waitFor()
  const presets = page.locator('.smtp-presets').first()
  await presets.locator('.n-base-selection').click()
  await page.locator('.n-base-select-option').filter({ hasText: /^iCloud 邮箱$/ }).filter({ visible: true }).click()
  await presets.getByText('smtp.mail.me.com · 587 · STARTTLS', { exact: true }).waitFor()
  await page.getByRole('heading', { name: '默认 SMTP 配置', exact: true }).click()
  await page.locator('.n-base-select-menu').filter({ visible: true }).waitFor({ state: 'hidden' })
  await page.getByRole('heading', { name: 'SMTP 发件池', exact: true }).waitFor()
  await noOverflow(page, `verification settings ${width}`); await screenshot(page, `verification-settings-${width}.png`)
  await page.goto(base + '/admin/home'); await page.getByRole('heading', { name: '首页', exact: true }).waitFor()
  assert.doesNotMatch(await page.locator('main').textContent(), /销售|库存|出库/)
  assert.equal(await page.locator('.sales-summary').count(), 0)
  await noOverflow(page, `dashboard ${width}`)
 }
 // Reverse proxies may expose the public app under a path prefix. Use the
 // latest saved template so this also tests changes made after fixture startup.
 const prefixed = await publicContext.newPage()
 prefixed.on('pageerror', error => errors.push(error.message))
 const publicTemplate = (await fs.readFile(path.resolve(import.meta.dirname, '../templates/redeem.html'), 'utf8')).replaceAll('{{.Nonce}}', 'qa-nonce')
 await prefixed.route(base + '/mail/redeem', route => route.fulfill({ status: 200, contentType: 'text/html;charset=utf-8', body: publicTemplate }))
 let prefixedRequests = 0
 await prefixed.route(base + '/mail/api/redeem', async route => {
  prefixedRequests++
  const response = await route.fetch({ url: base + '/api/redeem' })
  await route.fulfill({ response })
 })
 await prefixed.goto(base + '/mail/redeem')
 await prefixed.locator('#codes').fill(main.code)
 const prefixedResponse = prefixed.waitForResponse(result => new URL(result.url()).pathname === '/mail/api/redeem')
 await prefixed.getByRole('button', { name: '批量兑换', exact: true }).click()
 assert.equal((await prefixedResponse).status(), 200)
 await prefixed.getByText('已兑换 · 结果已找回', { exact: true }).waitFor()
 assert.equal(prefixedRequests, 1, 'Redeem fetch must retain the reverse proxy path prefix')
 await prefixed.close()
 assert.equal(forbiddenSends, 0, 'The test must never start a detection/send action')
 assert.deepEqual(errors, [], 'No browser runtime errors')
 console.log('PASS: admin bulk generation/download, fixed binding, public unauthenticated batch redemption/deduplication, failure retry/recovery, disable/enable, no credential leakage, retained SMTP presets, sales removal, reverse proxy path prefix, and 1440/390/360 layouts. Screenshots: ' + output)
} catch (error) {
 await screenshot(page, 'failure-admin.png').catch(() => {})
 await screenshot(redeem, 'failure-redeem.png').catch(() => {})
 throw error
} finally { await browser.close() }
