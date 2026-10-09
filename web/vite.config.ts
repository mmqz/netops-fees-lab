import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// NetOps Lab SPA build config.
// - dev server proxies /api to the Go backend (T7) on localhost:8080
// - production build is embedded into the Go binary via internal/webui
//   (go:embed all:dist), so the output directory lives OUTSIDE web/
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
  },
});
