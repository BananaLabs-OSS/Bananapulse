import { defineConfig } from 'drizzle-kit';

const url = process.env.DATABASE_URL ?? '';
const isLocalhost = url.includes('localhost') || url.includes('127.0.0.1');

export default defineConfig({
  schema: './src/db/schema.ts',
  out: './drizzle',
  dialect: 'postgresql',
  dbCredentials: {
    url,
    ssl: isLocalhost ? false : 'require',
  },
});
