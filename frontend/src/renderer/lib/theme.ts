export type Theme = "light" | "dark";
export type ThemePreference = Theme | "system";

export type ThemeStyle =
	| "orchestrate"
	| "orchestrate-deep"
	| "github"
	| "catppuccin"
	| "dracula"
	| "tokyo-night"
	| "rose-pine"
	| "nord"
	| "gruvbox"
	| "solarized";

export const themeStorageKey = "ao.theme";
export const themeStyleStorageKey = "ao.theme-style";
export const backgroundImageStorageKey = "ao.background-image";

function getLocalStorage() {
	if (typeof window === "undefined" || !window.localStorage) return null;
	return window.localStorage;
}

export function systemTheme(): Theme {
	if (typeof window === "undefined") return "dark";
	return window.matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
}

export function readStoredThemePreference(): ThemePreference {
	try {
		const stored = getLocalStorage()?.getItem(themeStorageKey);
		if (stored === "light" || stored === "dark" || stored === "system") return stored;
	} catch {
		// ignore
	}
	return "system";
}

/** Resolve the active light/dark appearance from a stored preference. */
export function resolveTheme(preference: ThemePreference = readStoredThemePreference()): Theme {
	if (preference === "system") return systemTheme();
	return preference;
}

export function readStoredThemeStyle(): ThemeStyle {
	try {
		const stored = getLocalStorage()?.getItem(themeStyleStorageKey);
		if (
			stored === "orchestrate" ||
			stored === "orchestrate-deep" ||
			stored === "github" ||
			stored === "catppuccin" ||
			stored === "dracula" ||
			stored === "tokyo-night" ||
			stored === "rose-pine" ||
			stored === "nord" ||
			stored === "gruvbox" ||
			stored === "solarized"
		) {
			return stored;
		}
	} catch {
		// ignore
	}
	return "orchestrate";
}

export function applyDocumentTheme(theme: Theme): void {
	if (typeof document === "undefined") return;
	document.documentElement.dataset.theme = theme;
	document.documentElement.style.colorScheme = theme;
}

export function applyDocumentThemeStyle(style: ThemeStyle): void {
	if (typeof document === "undefined") return;
	if (style === "orchestrate") {
		delete document.documentElement.dataset.styleTheme;
	} else {
		document.documentElement.dataset.styleTheme = style;
	}
}

/** A downscaled image data URL painted behind the home page and empty chats. */
export function readStoredBackgroundImage(): string | null {
	try {
		return getLocalStorage()?.getItem(backgroundImageStorageKey) || null;
	} catch {
		return null;
	}
}

export function applyDocumentBackgroundImage(dataUrl: string | null): void {
	if (typeof document === "undefined") return;
	const root = document.documentElement;
	if (dataUrl) {
		root.dataset.backdrop = "image";
		root.style.setProperty("--app-backdrop-image", `url("${dataUrl}")`);
	} else {
		delete root.dataset.backdrop;
		root.style.removeProperty("--app-backdrop-image");
	}
}

/**
 * Apply a theme DOM update under a View Transition so per-element
 * `transition-colors` / background tweens are hidden behind a snapshot.
 * Default VT crossfade is disabled in CSS — this is an instant cut.
 * Falls back to a plain update when the API is unavailable.
 *
 * Also sets `data-theme-transition` so global CSS can zero out
 * transition-duration while tokens swap; otherwise composer, search, and
 * switch chrome tween from the old palette and look stuck white/black.
 */
export function runThemeTransition(update: () => void): void {
	if (typeof document === "undefined") return;

	const root = document.documentElement;
	const finish = () => {
		requestAnimationFrame(() => {
			delete root.dataset.themeTransition;
		});
	};
	const run = () => {
		root.dataset.themeTransition = "active";
		update();
		void root.offsetHeight;
	};

	if (typeof document.startViewTransition !== "function") {
		run();
		finish();
		return;
	}

	const transition = document.startViewTransition(() => {
		run();
	});
	void transition.finished.finally(finish);
}
