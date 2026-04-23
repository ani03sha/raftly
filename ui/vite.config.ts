import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// In dev: `npm run dev` serves the UI on :5173 and proxies API calls to node1 (:8001).
// In prod: `npm run build` emits static files that Go embeds under web/dist/.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    sourcemap: false,
  },
  server: {
    port: 5173,
    proxy: {
      '/api':    { target: 'http://localhost:8001', changeOrigin: true },
      '/status': { target: 'http://localhost:8001', changeOrigin: true },
      '/keys':   { target: 'http://localhost:8001', changeOrigin: true },
    },
  },
})
