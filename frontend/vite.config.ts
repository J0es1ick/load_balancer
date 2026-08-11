import { defineConfig, loadEnv } from 'vite'

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, '.', '')

  return {
    base: env.VITE_BASE_PATH || '/',
    build: {
      // CI and Docker build from a clean checkout; keeping the directory avoids
      // Windows file-lock failures when a local preview still has an asset open.
      emptyOutDir: false,
    },
    server: {
      proxy: {
        '/api': {
          target: env.VITE_API_PROXY_TARGET || 'http://127.0.0.1:9090',
          changeOrigin: true,
          headers: env.VITE_MANAGEMENT_TOKEN
            ? { Authorization: `Bearer ${env.VITE_MANAGEMENT_TOKEN}` }
            : undefined,
        },
      },
    },
  }
})
