import { loadEnvFile } from 'node:process'
import { fileURLToPath } from 'node:url'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

loadEnvFile(fileURLToPath(new URL('../env/frontend.env', import.meta.url)))

const backendUrl = process.env.BACKEND_URL
if (!backendUrl?.trim())
  throw new Error('BACKEND_URL must be set in env/frontend.env.')

for (const key of [
  'VITE_ZITADEL_ISSUER',
  'VITE_ZITADEL_CLIENT_ID',
  'VITE_ZITADEL_PROJECT_ID',
]) {
  if (!process.env[key]?.trim())
    throw new Error(`${key} must be set in env/frontend.env.`)
}

export default defineConfig({
  plugins: [react()],
  envDir: false,
  server: {
    host: '127.0.0.1',
    proxy: {
      '/rpc': backendUrl,
      '/agent/': backendUrl,
    },
  },
})
