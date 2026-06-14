import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  base: process.env.VITE_BASE_PATH || '/',
  build: {
    outDir: './static',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/v1': 'http://localhost:17040',
      '/healthz': 'http://localhost:17040',
      '/openapi.json': 'http://localhost:17040',
      '/docs': 'http://localhost:17040',
      '/metrics': 'http://localhost:17040',
    },
  },
})
