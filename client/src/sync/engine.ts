import { listMessagesAfter, syncConversations } from '@/api/client';
import { useChatStore } from '@/store/chatStore';
import { useSeqStore } from './seqStore';

let inflight: Promise<void> | null = null;

/**
 * Catch up on any messages we missed while disconnected.
 *
 * Strategy:
 *   1. Ask server for the head seq of every conversation we belong to.
 *   2. For each conversation where head > local lastSeq, pull
 *      messages with afterSeq=local lastSeq (ascending, oldest-first).
 *   3. Append each into the chat store; the store dedupes by id and
 *      bumpLast records the new high-water seq.
 *
 * Called on initial load and after every WS reconnect. Coalesced so
 * concurrent triggers share one network round.
 */
export async function syncAllConversations(): Promise<void> {
  if (inflight) return inflight;
  inflight = (async () => {
    try {
      const convs = await syncConversations();
      const chat = useChatStore.getState();
      const seq = useSeqStore.getState();

      // Mirror the muted-conv set so notify/unread logic can suppress.
      chat.setMutedAll(convs.filter((c) => c.muted).map((c) => c.id));

      const needPull = convs.filter((c) => c.headSeq > (seq.last[c.id] ?? 0));
      if (needPull.length === 0) return;

      await Promise.all(
        needPull.map(async (c) => {
          const localLast = seq.last[c.id] ?? 0;
          // For brand-new conversations (localLast=0) we pull at most 200
          // most-recent; older history can be paged in on demand later.
          const since = localLast > 0 ? localLast : Math.max(0, c.headSeq - 200);
          const msgs = await listMessagesAfter(c.id, since);
          msgs.forEach((m) => chat.appendMessage(m));
        }),
      );
    } catch {
      // network blip; will be retried on next WS reconnect.
    } finally {
      inflight = null;
    }
  })();
  return inflight;
}
