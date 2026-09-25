import { afterEach, describe, expect, it, vi } from "vitest";
import { isKeyboardSoundProfile, startKeyboardSounds } from "./keyboard-sounds";

describe("keyboard sounds", () => {
	let stop: (() => void) | undefined;
	afterEach(() => stop?.());

	it("only accepts known switch profiles", () => {
		expect(isKeyboardSoundProfile("clicky")).toBe(true);
		expect(isKeyboardSoundProfile("toString")).toBe(false);
		expect(isKeyboardSoundProfile(null)).toBe(false);
	});

	it("asks for the profile on fresh presses only, not auto-repeat", () => {
		const getProfile = vi.fn(() => null);
		stop = startKeyboardSounds(getProfile);
		window.dispatchEvent(new KeyboardEvent("keydown", { key: "a" }));
		window.dispatchEvent(new KeyboardEvent("keydown", { key: "a", repeat: true }));
		expect(getProfile).toHaveBeenCalledTimes(1);
	});
});
