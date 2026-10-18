import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// In development the Go backend runs on :8080 (see `make dev`) and Vite
// proxies /api to it. changeOrigin stays false so the Host/Origin headers
// match and the backend's CSRF origin check passes.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    host: true,
    allowedHosts: true,
    proxy: {
      '/api': { target: 'http://127.0.0.1:8080', changeOrigin: false },
    },
  },
  build: { outDir: 'dist', emptyOutDir: true, chunkSizeWarningLimit: 900 },
})
