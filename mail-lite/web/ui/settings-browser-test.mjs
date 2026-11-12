// Use the opt-in TestBrowserFixture with an isolated temporary database.
// MAIL_LITE_QA_ADDR=127.0.0.1:18095 go test ./internal/core -run '^TestBrowserFixture$' -v -count=1 -timeout=20m
// QA_BASE_URL=http://127.0.0.1:18095 node web/ui/settings-browser-test.mjs
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import path from 'node:path'
import os from 'node:os'

const { chromium } = await import('playwright').catch(() => import('playwright-core'))

const base = process.env.QA_BASE_URL || 'http://127.0.0.1:18095'
const output = path.join(os.tmpdir(), 'mail-lite-settings-browser')
await fs.mkdir(output, { recursive: true })
const browser = await chromium.launch({ headless: true, executablePath: process.env.BROWSER_PATH || 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe' })
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
const page = await context.newPage(), errors = []
page.on('pageerror', error => errors.push(error.message))
page.setDefaultTimeout(15000)
const currentPassword = 'test-password-12345', newPassword = 'settings-updated-password-67890'
async function login(target, password) {
 await target.goto(base + '/admin/settings')
 await target.getByPlaceholder('输入管理员密码').fill(password)
 await target.getByRole('button', { name: '登录管理台' }).click()
 await target.getByRole('heading', { name: '系统设置', exact: true }).waitFor()
 await target.getByRole('button', { name: '保存常规设置', exact: true }).waitFor({ state: 'visible' })
}
async function fillPasswords(current, next, confirmation = next) {
 await page.getByLabel('当前密码', { exact: true }).fill(current)
 await page.getByLabel('新密码', { exact: true }).fill(next)
 await page.getByLabel('确认新密码', { exact: true }).fill(confirmation)
}
async function settingsReady() {
 await page.waitForFunction(() => [...document.querySelectorAll('.settings-page button')].find(button => button.textContent.includes('保存常规设置'))?.disabled === false)
 await page.waitForTimeout(350) // Let the loading mask's exit transition finish.
}
try {
 await login(page, currentPassword)
 const otherContext = await browser.newContext()
 const otherPage = await otherContext.newPage()
 await login(otherPage, currentPassword)

 // The general settings form saves and reloads actual server values.
 await page.getByPlaceholder('https://mail.example.com', { exact: true }).fill('https://settings.example.test')
 await page.getByRole('button', { name: '保存常规设置', exact: true }).click()
 await page.getByText('系统设置已保存', { exact: true }).waitFor()
 await page.reload()
 await page.waitForFunction(() => document.querySelector('input[placeholder="https://mail.example.com"]')?.value === 'https://settings.example.test')

 for (const width of [360, 390, 768, 1440]) for (const theme of ['light', 'dark']) {
  await page.setViewportSize({ width, height: 1000 })
  await page.evaluate(theme => localStorage.setItem('mail-lite-theme', theme), theme)
  await page.reload()
  await page.getByRole('heading', { name: '系统设置', exact: true }).waitFor()
  await settingsReady()
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false, `${width}/${theme}: horizontal overflow`)
  const nav = width < 768 ? page.locator('.bottom-nav') : page.locator('.sidebar')
  await nav.getByRole('link', { name: width < 768 ? '设置' : '系统设置', exact: true }).waitFor()
  const confirmation = await page.getByLabel('确认新密码', { exact: true }).boundingBox()
  const submit = await page.getByRole('button', { name: '修改管理员密码', exact: true }).boundingBox()
  assert.ok(confirmation && submit && submit.y > confirmation.y + confirmation.height, 'password action overlaps form fields')
  await page.screenshot({ path: path.join(output, `${width}-${theme}.png`), fullPage: true })
 }

 await page.getByRole('button', { name: '节点管理', exact: true }).click()
 await page.getByRole('heading', { name: '节点管理', exact: true }).waitFor()
 await page.getByRole('button', { name: '系统设置', exact: true }).click()
 await page.getByRole('heading', { name: '系统设置', exact: true }).waitFor()
 await fillPasswords(currentPassword, newPassword, 'mismatched-password')
 await page.getByRole('button', { name: '修改管理员密码', exact: true }).click()
 await page.getByText('两次输入的新密码不一致', { exact: true }).waitFor()
 await fillPasswords('incorrect-password', newPassword)
 await page.getByRole('button', { name: '修改管理员密码', exact: true }).click()
 await page.getByText('当前密码不正确', { exact: true }).waitFor()
 await fillPasswords(currentPassword, newPassword)
 await page.getByRole('button', { name: '修改管理员密码', exact: true }).click()
 await page.getByPlaceholder('输入管理员密码').waitFor()
 assert.equal(await otherPage.evaluate(async () => (await fetch('/api/admin/settings')).status), 401, 'other device retained its login')
 await otherContext.close()
 await page.getByPlaceholder('输入管理员密码').fill(currentPassword)
 await page.getByRole('button', { name: '登录管理台' }).click()
 await page.getByText('密码不正确', { exact: true }).waitFor()
 await page.getByPlaceholder('输入管理员密码').fill(newPassword)
 await page.getByRole('button', { name: '登录管理台' }).click()
 await page.getByRole('heading', { name: '系统设置', exact: true }).waitFor()
 await page.getByLabel('当前密码', { exact: true }).waitFor()
 assert.equal(await page.getByLabel('当前密码', { exact: true }).inputValue(), '')
 assert.deepEqual(errors, [])
 console.log(`Settings browser checks passed; screenshots: ${output}`)
} finally {
 await browser.close()
}
