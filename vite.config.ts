import babel from '@rolldown/plugin-babel';
import tailwindcss from '@tailwindcss/vite';
import react, { reactCompilerPreset } from '@vitejs/plugin-react';
import Unfonts from 'unplugin-fonts/vite';
import { defineConfig, mergeConfig } from 'vite';
import { coverageConfigDefaults, defineConfig as defineVitestConfig } from 'vitest/config';

// https://vite.dev/config/
const host = process.env.TAURI_DEV_HOST;

const viteConfig = defineConfig({
  resolve: {
    tsconfigPaths: true,
  },
  plugins: [
    react(),
    babel({
      presets: [reactCompilerPreset()],
    }),
    tailwindcss(),
    Unfonts({
      fontsource: {
        families: ['Roboto Flex Variable'],
      },
    }),
  ],

  // Vite options tailored for Tauri development and only applied in `tauri dev` or `tauri build`
  //
  // 1. prevent Vite from obscuring rust errors
  clearScreen: false,
  // 2. tauri expects a fixed port, fail if that port is not available
  server: {
    port: 1420,
    strictPort: true,
    host: host || '0.0.0.0',
    hmr: host
      ? {
          protocol: 'ws',
          host,
          port: 1421,
        }
      : undefined,
    watch: {
      // 3. tell Vite to ignore watching `src-tauri`
      ignored: ['**/src-tauri/**'],
    },
  },
});

const vitestConfig = defineVitestConfig({
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./vitest.setup.ts'],
    // CI uploads lcov.info to Codecov, which also ignores what this excludes.
    coverage: {
      provider: 'v8',
      reporter: ['text', 'lcovonly'],
      include: ['src/**/*.{ts,tsx}'],
      // Generated output, the router's wiring, the entry point, and the test
      // harness itself: a test would assert nothing the suite doesn't already.
      exclude: [
        ...coverageConfigDefaults.exclude,
        'src/gql/**',
        'src/routeTree.ts',
        'src/main.tsx',
        'src/test-utils.tsx',
      ],
    },
  },
});

export default mergeConfig(viteConfig, vitestConfig);
