/**
 * Mechanical-keyboard keystroke sounds, synthesized with Web Audio so every
 * press varies slightly instead of replaying one sample. A press is a filtered
 * noise transient (the switch's click/leaf) over a short pitch-dropping tone
 * (the keycap bottoming out); wide keys sound lower and longer.
 */

type Click = { freq: number; q: number; gain: number; decay: number; delay?: number };
type Voice = {
	clicks: Click[];
	body: { freq: number; gain: number; decay: number };
	/** Typewriter carriage-return bell on Enter. */
	bell?: boolean;
};

export const KEYBOARD_SOUND_PROFILES = {
	clicky: {
		clicks: [
			{ freq: 4200, q: 3, gain: 0.5, decay: 0.012 },
			{ freq: 3000, q: 3, gain: 0.3, decay: 0.01, delay: 0.009 },
		],
		body: { freq: 380, gain: 0.2, decay: 0.035 },
	},
	tactile: {
		clicks: [{ freq: 2500, q: 1.5, gain: 0.3, decay: 0.015 }],
		body: { freq: 260, gain: 0.35, decay: 0.055 },
	},
	linear: {
		clicks: [{ freq: 1800, q: 1, gain: 0.14, decay: 0.01 }],
		body: { freq: 220, gain: 0.35, decay: 0.05 },
	},
	thock: {
		clicks: [{ freq: 900, q: 0.8, gain: 0.22, decay: 0.02 }],
		body: { freq: 135, gain: 0.6, decay: 0.09 },
	},
	typewriter: {
		clicks: [
			{ freq: 3200, q: 2, gain: 0.6, decay: 0.025 },
			{ freq: 1400, q: 4, gain: 0.25, decay: 0.03, delay: 0.004 },
		],
		body: { freq: 180, gain: 0.3, decay: 0.05 },
		bell: true,
	},
} satisfies Record<string, Voice>;

export type KeyboardSoundProfile = keyof typeof KEYBOARD_SOUND_PROFILES;
export const DEFAULT_KEYBOARD_SOUND_PROFILE: KeyboardSoundProfile = "tactile";

export function isKeyboardSoundProfile(value: unknown): value is KeyboardSoundProfile {
	return typeof value === "string" && Object.hasOwn(KEYBOARD_SOUND_PROFILES, value);
}

const WIDE_KEYS = new Set([" ", "Enter", "Backspace", "Tab", "Shift", "CapsLock"]);
const MASTER_GAIN = 0.35;

let context: AudioContext | undefined;
let noise: AudioBuffer | undefined;

function audio(): { ctx: AudioContext; noise: AudioBuffer } | undefined {
	if (typeof AudioContext === "undefined") return undefined;
	if (!context) {
		context = new AudioContext();
		noise = context.createBuffer(1, Math.ceil(context.sampleRate * 0.1), context.sampleRate);
		const data = noise.getChannelData(0);
		for (let i = 0; i < data.length; i++) data[i] = Math.random() * 2 - 1;
	}
	if (context.state === "suspended") void context.resume();
	return { ctx: context, noise: noise! };
}

function envelope(ctx: AudioContext, at: number, peak: number, decay: number): GainNode {
	const gain = ctx.createGain();
	gain.gain.setValueAtTime(peak, at);
	gain.gain.exponentialRampToValueAtTime(0.0001, at + decay);
	gain.connect(ctx.destination);
	return gain;
}

export function playKeySound(profile: KeyboardSoundProfile, key: string): void {
	const a = audio();
	if (!a) return;
	const { ctx } = a;
	const voice: Voice = KEYBOARD_SOUND_PROFILES[profile];
	const wide = WIDE_KEYS.has(key);
	const pitch = (wide ? 0.72 : 1) * (0.94 + Math.random() * 0.12);
	const length = wide ? 1.4 : 1;
	const now = ctx.currentTime;

	for (const click of voice.clicks) {
		const at = now + (click.delay ?? 0);
		const src = ctx.createBufferSource();
		src.buffer = a.noise;
		const filter = ctx.createBiquadFilter();
		filter.type = "bandpass";
		filter.frequency.value = click.freq * pitch;
		filter.Q.value = click.q;
		src.connect(filter).connect(envelope(ctx, at, click.gain * MASTER_GAIN, click.decay * length));
		src.start(at, Math.random() * 0.05, click.decay * length + 0.01);
	}

	const body = ctx.createOscillator();
	body.type = "triangle";
	body.frequency.setValueAtTime(voice.body.freq * pitch * 1.6, now);
	body.frequency.exponentialRampToValueAtTime(voice.body.freq * pitch, now + 0.015);
	const decay = voice.body.decay * length;
	body.connect(envelope(ctx, now, voice.body.gain * MASTER_GAIN, decay));
	body.start(now);
	body.stop(now + decay + 0.01);

	if (voice.bell && key === "Enter") {
		const bell = ctx.createOscillator();
		bell.frequency.value = 2093;
		bell.connect(envelope(ctx, now + 0.06, 0.25 * MASTER_GAIN, 0.8));
		bell.start(now + 0.06);
		bell.stop(now + 0.9);
	}
}

/**
 * Plays a keystroke on every physical key press anywhere in the window
 * (inputs, chat composer, terminal). Auto-repeat is skipped: a held key makes
 * no extra sound on a real keyboard.
 */
export function startKeyboardSounds(getProfile: () => KeyboardSoundProfile | null): () => void {
	const onKeyDown = (event: KeyboardEvent) => {
		if (event.repeat) return;
		const profile = getProfile();
		if (profile) playKeySound(profile, event.key);
	};
	window.addEventListener("keydown", onKeyDown, { capture: true, passive: true });
	return () => window.removeEventListener("keydown", onKeyDown, { capture: true });
}
