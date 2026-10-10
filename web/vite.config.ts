import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// NetOps Lab SPA build config.
// - dev server proxies /api to the Go backend (T7) on localhost:8080
// - preview server (vite preview, used by FX browser sanity checks) applies
//   the same proxy so the built bundle can talk to a scratch backend
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
  preview: {
    port: 8096,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8098',
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
    rollupOptions: {
      output: {
        // Vendor chunking by dependency family (W5-D, v0.4.0). The app used
        // to ship ONE ~795 kB index.js (vite ">500 kB after minification"
        // warning). Split by family so:
        //   - vendor-react: react + react-dom + react-router-dom (+ their
        //     internal deps scheduler / @remix-run/router) — loaded once
        //     with the app shell;
        //   - vendor-cytoscape: the topology canvas engine — statically
        //     imported by the /lab bundle but heavy enough to own a chunk;
        //   - vendor-maplibre: the geo 2D/3D engine — ALREADY lazily
        //     imported (ADR-0009, await import('maplibre-gl')), and the
        //     manualChunks assignment keeps that chunk async; its ~1.08 MB
        //     body is a single third-party library that cannot be split
        //     further (the remaining >500 kB warning is that vendor's
        //     intrinsic size, reported honestly);
        //   - everything else (app code, styles) stays in the entry chunks.
        // maplibre's web worker ships as a ?url asset
        // (maplibre-gl-worker-*.mjs), not as a JS chunk.
        manualChunks(id: string) {
          if (!id.includes('node_modules')) return undefined;
          if (id.includes('react') || id.includes('scheduler') || id.includes('@remix-run')) {
            return 'vendor-react';
          }
          if (id.includes('cytoscape')) {
            return 'vendor-cytoscape';
          }
          if (id.includes('maplibre-gl')) {
            return 'vendor-maplibre';
          }
          return undefined;
        },
      },
    },
  },
});
