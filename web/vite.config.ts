import { resolve } from 'node:path'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  base: '/',
  css: {
    preprocessorOptions: {
      scss: { loadPaths: [resolve(__dirname, 'node_modules')] }
    }
  },
  build: {
    outDir: '../static',
    emptyOutDir: true,
    rollupOptions: {
      input: {
        login: resolve(__dirname, 'login.html'),
        'change-password': resolve(__dirname, 'change-password.html'),
        workspace: resolve(__dirname, 'workspace.html'),
		semester: resolve(__dirname, 'semester.html'),
		dorms: resolve(__dirname, 'dorms.html'),
		'daily-management': resolve(__dirname, 'daily-management.html'),
        students: resolve(__dirname, 'students.html'),
        deductions: resolve(__dirname, 'deductions.html'),
        'multi-deductions': resolve(__dirname, 'multi-deductions.html')
      }
    }
  }
})
