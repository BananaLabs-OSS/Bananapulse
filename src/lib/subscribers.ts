import { db } from '@/db';
import { subscribers } from '@/db/schema';
import { eq } from 'drizzle-orm';
import { nanoid } from 'nanoid';

export async function addSubscriber(email: string) {
  const normalized = email.toLowerCase().trim();
  const existing = await db.select().from(subscribers).where(eq(subscribers.email, normalized));
  if (existing.length > 0) return existing[0];
  const row = { id: nanoid(), email: normalized };
  await db.insert(subscribers).values(row);
  return row;
}

export async function listSubscribers() {
  return db.select().from(subscribers).orderBy(subscribers.createdAt);
}
