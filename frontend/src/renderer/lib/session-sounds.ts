import type { InfiniteData, QueryClient } from "@tanstack/react-query";
import attentionSoundUrl from "../assets/notification.mp3";
import mergedSoundUrl from "../assets/merged.mp3";
import receivedSoundUrl from "../assets/received.mp3";
import statusChangeSoundUrl from "../assets/status-change.mp3";
import { conversationQueryRoot } from "../hooks/useConversation";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { useSoundNotificationsStore } from "../stores/sound-notifications-store";
import type { ConversationSnapshot } from "../types/conversation";
import type { SessionStatus, WorkspaceSummary } from "../types/workspace";
import { aoBridge } from "./bridge";
import { playNotificationSound } from "./notification-sound-player";

/**
 * Two tiers: "attention" means the user is needed; the rest are quiet FYI
 * sounds that stay easy to tune out (the ambient tier, gated by its own setting).
 */
export type SessionSound = "attention" | "merged" | "received" | "status-change";

const PRIORITY: Record<SessionSound, number> = { attention: 3, merged: 2, received: 1, "status-change": 0 };
const URLS: Record<SessionSound, string> = {
	attention: attentionSoundUrl,
	merged: mergedSoundUrl,
	received: receivedSoundUrl,
	"status-change": statusChangeSoundUrl,
};
// Several sources can fire for one real event (a turn ends: status flips and the
// final message lands). Collect for a moment and play only the loudest.
const COALESCE_MS = 200;

let pendingSound: SessionSound | undefined;
let flushTimer: ReturnType<typeof setTimeout> | undefined;

/** Queue a sound; bursts inside the coalesce window play once, at the highest tier. */
export function requestSessionSound(sound: SessionSound): void {
	const { enabled, ambientEnabled } = useSoundNotificationsStore.getState();
	if (!enabled || (sound !== "attention" && !ambientEnabled)) return;
	if (pendingSound === undefined || PRIORITY[sound] > PRIORITY[pendingSound]) pendingSound = sound;
	flushTimer ??= setTimeout(() => {
		const next = pendingSound as SessionSound;
		pendingSound = undefined;
		flushTimer = undefined;
		// Only a missed attention sound is worth the system beep fallback.
		playNotificationSound(next === "attention" ? () => aoBridge.notifications.reportSoundFailure() : () => {}, URLS[next]);
	}, COALESCE_MS);
}

// Statuses main already sounds for via the needs_input / ready_to_merge notifications.
const NOTIFIED_STATUSES: ReadonlySet<SessionStatus> = new Set<SessionStatus>(["needs_input", "mergeable"]);
const ATTENTION_STATUSES: ReadonlySet<SessionStatus> = new Set<SessionStatus>(["ci_failed", "changes_requested", "approved"]);
// Leaving "working" for one of these is the agent going away, not finishing a turn.
const NOT_FINISHED: ReadonlySet<SessionStatus> = new Set<SessionStatus>(["exited", "terminated", "no_signal", "unknown"]);

/** The sound for one session's display-status transition, if any. */
export function soundForTransition(prev: SessionStatus, next: SessionStatus): SessionSound | undefined {
	if (prev === next || prev === "unknown" || next === "unknown") return undefined;
	if (NOTIFIED_STATUSES.has(next)) return undefined;
	if (next === "merged") return "merged";
	if (ATTENTION_STATUSES.has(next)) return "attention";
	if (prev === "working" && !NOT_FINISHED.has(next)) return "attention";
	return "status-change";
}

function isWatching(sessionId: string, watchedSessionId: string | undefined): boolean {
	return sessionId === watchedSessionId && document.visibilityState === "visible" && document.hasFocus();
}

/**
 * Plays session sounds from the query cache: display-status transitions in the
 * workspace list, and completed agent messages in any loaded Chat conversation.
 * The first snapshot of each only seeds state, so startup and reconnects are silent.
 * An attention transition on the session the user is looking at plays as a tick.
 */
export function startSessionSounds(queryClient: QueryClient, getWatchedSessionId: () => string | undefined): () => void {
	let statuses: Map<string, SessionStatus> | undefined;
	const seenMessages = new Map<string, Set<string>>();

	const onWorkspaces = (workspaces: WorkspaceSummary[] | undefined) => {
		if (!workspaces) return;
		const next = new Map<string, SessionStatus>();
		for (const workspace of workspaces) for (const session of workspace.sessions) next.set(session.id, session.status);
		if (statuses) {
			const watched = getWatchedSessionId();
			for (const [id, status] of next) {
				const prev = statuses.get(id);
				const sound = prev === undefined ? undefined : soundForTransition(prev, status);
				if (sound) requestSessionSound(sound === "attention" && isWatching(id, watched) ? "status-change" : sound);
			}
		}
		statuses = next;
	};

	const onConversation = (queryHash: string, data: InfiniteData<ConversationSnapshot> | undefined) => {
		if (!data) return;
		const seen = seenMessages.get(queryHash);
		let received = false;
		const ids = seen ?? new Set<string>();
		for (const page of data.pages ?? []) {
			for (const item of page.items ?? []) {
				if (item.kind !== "message" || item.role !== "assistant" || item.streaming || ids.has(item.id)) continue;
				ids.add(item.id);
				received = true;
			}
		}
		if (!seen) seenMessages.set(queryHash, ids);
		else if (received) requestSessionSound("received");
	};

	return queryClient.getQueryCache().subscribe((event) => {
		if (event.type !== "updated" || event.action.type !== "success") return;
		const { queryKey, queryHash, state } = event.query;
		if (queryKey.length === 1 && queryKey[0] === workspaceQueryKey[0]) {
			onWorkspaces(state.data as WorkspaceSummary[] | undefined);
		} else if (queryKey[0] === conversationQueryRoot[0]) {
			onConversation(queryHash, state.data as InfiniteData<ConversationSnapshot> | undefined);
		}
	});
}
