// @ts-check
import { defineConfig } from 'astro/config';

// Project page: served under /crossmemcli/. Links are written with
// BASE_URL so the same build works on a custom domain.
export default defineConfig({
  site: 'https://muthuishere.github.io',
  base: '/crossmemcli',
  trailingSlash: 'ignore',
  build: { format: 'file' },
});
