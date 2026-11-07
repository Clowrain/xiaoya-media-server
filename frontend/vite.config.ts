import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  base: './',
  build: {
    outDir: '../web',
    emptyOutDir: false,
  },
  server: {
    port: 5173,
    proxy: {
      '/emby': 'http://localhost:8096',
      '/admin': 'http://localhost:8096',
      '/api': 'http://localhost:8096',
      '/Sessions': 'http://localhost:8096',
      '/Users': 'http://localhost:8096',
      '/Items': 'http://localhost:8096',
      '/videos': 'http://localhost:8096',
      '/files': 'http://localhost:8096',
    }
  }
})
