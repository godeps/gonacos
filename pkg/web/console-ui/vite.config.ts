/// <reference types="vitest/config" />
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import path from 'path';

const isTest = !!process.env.VITEST;

export default defineConfig(({ command }) => ({
  // Absolute base path so asset URLs in index.html resolve correctly
  // regardless of whether the page is accessed with a trailing slash.
  // With base:'./', visiting /v3/console/ui (no slash) makes the browser
  // resolve ./js/main.js as /v3/console/js/main.js (404). Absolute base
  // sidesteps the trailing-slash ambiguity entirely.
  base: command === 'build' ? '/v3/console/ui/' : '/',
  plugins: [react(), tailwindcss()],
  test: {
    globals: true,
    environment: 'node',
  },
  define: isTest
    ? {}
    : {
        // Polyfill Node.js `process` global for browser-incompatible libs (e.g. swagger2openapi)
        'process.env': {},
        'process.version': JSON.stringify(''),
      },
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  // gonacos is same-origin — no dev server proxy needed.
  // Frontend axios baseURL is '/' and requests go directly to the gonacos server.
  server: {
    port: 8000,
  },
  build: {
    outDir: 'dist',
    rollupOptions: {
      output: {
        entryFileNames: 'js/main.js',
        chunkFileNames: 'js/[name].js',
        assetFileNames: (info) => {
          if (info.name?.endsWith('.css')) return 'css/main.css';
          return 'assets/[name]-[hash][extname]';
        },
        manualChunks(id) {
          // Merge all lucide-react icons into one chunk
          if (id.includes('lucide-react')) {
            return 'vendor-icons';
          }
          // Merge Monaco Editor core + languages into one chunk
          if (id.includes('monaco-editor')) {
            return 'vendor-monaco';
          }
          // Merge major vendor libs
          if (id.includes('node_modules')) {
            if (id.includes('react-dom')) return 'vendor-react';
            if (id.includes('/react/') || id.includes('react-router') || id.includes('react-i18next') || id.includes('i18next')) return 'vendor-react';
            if (id.includes('@radix-ui') || id.includes('class-variance-authority') || id.includes('clsx') || id.includes('tailwind-merge')) return 'vendor-ui';
            if (id.includes('react-markdown') || id.includes('remark') || id.includes('rehype') || id.includes('unified') || id.includes('mdast') || id.includes('hast') || id.includes('micromark') || id.includes('@uiw/react-md-editor')) return 'vendor-markdown';
          }
        },
      },
    },
  },
}));
