import { defineConfig } from 'vitest/config';
import { loadEnv } from 'vite';
import vue from '@vitejs/plugin-vue';

export default defineConfig(({ mode }) => {
  // Load .env / .env.local so VITE_DEV_ADMIN_TOKEN is available at config time.
  const env = loadEnv(mode, process.cwd(), '');
  const devAdminToken = env.VITE_DEV_ADMIN_TOKEN || process.env.VITE_DEV_ADMIN_TOKEN || 'dev-admin-token-fallback';

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
