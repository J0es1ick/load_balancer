import './styles/base.css'
import './styles/layout.css'
import './styles/components.css'
import './styles/responsive.css'
import './styles/guide.css'

import { createAdapter } from './adapters/index.js'
import { ProxyConsole } from './app.js'
import type { AppMode } from './types.js'

function applicationMode(): AppMode {
  const configured = import.meta.env.VITE_APP_MODE?.trim()
  const candidate = configured || import.meta.env.MODE
  if (candidate !== 'demo' && candidate !== 'live') {
    throw new Error(`VITE_APP_MODE must be "demo" or "live"; received ${JSON.stringify(candidate)}`)
  }
  return candidate
}

const mode = applicationMode()
const root = document.querySelector<HTMLElement>('#app')

if (!root) throw new Error('#app root was not found')

const consoleApp = new ProxyConsole(root, createAdapter(mode), mode)
void consoleApp.start()
