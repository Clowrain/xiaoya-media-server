import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { execSync } from 'child_process'

let gitCommit = 'dev'
try {
  gitCommit = execSync('git rev-parse --short HEAD').toString().trim()
} catch {}

const buildTime = new Date().toISOString().replace('T', ' ').slice(0, 19)

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  base: './',
  define: {
    'import.meta.env.VITE_APP_VERSION': JSON.stringify('v2.1.0'),
    'import.meta.env.VITE_BUILD_COMMIT': JSON.stringify(gitCommit),
    'import.meta.env.VITE_BUILD_TIME': JSON.stringify(buildTime),
  },
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
