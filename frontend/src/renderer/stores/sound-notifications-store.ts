import { create } from "zustand";
import { aoBridge } from "../lib/bridge";

type SoundNotificationsState = {
	enabled: boolean;
	/** Quiet FYI sounds (status changes, received messages, merges). Only audible while `enabled`. */
	ambientEnabled: boolean;
	loaded: boolean;
	saving: boolean;
	saveError: boolean;
	load: () => Promise<void>;
	setEnabled: (enabled: boolean) => Promise<void>;
	setAmbientEnabled: (enabled: boolean) => Promise<void>;
};

const DEFAULT_ENABLED = true;
const DEFAULT_AMBIENT_ENABLED = true;

let settingRevision = 0;
let pendingLoad: Promise<void> | undefined;

export const useSoundNotificationsStore = create<SoundNotificationsState>((set, get) => {
	const save = async (
		patch: { soundNotificationsEnabled: boolean } | { ambientSoundsEnabled: boolean },
		local: Partial<Pick<SoundNotificationsState, "enabled" | "ambientEnabled">>,
	) => {
		const revision = ++settingRevision;
		set({ saving: true, saveError: false });
		try {
			await aoBridge.uiSettings.set(patch);
			if (revision === settingRevision) set({ ...local, loaded: true, saving: false });
		} catch {
			if (revision === settingRevision) set({ saving: false, saveError: true });
		}
	};
	return {
		enabled: DEFAULT_ENABLED,
		ambientEnabled: DEFAULT_AMBIENT_ENABLED,
		loaded: false,
		saving: false,
		saveError: false,
		load: async () => {
			if (get().loaded) return;
			if (pendingLoad) return pendingLoad;
			const revisionAtStart = settingRevision;
			pendingLoad = (async () => {
				let enabled = DEFAULT_ENABLED;
				let ambientEnabled = DEFAULT_AMBIENT_ENABLED;
				try {
					const settings = await aoBridge.uiSettings.get();
					enabled = settings.soundNotificationsEnabled;
					ambientEnabled = settings.ambientSoundsEnabled ?? DEFAULT_AMBIENT_ENABLED;
				} catch {
					// A missing bridge or unreadable setting must not prevent the UI from starting.
				}
				if (revisionAtStart === settingRevision) set({ enabled, ambientEnabled, loaded: true });
			})();
			try {
				await pendingLoad;
			} finally {
				pendingLoad = undefined;
			}
		},
		setEnabled: (enabled) => save({ soundNotificationsEnabled: enabled }, { enabled }),
		setAmbientEnabled: (ambientEnabled) => save({ ambientSoundsEnabled: ambientEnabled }, { ambientEnabled }),
	};
});
