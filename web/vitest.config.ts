import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import path from 'path';

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: { '@': path.resolve(import.meta.dirname, './src') },
  },
  test: {
    environment: 'jsdom',
    // Bound jsdom memory/CPU on developer machines also serving local models.
    // Unbounded file workers can time out user-event tests and leak late events.
    maxWorkers: 4,
    // These assert functional interactions, not five-second latency. Longer
    // form edits need scheduling headroom while local inference is running.
    testTimeout: 15_000,
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
    coverage: {
      provider: 'v8',
      reporter: ['text', 'lcov'],
      include: ['src/**/*.{ts,tsx}'],
      exclude: ['src/test/**', 'src/**/*.test.{ts,tsx}', 'src/types/**'],
    },
  },
});
