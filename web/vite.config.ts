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
    chunkSizeWarningLimit: 900,
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
          if (id.includes('node_modules/lucide-react/')) return 'icons';
          return undefined;
        },
      },
    },
  },
});
