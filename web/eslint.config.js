// ── ESLint (flat config) ──
//
// Studio shipped 16k lines of React with `"lint": "tsc --noEmit"` and nothing
// else. In particular there was no `eslint-plugin-react-hooks`, which is the
// rule set that would have caught the stale-closure bug in LiveView: an
// `es.onmessage` handler installed from a `useCallback(…, [])` that closed over
// state, so every message recomputed `[...frozenEvents, data]` and the live log
// reset to a single entry.
//
// `react-hooks/exhaustive-deps` is therefore an ERROR, not a warning.

import js from '@eslint/js';
import globals from 'globals';
import tseslint from 'typescript-eslint';
import reactHooks from 'eslint-plugin-react-hooks';
import reactRefresh from 'eslint-plugin-react-refresh';
import jsxA11y from 'eslint-plugin-jsx-a11y';

export default tseslint.config(
  { ignores: ['dist', 'node_modules', 'coverage', '*.config.js', '*.config.ts'] },
  {
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    files: ['**/*.{ts,tsx}'],
    languageOptions: {
      ecmaVersion: 2022,
      globals: { ...globals.browser, ...globals.es2021 },
    },
    plugins: {
      'react-hooks': reactHooks,
      'react-refresh': reactRefresh,
      'jsx-a11y': jsxA11y,
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      ...jsxA11y.flatConfigs.recommended.rules,

      // ── The rules that would have caught the shipped defects ──
      'react-hooks/rules-of-hooks': 'error',
      'react-hooks/exhaustive-deps': 'error',

      // A bare `catch {}` (or `catch (e) {}` with an empty body) is how 37 API
      // failures became invisible. Empty blocks must carry a comment saying
      // why swallowing is correct.
      'no-empty': ['error', { allowEmptyCatch: false }],

      // Native dialogs are unstylable, unlabelled and untestable — use the
      // shared <Modal>/useConfirm primitives instead.
      'no-alert': 'error',
      'no-restricted-globals': [
        'error',
        { name: 'confirm', message: 'Use useConfirm() from components/ui/Modal instead.' },
        { name: 'alert', message: 'Use useToast() from components/ui/Toast instead.' },
      ],

      'react-refresh/only-export-components': ['warn', { allowConstantExport: true }],
      '@typescript-eslint/no-unused-vars': [
        'warn',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_' },
      ],
      '@typescript-eslint/no-explicit-any': 'warn',
      'no-console': ['warn', { allow: ['warn', 'error'] }],
      eqeqeq: ['error', 'smart'],
    },
  },
  {
    // Tests may use any/console freely and render components ad hoc.
    files: ['**/*.test.{ts,tsx}', 'src/test/**/*.{ts,tsx}'],
    languageOptions: { globals: { ...globals.browser, ...globals.node } },
    rules: {
      '@typescript-eslint/no-explicit-any': 'off',
      'no-console': 'off',
      'react-refresh/only-export-components': 'off',
    },
  },
);
