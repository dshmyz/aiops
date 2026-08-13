import { defineConfig } from 'vitest/config';
import { loadEnv } from 'vite';
import { resolve } from 'node:path';
import vue from '@vitejs/plugin-vue';

export default defineConfig(({ mode }) => {
  // Load .env / .env.local so VITE_DEV_ADMIN_TOKEN is available at config time.
  // 仓库根 .env 优先（app 目录下的 token 可能过期）；app 目录作为回退。
  const env = loadEnv(mode, process.cwd(), '');
  const rootEnv = loadEnv(mode, resolve(process.cwd(), '../..'), '');
  const devAdminToken = rootEnv.VITE_DEV_ADMIN_TOKEN || env.VITE_DEV_ADMIN_TOKEN || process.env.VITE_DEV_ADMIN_TOKEN || 'dev-admin-token-fallback';

  return {
    plugins: [vue()],
    server: {
      proxy: {
        '/v1': {
          target: 'http://127.0.0.1:18080',
          changeOrigin: true,
          headers: {
            Authorization: `Bearer ${devAdminToken}`,
            'X-Request-ID': 'capability-console-dev',
          },
        },
      },
    },
    build: {
      chunkSizeWarningLimit: 600,
      rollupOptions: {
        output: {
          manualChunks: {
            'vendor-vue': ['vue'],
            'vendor-marked': ['marked', 'dompurify'],
          },
        },
        onwarn(warning, warn) {
          if (warning.message.includes('contains an annotation that Rollup cannot interpret')) {
            return;
          }
          warn(warning);
        },
      },
    },
    test: {
      environment: 'jsdom',
      setupFiles: './src/test/setup.ts',
    },
  };
});
