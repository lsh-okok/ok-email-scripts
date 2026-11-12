// Run only against the isolated, opt-in Go browser fixture:
// MAIL_LITE_QA_SOURCE_LINKS=1 MAIL_LITE_QA_ADDR=127.0.0.1:18096 go test ./internal/core -run '^TestBrowserFixture$' -v -count=1 -timeout=20m
// QA_BASE_URL=http://127.0.0.1:18096 node web/ui/mapping-browser-test.mjs
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'

const { chromium } = await import('playwright').catch(() => import('playwright-core'))
const base = process.env.QA_BASE_URL || 'http://127.0.0.1:18096'
const fixtureURL = new URL(base)
assert.ok(['127.0.0.1', 'localhost'].includes(fixtureURL.hostname) && fixtureURL.port !== '8081', 'Use an isolated TestBrowserFixture, never the live service')
const output = path.join(os.tmpdir(), 'mail-lite-mapping-browser')
await fs.mkdir(output, { recursive: true })
const browser = await chromium.launch({ headless: true, executablePath: process.env.BROWSER_PATH || 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe' })
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
const page = await context.newPage(), errors = [], previewRequests = []
page.setDefaultTimeout(15000)
page.on('pageerror', error => errors.push(error.message))
const previewPattern = /\/api\/admin\/mail\/mapping\/preview(?:\?|$)/
page.on('request', request => { if (previewPattern.test(request.url())) previewRequests.push(new URL(request.url())) })
const pane = () => page.locator('.n-tab-pane').filter({ visible: true }).last()
const refresh = () => pane().getByRole('button').filter({ hasText: '刷新预览' })
const commit = () => pane().getByRole('button').filter({ hasText: '提交全部映射' })
const children = () => pane().locator('.mapping-toolbar + .n-spin-container .mapping-row')
async function ready() {
 await page.waitForFunction(() => [...document.querySelectorAll('button')].some(button => button.textContent.trim() === '刷新预览' && !button.disabled))
 await page.waitForFunction(() => !document.querySelector('.n-spin-body'))
 assert.equal(await commit().isDisabled(), false, 'commit must be enabled after a successful preview')
}
async function chooseFilter(label) {
 const response = page.waitForResponse(response => previewPattern.test(response.url()))
 await pane().locator('.mapping-toolbar .n-base-selection').click()
 await page.locator('.n-base-select-option').filter({ hasText: new RegExp(`^${label}$`) }).filter({ visible: true }).last().click()
 const result = await response
 await ready()
 return result.json()
}
function assertRequest(url, { page: expectedPage, issues }) {
 assert.equal(url.searchParams.get('page'), String(expectedPage))
 assert.equal(url.searchParams.get('limit'), '50')
 assert.equal(url.searchParams.get('issues_only'), issues ? '1' : null)
}
try {
 // This endpoint exists only in the Go test fixture; check before logging in.
 const marker = await context.request.get(base + '/__qa/stats')
 assert.equal(marker.status(), 200, 'TestBrowserFixture marker is missing')
 assert.equal(typeof await marker.json(), 'object')
 await page.goto(base + '/admin/sales?tab=mapping')
 await page.getByPlaceholder('输入管理员密码').fill('test-password-12345')
 const firstPreview = page.waitForResponse(response => previewPattern.test(response.url()))
 await page.getByRole('button', { name: '登录管理台' }).click()
 const real = await (await firstPreview).json()
 await pane().getByRole('heading', { name: '当前账号映射', exact: true }).waitFor()
 await ready()
 assertRequest(previewRequests.at(-1), { page: 1, issues: true })
 assert.equal(real.summary.links, 5, 'seed the fixture with MAIL_LITE_QA_SOURCE_LINKS=1')
 assert.equal(real.summary.mapped_links, 5)
 assert.equal(real.total, 3)
 assert.equal(real.links.length, 3)
 assert.equal(await children().count(), 3)
 for (const item of real.links) assert.ok(item.conflict || item.needs_detection_link)
 const all = await chooseFilter('全部子邮箱')
 assertRequest(previewRequests.at(-1), { page: 1, issues: false })
 assert.equal(all.total, 5)
 assert.equal(await children().count(), 5)
 assert.equal(await children().getByText('已映射', { exact: true }).count(), 2, 'ready links need their own successful state')

 // Mock only preview details to exercise multiple pages without modifying data.
 const dataset = Array.from({ length: 130 }, (_, index) => ({
  source_id: `qa-child-${String(index).padStart(3, '0')}`,
  account_id: real.accounts[0].source_id,
  recipient: `child${String(index).padStart(3, '0')}@child.example.test`,
  mailbox_id: 1000 + index, parent_id: real.accounts[0].mailbox_id,
  mapped: true, needs_detection_link: index < 65,
  ...(index === 0 ? { conflict: 'QA 映射冲突' } : {}),
 }))
 let failNext = false, holdNext = null
 await page.route(previewPattern, async route => {
  const url = new URL(route.request().url())
  if (failNext) { failNext = false; await route.fulfill({ status: 500, json: { error: 'QA 预览暂时不可用' } }); return }
  if (holdNext) { const gate = holdNext; holdNext = null; gate.started(); await gate.wait }
  const filtered = url.searchParams.get('issues_only') === '1' ? dataset.filter(item => item.conflict || item.needs_detection_link) : dataset
  const limit = Math.max(1, Math.min(200, Number(url.searchParams.get('limit')) || 50))
  const currentPage = Math.max(1, Math.min(Math.ceil(filtered.length / limit), Number(url.searchParams.get('page')) || 1))
  await route.fulfill({ json: {
   accounts: real.accounts,
   links: filtered.slice((currentPage - 1) * limit, currentPage * limit),
   summary: { ...real.summary, links: dataset.length, mapped_links: dataset.length, conflicts: 1 },
   page: currentPage, limit, total: filtered.length,
  } })
 })
 let result = await chooseFilter('需要处理的子邮箱')
 assert.equal(result.total, 65)
 assert.equal(result.links.length, 50)
 assert.equal(await children().count(), 50, 'only the current page may be rendered')
 const [secondPage] = await Promise.all([
  page.waitForResponse(response => previewPattern.test(response.url())),
  pane().locator('.n-pagination-item--button').last().click(),
 ])
 result = await secondPage.json()
 await ready()
 assertRequest(previewRequests.at(-1), { page: 2, issues: true })
 assert.equal(result.links.length, 15)
 assert.equal(await children().count(), 15)
 assert.ok((await children().first().innerText()).includes('child050@child.example.test'))
 assert.equal(await pane().locator('.mapping-card').nth(1).locator('strong').innerText(), '130', 'summary must cover every page')
 result = await chooseFilter('全部子邮箱')
 assertRequest(previewRequests.at(-1), { page: 1, issues: false })
 assert.equal(result.page, 1, 'changing the filter must reset pagination')
 assert.equal(result.total, 130)
 assert.equal(await children().count(), 50)

 // Keep one preview request pending and prove controls prevent extra requests.
 let release, started
 const startedPromise = new Promise(resolve => { started = resolve })
 const wait = new Promise(resolve => { release = resolve })
 holdNext = { started, wait }
 const before = previewRequests.length
 await refresh().click()
 await startedPromise
 assert.equal(await refresh().isDisabled(), true)
 assert.equal(await commit().isDisabled(), true)
 assert.ok(await pane().locator('.mapping-toolbar .n-base-selection--disabled').count())
 await refresh().evaluate(button => { button.click(); button.click() })
 await commit().evaluate(button => button.click())
 assert.equal(previewRequests.length, before + 1, 'disabled duplicate clicks triggered more requests')
 release()
 await ready()

 failNext = true
 await refresh().click()
 await pane().getByText('映射预览加载失败', { exact: true }).waitFor()
 await pane().getByText('QA 预览暂时不可用', { exact: true }).waitFor()
 assert.equal(await commit().isDisabled(), true, 'an unsuccessful preview must not be submitted')
 assert.equal(await children().count(), 0, 'failed refresh left stale details visible')
 await pane().getByRole('button', { name: '重试', exact: true }).click()
 await ready()
 assert.equal(await pane().getByText('映射预览加载失败', { exact: true }).count(), 0)
 assert.equal(await children().count(), 50)

 for (const width of [390, 1440]) for (const theme of ['light', 'dark']) {
  await page.setViewportSize({ width, height: 1000 })
  await page.evaluate(value => localStorage.setItem('mail-lite-theme', value), theme)
  await page.reload()
  await ready()
  assertRequest(previewRequests.at(-1), { page: 1, issues: true })
  assert.equal(await children().count(), 50)
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false, `${width}/${theme}: horizontal overflow`)
  await page.screenshot({ path: path.join(output, `${width}-${theme}.png`), fullPage: true })
  if (width === 1440) {
   await pane().locator('.mapping-toolbar').scrollIntoViewIfNeeded()
   await page.screenshot({ path: path.join(output, `${width}-${theme}-children.png`), fullPage: true })
   await pane().locator('.n-pagination').scrollIntoViewIfNeeded()
   await page.screenshot({ path: path.join(output, `${width}-${theme}-pagination.png`), fullPage: true })
  }
 }
 assert.deepEqual(errors, [])
 console.log(`PASS: real mapped/source/custom preview; server filters and pagination; global totals; bounded child rows; disabled duplicate requests; error/retry; 390/1440 light/dark layouts. Screenshots: ${output}`)
} catch (error) {
 await page.screenshot({ path: path.join(output, 'failure.png'), fullPage: true }).catch(() => {})
 throw error
} finally { await browser.close() }
