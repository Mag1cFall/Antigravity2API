import { fileURLToPath, URL } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

const serviceTarget = 'http://127.0.0.1:8017'

// defineConfig 定义前端构建与本地联调
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  build: {
    outDir: fileURLToPath(new URL('../internal/webui/dist', import.meta.url)),
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/api': serviceTarget,
      '/v1': serviceTarget,
      '/v1beta': serviceTarget,
      '/readyz': serviceTarget,
    },
  },
})
