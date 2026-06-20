import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'node:path'

export default defineConfig({
  plugins: [react()],
  base: process.env.VITE_BASE_PATH || './',
  resolve: {
    alias: {
      react: path.resolve(__dirname, 'node_modules/react'),
      'react/jsx-runtime': path.resolve(__dirname, 'node_modules/react/jsx-runtime'),
      'lucide-react': path.resolve(__dirname, 'node_modules/lucide-react')
    },
    dedupe: ['react', 'react-dom']
  },
  build: {
    outDir: './static',
    emptyOutDir: true,
  },
  server: {
    fs: {
      allow: ['.', '../../web-shared'],
    },
    proxy: {
      '/v1': 'http://localhost:17040',
      '/healthz': 'http://localhost:17040',
      '/openapi.json': 'http://localhost:17040',
      '/docs': 'http://localhost:17040',
      '/metrics': 'http://localhost:17040',
    },
  },
})
