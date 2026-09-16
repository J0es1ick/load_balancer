import { defineConfig, loadEnv } from 'vite'
import { readFileSync } from 'node:fs'

function managementToken(env: Record<string, string>): string {
  const secretFile = env.BALANCER_ADMIN_TOKEN_FILE
  if (secretFile) return readFileSync(secretFile, 'utf8').trim()
  return env.BALANCER_ADMIN_TOKEN?.trim() ?? ''
}

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, '.', '')
  const appMode = env.VITE_APP_MODE?.trim() || mode
  if (appMode !== 'demo' && appMode !== 'live') {
    throw new Error(`VITE_APP_MODE must be "demo" or "live"; received ${JSON.stringify(appMode)}`)
  }

  return {
    base: env.VITE_BASE_PATH || '/',
    plugins: [
      {
        name: 'proxy-console-mode-marker',
        transformIndexHtml(html) {
          return html.replace('<meta charset="UTF-8" />', `<meta charset="UTF-8" />\n    <meta name="proxy-console-mode" content="${appMode}" />`)
        },
      },
    ],
    server: {
      proxy: {
        '/api': {
          target: env.VITE_API_PROXY_TARGET || 'http://127.0.0.1:9090',
          changeOrigin: true,
          configure(proxy) {
            proxy.on('proxyReq', (proxyRequest) => {
              const token = managementToken(env)
              if (token) proxyRequest.setHeader('Authorization', `Bearer ${token}`)
            })
          },
        },
      },
    },
  }
})
