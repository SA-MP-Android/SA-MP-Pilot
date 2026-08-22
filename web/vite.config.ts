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
              name: 'codemirror',
              test: /node_modules\/\.pnpm\/(?:@codemirror|@lezer|@uiw)/,
              maxSize: 300_000,
              minSize: 0,
              priority: 20,
            },
          ],
        },
      },
    },
  },
  server: { proxy: { '/api': 'http://127.0.0.1:8080' } },
})
