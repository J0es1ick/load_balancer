import type {
  ConsoleEvent,
  GatewayConfig,
  GatewayStatus,
  HTTPMethod,
  RequestResult,
  SectionID,
  ValidationResult,
} from './types.js'

export interface RequestDraft {
  method: HTTPMethod
  host: string
  path: string
  headersText: string
  body: string
}

export interface TrafficState {
  running: boolean
  rps: number
  sent: number
  ok: number
  errors: number
  lastBackend: string
  distribution: Record<string, number>
}

export interface RateLimitDraft {
  enabled: boolean
  capacity: number
  refillPerSecond: number
  failureMode: 'fail-open' | 'fail-closed' | 'local-fallback'
}

export interface ConsoleState {
  section: SectionID
  loading: boolean
  busy: boolean
  connected: boolean
  error: string
  status: GatewayStatus | null
  config: GatewayConfig | null
  editorText: string
  editorDirty: boolean
  validation: ValidationResult | null
  request: RequestDraft
  response: RequestResult | null
  traffic: TrafficState
  rateLimitDraft: RateLimitDraft
  rateLimitDirty: boolean
  events: ConsoleEvent[]
  activeEndpoint: string
  persistence: boolean
}

export function initialState(section: SectionID, persistence: boolean): ConsoleState {
  return {
    section,
    loading: true,
    busy: false,
    connected: false,
    error: '',
    status: null,
    config: null,
    editorText: '',
    editorDirty: false,
    validation: null,
    request: {
      method: 'GET',
      host: 'localhost',
      path: '/api/',
      headersText: '{\n  "Accept": "application/json"\n}',
      body: '',
    },
    response: null,
    traffic: { running: false, rps: 4, sent: 0, ok: 0, errors: 0, lastBackend: '—', distribution: {} },
    rateLimitDraft: { enabled: true, capacity: 100, refillPerSecond: 1, failureMode: 'local-fallback' },
    rateLimitDirty: false,
    events: [],
    activeEndpoint: '',
    persistence,
  }
}
