import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

const backend = process.env.DONGLED_PANEL_ADDR
  ? `http://${process.env.DONGLED_PANEL_ADDR}`
  : 'http://127.0.0.1:8788'

export default defineConfig({
  plugins: [react()],
  server: {
    host: '127.0.0.1',
    port: 5273,
    strictPort: true,
    proxy: {
      '/api': { target: backend, changeOrigin: false },
      '/r': { target: backend, changeOrigin: false },
    },
  },
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: false,
    assetsDir: 'assets',
    sourcemap: false,
    target: 'es2022',
  },
})
