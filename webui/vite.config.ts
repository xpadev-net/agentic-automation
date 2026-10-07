import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The build output is written into the Go binary's embed dir
// (internal/webui/static) so the Operator serves the SPA directly.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "../internal/webui/static",
    emptyOutDir: true,
  },
});
