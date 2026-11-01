import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Vite builds straight into the Go module's embed directory: the backend's
// //go:embed picks up whatever lands in internal/webui/dist with no copy
// step. emptyOutDir keeps stale chunks from a previous build out of the
// embedded binary.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/api': 'http://localhost:8085',
      '/push': 'http://localhost:8085',
    },
  },
})
