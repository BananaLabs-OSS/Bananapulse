import { describe, it, expect, vi, beforeEach } from 'vitest';

// AUDIT REGRESSION (C1/C2 archive guard + cascade): archiving a component must
//  - GUARD: refuse (ArchiveBlockedError) if ANY node in the subtree has a live
//    declared outage (open incident / quorum-declared) — archiving must never
//    silently paint a live outage green by dropping the node out of the tree.
//  - CASCADE: when clear, archive the WHOLE descendant subtree so a live child
//    can't be orphaned under a vanished parent.
//
// We mock the component-tree helpers + @/db so we drive subtree shape + liveness
// deterministically and observe exactly which ids get archived.

const { descendantIds, firstLiveComponent, archivedSets, updateWhere } = vi.hoisted(() => ({
  descendantIds: vi.fn<(id: string) => Promise<string[]>>(),
  firstLiveComponent: vi.fn<(ids: string[]) => Promise<string | null>>(),
  archivedSets: [] as any[],
  updateWhere: vi.fn(async (..._args: any[]) => undefined),
}));

vi.mock('./components', () => ({
  descendantIds,
  firstLiveComponent,
  descendantIdsIncludingArchived: vi.fn(async () => []),
}));

// Capture db.update(...).set(...).where(...) — record the archived id set.
vi.mock('@/db', () => ({
  db: {
    update: () => ({
      set: (patch: any) => {
        archivedSets.push(patch);
        return { where: updateWhere };
      },
    }),
    select: () => ({ from: () => ({ where: async () => [] }) }),
  },
}));
vi.mock('@/db/schema', () => ({ components: { id: 'id', archivedAt: 'archived_at' } }));
// drizzle inArray is used to scope the cascade update; stub returns a sentinel.
vi.mock('drizzle-orm', async (orig) => {
  const actual = (await orig()) as any;
  return { ...actual, inArray: (_col: any, ids: string[]) => ({ __inArray: ids }) };
});

import { setComponentArchived, ArchiveBlockedError } from './db-components';

beforeEach(() => {
  vi.clearAllMocks();
  archivedSets.length = 0;
});

describe('setComponentArchived — archive guard + cascade (audit C1/C2 regression)', () => {
  it('REGRESSION: refuses to archive when a subtree node has a live outage', async () => {
    descendantIds.mockResolvedValue(['product', 'svc-a', 'svc-b']);
    firstLiveComponent.mockResolvedValue('svc-b'); // svc-b is live-declared

    await expect(setComponentArchived('product', true)).rejects.toBeInstanceOf(ArchiveBlockedError);
    // Guard fires BEFORE any write.
    expect(updateWhere).not.toHaveBeenCalled();
    expect(archivedSets).toHaveLength(0);
  });

  it('REGRESSION: cascades the archive across the WHOLE subtree when clear', async () => {
    const subtree = ['product', 'svc-a', 'svc-b', 'host-1'];
    descendantIds.mockResolvedValue(subtree);
    firstLiveComponent.mockResolvedValue(null); // nothing live

    await setComponentArchived('product', true);

    // Exactly one archive update, scoping inArray to the full subtree.
    expect(updateWhere).toHaveBeenCalledTimes(1);
    expect(archivedSets).toHaveLength(1);
    expect(archivedSets[0].archivedAt).toBeInstanceOf(Date);
    const whereArg = updateWhere.mock.calls[0][0];
    expect(whereArg.__inArray).toEqual(subtree);
  });

  it('checks liveness over the full descendant set, not just the root id', async () => {
    descendantIds.mockResolvedValue(['root', 'child', 'grandchild']);
    firstLiveComponent.mockResolvedValue(null);
    await setComponentArchived('root', true);
    expect(firstLiveComponent).toHaveBeenCalledWith(['root', 'child', 'grandchild']);
  });

  it('ArchiveBlockedError names the blocking component id', () => {
    const e = new ArchiveBlockedError('svc-x');
    expect(e.componentId).toBe('svc-x');
    expect(e.message).toContain('svc-x');
  });
});
