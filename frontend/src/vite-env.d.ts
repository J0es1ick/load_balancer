/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_APP_MODE: 'demo' | 'live'
  readonly VITE_BASE_PATH?: string
  readonly VITE_API_PROXY_TARGET?: string
  readonly VITE_PUBLIC_URL?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
