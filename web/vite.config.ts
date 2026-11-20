/// <reference types="vitest/config" />
import { fileURLToPath, URL } from 'node:url'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'

// The API server for `npm run dev`. Run it with BASE_URL set to this dev
// server's address so that its Origin check, cookies and email links match.
const apiTarget = process.env.API_TARGET ?? 'http://127.0.0.1:8080'

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  server: {
    port: 5173,
    strictPort: true,
    proxy: { '/api': { target: apiTarget } },
  },
  build: {
    // Embedded into the Go binary by internal/webui.
    outDir: '../internal/webui/dist/app',
    emptyOutDir: true,
    // WeChat's in-app browser on older phones; iOS uses the system WebKit.
    target: ['es2020', 'chrome87', 'safari14'],
  },
  test: {
    environment: 'happy-dom',
    include: ['tests/**/*.test.ts'],
  },
})
