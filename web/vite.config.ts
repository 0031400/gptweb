import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://127.0.0.1:8080',
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    rolldownOptions: {
      output: {
        // 静态 vendor 分包：不改 import，也不做懒加载；浏览器仍会加载这些 chunk，
        // 但可以避免单文件过大，并让依赖缓存更稳定。
        codeSplitting: {
          groups: [
            {
              name: 'charts',
              test: /node_modules[\\/](?:recharts|victory-vendor|d3-)/,
              priority: 30,
            },
            {
              name: 'markdown',
              test: /node_modules[\\/](?:react-markdown|remark-|rehype-|katex|unified|mdast|hast|micromark|unist|vfile)/,
              priority: 20,
            },
            {
              name: 'react',
              test: /node_modules[\\/](?:react|react-dom|react-is|scheduler|use-sync-external-store)/,
              priority: 10,
            },
            {
              name: 'vendor',
              test: /node_modules[\\/]/,
              priority: 1,
            },
          ],
        },
      },
    },
  },
})
