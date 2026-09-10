import type { ReactNode } from 'react';
import { BoardStoreProvider, useBoardStoreSource, type BoardSourceOptions } from '@/hooks/useBoardStore';

/**
 * A self-contained board store for a subtree that has no App above it — a
 * test, a storybook, a page mounted on its own. App itself calls
 * useBoardStoreSource directly, since it also hands `applyEvent` to the
 * stream; see hooks/useBoardStore.
 */
export default function BoardStoreRoot({ children, ...opts }: BoardSourceOptions & { children: ReactNode }) {
  const { store } = useBoardStoreSource(opts);
  return <BoardStoreProvider value={store}>{children}</BoardStoreProvider>;
}
