import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { fileURLToPath, URL } from 'node:url'
import { visualizer } from 'rollup-plugin-visualizer'

const analyze = process.env.ANALYZE === '1'

// https://vite.dev/config/
export default defineConfig({
  plugins: [
    react(),
    tailwindcss(),
    ...(analyze
      ? [
          visualizer({
            filename: process.env.ANALYZE_OUTPUT ?? '/tmp/sa-mp-pilot-bundle.json',
            template: 'raw-data',
            gzipSize: true,
            brotliSize: true,
            projectRoot: fileURLToPath(new URL('.', import.meta.url)),
          }),
        ]
      : []),
  ],
  resolve: { alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) } },
  build: {
    rolldownOptions: {
      output: {
        codeSplitting: {
          groups: [
            {
              name: 'lezer',
              test: /node_modules\/\.pnpm\/@lezer/,
              priority: 30,
            },
            {
              name: 'codemirror',
              test: /node_modules\/\.pnpm\/(?:@codemirror|@uiw)/,
              priority: 20,
            },
          ],
        },
      },
    },
  },
  server: { proxy: { '/api': 'http://127.0.0.1:8080' } },
})
