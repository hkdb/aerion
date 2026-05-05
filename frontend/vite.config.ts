import { svelte } from "@sveltejs/vite-plugin-svelte";
import path from "path";
import { defineConfig } from "vite";

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [svelte()],
  resolve: {
    alias: {
      $lib: path.resolve("./src/lib"),
      $: path.resolve("./src")
    }
  },
  optimizeDeps: {
    include: [
      "@iconify-json/mdi",
      "@iconify-json/lucide",
      "@iconify-json/heroicons",
      "@iconify-json/logos",
      "@iconify-json/simple-icons"
    ]
  },
  build: {
    target: "esnext",
    minify: "esbuild",
    sourcemap: false,
    rollupOptions: {
      input: {
        main: path.resolve(__dirname, "index.html"),
        composer: path.resolve(__dirname, "composer.html")
      }
    }
  },
  server: {
    strictPort: true
  }
});
