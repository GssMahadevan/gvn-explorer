import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// Dev: vite serves the GUI and proxies API calls to the Go backend.
// Build: static dist/ served by the Go binary itself:
//   ./server -web frontend/dist   (or by nginx)
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: { '/api': 'http://localhost:8095' },
  },
});
