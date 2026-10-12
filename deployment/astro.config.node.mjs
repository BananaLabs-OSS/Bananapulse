import { defineConfig } from 'astro/config';
import node from '@astrojs/node';

const site = process.env.SITE_URL || 'http://127.0.0.1:4321';
const publicURL = new URL(site);

export default defineConfig({
  site,
  security: { allowedDomains: [{ hostname: publicURL.hostname, protocol: publicURL.protocol.slice(0, -1) }] },
  output: 'server',
  adapter: node({ mode: 'standalone' }),
});
