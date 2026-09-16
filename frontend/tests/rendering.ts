import '../src/styles/base.css'
import '../src/styles/layout.css'
import '../src/styles/components.css'
import '../src/styles/responsive.css'

import { DemoAdapter } from '../src/adapters/demo.js'
import { ProxyConsole } from '../src/app.js'
import type { RequestResult, RequestSpec, SectionID } from '../src/types.js'

const fixtureTimeout = 2_000

class ObservedDemoAdapter extends DemoAdapter {
  requestsStarted = 0
  requestsCompleted = 0

  constructor() {
    super(null)
  }

  override async sendRequest(request: RequestSpec): Promise<RequestResult> {
    this.requestsStarted += 1
    try {
      return await super.sendRequest(request)
    } finally {
      this.requestsCompleted += 1
    }
  }
}

function delay(milliseconds: number): Promise<void> {
  return new Promise((resolve) => window.setTimeout(resolve, milliseconds))
}

function nextFrame(): Promise<void> {
  return new Promise((resolve) => window.requestAnimationFrame(() => resolve()))
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

function assert(condition: unknown, failure: string): asserts condition {
  if (!condition) throw new Error(failure)
}

function required<T extends Element>(scope: ParentNode, selector: string): T {
  const element = scope.querySelector<T>(selector)
  if (!element) throw new Error(`Missing test subject: ${selector}`)
  return element
}

async function waitFor<T>(
  read: () => T | null | undefined | false | Promise<T | null | undefined | false>,
  description: string,
  timeout = fixtureTimeout,
): Promise<T> {
  const deadline = performance.now() + timeout
  let lastError: unknown
  while (performance.now() < deadline) {
    try {
      const value = await read()
      if (value) return value
    } catch (error) {
      lastError = error
    }
    await delay(20)
  }
  throw new Error(`${description} timed out after ${timeout} ms${lastError ? `: ${message(lastError)}` : ''}`)
}

function visibleSent(root: ParentNode): number {
  const value = required<HTMLElement>(root, '.traffic-summary > div:first-child strong').textContent ?? ''
  return Number(value.replace(/[^0-9]/g, ''))
}

async function stableCounter(read: () => number, stableFor = 180, timeout = fixtureTimeout): Promise<number> {
  const deadline = performance.now() + timeout
  let value = read()
  let stableSince = performance.now()
  while (performance.now() < deadline) {
    await delay(25)
    const next = read()
    if (next !== value) {
      value = next
      stableSince = performance.now()
    } else if (performance.now() - stableSince >= stableFor) {
      return value
    }
  }
  throw new Error(`traffic counter did not settle within ${timeout} ms`)
}

const appRoot = required<HTMLElement>(document, '#app')
const runButton = required<HTMLButtonElement>(document, '#run-tests')
const statusLine = required<HTMLElement>(document, '#test-status')
const summary = required<HTMLElement>(document, '#test-summary')
const results = required<HTMLOListElement>(document, '#test-results')

window.history.replaceState(null, '', `${window.location.pathname}${window.location.search}#request`)
document.documentElement.style.scrollBehavior = 'auto'

const adapter = new ObservedDemoAdapter()
const consoleApp = new ProxyConsole(appRoot, adapter, 'demo')
let passed = 0
let failed = 0

async function navigate(section: SectionID): Promise<void> {
  const link = required<HTMLAnchorElement>(appRoot, `[data-section="${section}"]`)
  link.click()
  await waitFor(
    () => window.location.hash === `#${section}` && appRoot.querySelector(`[data-section="${section}"].is-active`),
    `${section} navigation`,
  )
  await nextFrame()
}

async function check(name: string, test: () => Promise<void>): Promise<void> {
  statusLine.textContent = `Running: ${name}`
  const item = document.createElement('li')
  const label = document.createElement('strong')
  label.textContent = name
  item.append(label)
  results.append(item)
  try {
    await test()
    item.dataset.result = 'pass'
    item.append(document.createTextNode(' — PASS'))
    passed += 1
  } catch (error) {
    item.dataset.result = 'fail'
    item.append(document.createTextNode(' — FAIL'))
    const detail = document.createElement('small')
    detail.textContent = message(error)
    item.append(detail)
    failed += 1
  }
}

async function stopTrafficIfNeeded(): Promise<void> {
  try {
    await navigate('request')
    const toggle = appRoot.querySelector<HTMLButtonElement>('[data-action="toggle-traffic"]')
    if (toggle?.textContent?.includes('Stop traffic')) toggle.click()
  } catch {}
}

async function run(): Promise<void> {
  runButton.disabled = true
  results.replaceChildren()
  passed = 0
  failed = 0
  document.body.dataset.testStatus = 'running'

  try {
    await check('Console boots with the real DemoAdapter', async () => {
      await consoleApp.start()
      await waitFor(() => appRoot.querySelector('.sidebar-state .status-dot--ok'), 'demo adapter readiness')
      assert(required<HTMLElement>(appRoot, '.mode-badge').textContent?.trim() === 'demo', 'console did not boot in demo mode')
      adapter.setPersistence(true)
      assert(!adapter.persistenceEnabled(), 'storage-free fixture unexpectedly enabled persistence')
    })

    let shellMain: HTMLElement | null = null
    let shellNav: HTMLElement | null = null

    await check('25 RPS preserves request controls and shell nodes', async () => {
      await navigate('request')
      const rps = required<HTMLInputElement>(appRoot, '[data-traffic-rps]')
      rps.value = '25'
      rps.dispatchEvent(new Event('input', { bubbles: true }))
      rps.dispatchEvent(new Event('change', { bubbles: true }))

      const toggle = required<HTMLButtonElement>(appRoot, '[data-action="toggle-traffic"]')
      toggle.click()
      await waitFor(() => adapter.requestsStarted >= 4, '25 RPS traffic start')
      await waitFor(() => visibleSent(appRoot) >= 2, 'visible traffic counters')

      shellMain = required<HTMLElement>(appRoot, '#main-view')
      shellNav = required<HTMLElement>(appRoot, '.nav-list')
      const form = required<HTMLFormElement>(appRoot, '#request-form')
      const path = required<HTMLInputElement>(appRoot, '[data-request-field="path"]')
      const liveToggle = required<HTMLButtonElement>(appRoot, '[data-action="toggle-traffic"]')
      const editedPath = `${path.value}?fixture=rendering`
      path.value = editedPath
      path.dispatchEvent(new Event('input', { bubbles: true }))
      path.focus({ preventScroll: true })
      const pathSelectionStart = Math.min(3, editedPath.length)
      const pathSelectionEnd = Math.min(12, editedPath.length)
      path.setSelectionRange(pathSelectionStart, pathSelectionEnd, 'forward')
      const baseline = adapter.requestsCompleted

      await waitFor(() => adapter.requestsCompleted >= baseline + 4, 'render pressure from traffic')
      await nextFrame()

      assert(required(appRoot, '#main-view') === shellMain, 'main node was replaced during traffic')
      assert(required(appRoot, '.nav-list') === shellNav, 'navigation node was replaced during traffic')
      assert(required(appRoot, '#request-form') === form, 'request form was replaced during traffic')
      assert(required(appRoot, '[data-request-field="path"]') === path, 'request input was replaced during traffic')
      assert(required(appRoot, '[data-action="toggle-traffic"]') === liveToggle, 'traffic button was replaced during traffic')
      assert(document.activeElement === path, 'request input lost focus during traffic')
      assert(path.value === editedPath, 'request input value was overwritten during traffic')
      assert(path.selectionStart === pathSelectionStart && path.selectionEnd === pathSelectionEnd, 'request input selection was not preserved')

      const method = required<HTMLSelectElement>(appRoot, '[data-request-field="method"]')
      const options = Array.from(method.options)
      method.focus({ preventScroll: true })
      const selectBaseline = adapter.requestsCompleted
      await waitFor(() => adapter.requestsCompleted >= selectBaseline + 4, 'native select render pressure')
      await nextFrame()

      assert(required(appRoot, '[data-request-field="method"]') === method, 'native select was replaced during traffic')
      assert(document.activeElement === method, 'native select lost focus during traffic')
      assert(method.options.length === options.length, 'native select options changed during traffic')
      options.forEach((option, index) => assert(method.options[index] === option, `native select option ${index} was replaced`))
    })

    await check('Open details and document scroll survive traffic renders', async () => {
      await navigate('clusters')
      assert(required(appRoot, '#main-view') === shellMain, 'main node changed while navigating')
      assert(required(appRoot, '.nav-list') === shellNav, 'navigation node changed while navigating')

      const details = required<HTMLDetailsElement>(appRoot, '.cluster-details')
      details.open = true
      const maxScroll = Math.max(0, document.documentElement.scrollHeight - window.innerHeight)
      assert(maxScroll > 0, 'cluster fixture is not tall enough to exercise document scroll')
      window.scrollTo(0, Math.min(320, maxScroll))
      await nextFrame()
      const scroll = window.scrollY
      const baseline = adapter.requestsCompleted

      await waitFor(() => adapter.requestsCompleted >= baseline + 4, 'cluster render pressure')
      await nextFrame()

      assert(required(appRoot, '.cluster-details') === details, 'details node was replaced during traffic')
      assert(details.open, 'open details collapsed during traffic')
      assert(Math.abs(window.scrollY - scroll) <= 1, `document scroll moved from ${scroll} to ${window.scrollY}`)
    })

    await check('Edited config keeps identity, focus, selection and scroll', async () => {
      await navigate('config')
      const editor = required<HTMLTextAreaElement>(appRoot, '#config-editor')
      const format = required<HTMLButtonElement>(appRoot, '[data-action="format-config"]')
      const edited = `${editor.value}\n `
      editor.value = edited
      editor.dispatchEvent(new Event('input', { bubbles: true }))

      const selectionStart = Math.min(96, Math.max(0, edited.length - 8))
      const selectionEnd = Math.min(edited.length, selectionStart + 7)
      editor.focus({ preventScroll: true })
      editor.setSelectionRange(selectionStart, selectionEnd, 'forward')
      const maxEditorScroll = Math.max(0, editor.scrollHeight - editor.clientHeight)
      assert(maxEditorScroll > 0, 'config fixture is not tall enough to exercise textarea scroll')
      editor.scrollTop = Math.min(180, maxEditorScroll)
      const editorScroll = editor.scrollTop
      const documentScroll = window.scrollY
      const baseline = adapter.requestsCompleted

      await waitFor(() => adapter.requestsCompleted >= baseline + 4, 'config render pressure')
      await nextFrame()
      const current = required<HTMLTextAreaElement>(appRoot, '#config-editor')

      assert(current === editor, 'config textarea was replaced during traffic')
      assert(current.value === edited, 'edited config text was overwritten')
      assert(document.activeElement === editor, 'config textarea lost focus')
      assert(current.selectionStart === selectionStart && current.selectionEnd === selectionEnd, 'textarea selection was not preserved')
      assert(Math.abs(current.scrollTop - editorScroll) <= 1, `textarea scroll moved from ${editorScroll} to ${current.scrollTop}`)
      assert(Math.abs(window.scrollY - documentScroll) <= 1, 'document scroll changed while editing config')
      assert(required(appRoot, '[data-action="format-config"]') === format, 'config action button was replaced during traffic')
    })

    await check('Endpoint button click mutates the real simulator', async () => {
      await navigate('clusters')
      const button = Array.from(appRoot.querySelectorAll<HTMLButtonElement>('[data-action="toggle-endpoint"]')).find(
        (candidate) => !candidate.disabled && candidate.dataset.cluster && candidate.dataset.endpoint,
      )
      assert(button, 'no mutable endpoint button found')
      const clusterID = button.dataset.cluster ?? ''
      const endpointID = button.dataset.endpoint ?? ''
      const desired = button.dataset.value === 'true'

      button.click()
      await waitFor(async () => {
        const status = await adapter.getStatus()
        const endpoint = status.gateway.clusters
          .find((cluster) => cluster.id === clusterID)
          ?.endpoints.find((candidate) => candidate.id === endpointID)
        return endpoint?.enabled === desired ? endpoint : null
      }, 'endpoint mutation')
      await waitFor(() => {
        const current = Array.from(appRoot.querySelectorAll<HTMLButtonElement>('[data-action="toggle-endpoint"]')).find(
          (candidate) => candidate.dataset.cluster === clusterID && candidate.dataset.endpoint === endpointID,
        )
        return current?.dataset.value === String(!desired) ? current : null
      }, 'endpoint control refresh')

      const current = Array.from(appRoot.querySelectorAll<HTMLButtonElement>('[data-action="toggle-endpoint"]')).find(
        (candidate) => candidate.dataset.cluster === clusterID && candidate.dataset.endpoint === endpointID,
      )
      assert(current === button, 'endpoint button was replaced while its action completed')
    })

    await check('Stop traffic freezes request statistics', async () => {
      await navigate('request')
      const toggle = required<HTMLButtonElement>(appRoot, '[data-action="toggle-traffic"]')
      assert(toggle.textContent?.includes('Stop traffic'), 'traffic was not running before the stop check')
      toggle.click()

      await waitFor(
        () => required<HTMLButtonElement>(appRoot, '[data-action="toggle-traffic"]').textContent?.includes('Start traffic'),
        'traffic stop control',
      )
      await waitFor(() => adapter.requestsCompleted === adapter.requestsStarted, 'in-flight request drain')
      const settled = await stableCounter(() => visibleSent(appRoot))
      const started = adapter.requestsStarted
      await delay(260)
      await nextFrame()

      assert(visibleSent(appRoot) === settled, 'visible sent counter advanced after Stop traffic')
      assert(adapter.requestsStarted === started, 'adapter received another request after Stop traffic')
    })
  } finally {
    await stopTrafficIfNeeded()
    const total = passed + failed
    summary.textContent = `Results: ${passed}/${total} passed`
    statusLine.textContent = failed === 0 ? 'PASS — all rendering regressions are covered.' : 'FAIL — inspect the failed checks below.'
    document.body.dataset.testStatus = failed === 0 ? 'passed' : 'failed'
    runButton.textContent = failed === 0 ? 'Rendering tests passed' : 'Rendering tests failed'
  }
}

runButton.addEventListener('click', () => void run())
