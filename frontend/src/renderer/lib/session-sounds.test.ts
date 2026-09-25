import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../assets/notification.mp3", () => ({ default: "attention.mp3" }));
vi.mock("../assets/merged.mp3", () => ({ default: "merged.mp3" }));
vi.mock("../assets/received.mp3", () => ({ default: "received.mp3" }));
vi.mock("../assets/status-change.mp3", () => ({ default: "status-change.mp3" }));
vi.mock("./bridge", () => ({ aoBridge: { notifications: { reportSoundFailure: vi.fn() } } }));
const play = vi.fn();
vi.mock("./notification-sound-player", () => ({ playNotificationSound: (_onFailure: () => void, src: string) => play(src) }));

import { QueryClient } from "@tanstack/react-query";
import { useSoundNotificationsStore } from "../stores/sound-notifications-store";
import { requestSessionSound, soundForTransition, startSessionSounds } from "./session-sounds";

describe("soundForTransition", () => {
	it.each([
		["working", "idle", "attention"],
		["working", "pr_open", "attention"],
		["pr_open", "ci_failed", "attention"],
		["review_pending", "changes_requested", "attention"],
		["review_pending", "approved", "attention"],
		["mergeable", "merged", "merged"],
		["idle", "working", "status-change"],
		["pr_open", "review_pending", "status-change"],
		["working", "exited", "status-change"],
		// main already sounds these through their notifications
		["working", "needs_input", undefined],
		["approved", "mergeable", undefined],
		["idle", "idle", undefined],
		["unknown", "working", undefined],
	] as const)("%s -> %s = %s", (prev, next, sound) => {
		expect(soundForTransition(prev, next)).toBe(sound);
	});
});

describe("requestSessionSound", () => {
	beforeEach(() => {
		vi.useFakeTimers();
		play.mockReset();
		useSoundNotificationsStore.setState({ enabled: true, ambientEnabled: true });
	});
	afterEach(() => vi.useRealTimers());

	it("plays only the loudest sound of a burst", () => {
		requestSessionSound("status-change");
		requestSessionSound("attention");
		requestSessionSound("received");
		vi.runAllTimers();
		expect(play.mock.calls).toEqual([["attention.mp3"]]);
	});

	it("drops ambient sounds when they are turned off, keeping attention", () => {
		useSoundNotificationsStore.setState({ ambientEnabled: false });
		requestSessionSound("status-change");
		vi.runAllTimers();
		requestSessionSound("attention");
		vi.runAllTimers();
		expect(play.mock.calls).toEqual([["attention.mp3"]]);
	});

	it("plays nothing when sound notifications are off", () => {
		useSoundNotificationsStore.setState({ enabled: false });
		requestSessionSound("attention");
		vi.runAllTimers();
		expect(play).not.toHaveBeenCalled();
	});

	it("seeds silently from the first workspace snapshot, then sounds transitions", () => {
		const queryClient = new QueryClient();
		const stop = startSessionSounds(queryClient, () => undefined);
		const snapshot = (status: string) => [{ sessions: [{ id: "s1", status }] }];
		queryClient.setQueryData(["workspaces"], snapshot("working"));
		vi.runAllTimers();
		expect(play).not.toHaveBeenCalled();
		queryClient.setQueryData(["workspaces"], snapshot("idle"));
		vi.runAllTimers();
		expect(play.mock.calls).toEqual([["attention.mp3"]]);
		stop();
	});
});
