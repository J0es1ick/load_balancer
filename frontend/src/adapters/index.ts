import type { AppMode } from '../types.js'
import { DemoAdapter } from './demo.js'
import type { GatewayAdapter } from './gateway.js'
import { LiveAdapter } from './live.js'

export function createAdapter(mode: AppMode): GatewayAdapter {
  return mode === 'live' ? new LiveAdapter() : new DemoAdapter()
}
