import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'path';

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(import.meta.dirname, './src'),
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:7420',
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    // The dist/ tree is embedded into the Go binary via go:embed. `true` shipped
    // full sourcemaps (and the entire TSX source) inside every release binary.
    // 'hidden' still generates maps for local debugging but emits no
    // sourceMappingURL comment, so browsers never request them.
    sourcemap: 'hidden',
    // three + fiber + drei is one ~950 kB chunk (250 kB gzipped), loaded lazily
    // by the Live view's floor only where WebGL exists. It is the one chunk
    // allowed past the old limit, and nothing else should approach it.
    chunkSizeWarningLimit: 1000,
    rollupOptions: {
      output: {
        // Split the vendor bundle so a Studio code change does not invalidate
        // React/router/dnd for every user on every release.
        //
        // A FUNCTION, not the object form: Vite 8 builds on rolldown, which
        // accepts only the callback signature and fails the build outright on
        // the object one ("manualChunks is not a function"). Same three chunks,
        // matched on the module id instead of declared as a map.
        manualChunks(id: string) {
          if (!id.includes('node_modules')) return undefined;
          if (/[\\/]node_modules[\\/](react|react-dom|react-router|react-router-dom)[\\/]/.test(id)) {
            return 'react';
          }
          if (id.includes('node_modules/@dnd-kit/')) return 'dnd';
          // The 3D team floor. One chunk, loaded lazily by the Live view only
          // when WebGL is available — three alone is larger than the rest of
          // Studio, and a page that never shows the floor must not pay for it.
          if (/[\\/]node_modules[\\/](three|@react-three|three-stdlib|troika-three-text|troika-three-utils|troika-worker-utils|maath|zustand|suspend-react|its-fine|react-reconciler|@monogrid|camera-controls|stats-gl|stats\.js|detect-gpu|hls\.js|meshline|three-mesh-bvh|webgl-sdf-generator|bidi-js|utility-types|glsl-noise|tunnel-rat|@use-gesture)[\\/]/.test(id)) {
            return 'three';
          }
          if (id.includes('node_modules/lucide-react/')) return 'icons';
          return undefined;
        },
      },
    },
  },
});
