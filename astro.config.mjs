import { defineConfig } from 'astro/config';
import netlify from '@astrojs/netlify';

export default defineConfig({
  // Set SITE_URL in your deploy environment; used for absolute URLs in feeds.
  site: process.env.SITE_URL || 'https://status.example.com',
  output: 'server',
  adapter: netlify(),
});
