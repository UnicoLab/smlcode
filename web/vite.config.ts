import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'path';

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
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
        manualChunks: {
          react: ['react', 'react-dom', 'react-router-dom'],
          dnd: ['@dnd-kit/core', '@dnd-kit/sortable', '@dnd-kit/utilities'],
          icons: ['lucide-react'],
        },
      },
    },
  },
});
