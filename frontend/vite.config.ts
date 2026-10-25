import { loadEnvFile } from 'node:process'
import { fileURLToPath } from 'node:url'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

loadEnvFile(fileURLToPath(new URL('../env/frontend.env', import.meta.url)))

if (!process.env.VITE_USER_ID?.trim() || !process.env.BACKEND_URL) {
  throw new Error(
    'VITE_USER_ID and BACKEND_URL must be set in env/frontend.env.',
  )
}

export default defineConfig({
  plugins: [react()],
  define: {
    'import.meta.env.VITE_USER_ID': JSON.stringify(process.env.VITE_USER_ID),
  },
  server: {
    host: '127.0.0.1',
    proxy: {
      '/rpc': process.env.BACKEND_URL,
      '/agent/': process.env.BACKEND_URL,
    },
  },
})
