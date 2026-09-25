import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { ThemePreference, ThemeStyle } from "../../lib/theme";
import { playKeySound, type KeyboardSoundProfile } from "../../lib/keyboard-sounds";
import type { AppLocale } from "../../i18n";
import { useLocaleStore } from "../../stores/locale-store";
import { useSoundNotificationsStore } from "../../stores/sound-notifications-store";
import { useUiStore } from "../../stores/ui-store";
import { useTelemetryPolicyStore } from "../../stores/telemetry-policy-store";
import { ConfirmDialog } from "../ConfirmDialog";
import { useTerminalShellStore } from "../../stores/terminal-shell-store";
import { SettingsOptionMenu, type SettingsOption } from "./SettingsOptionMenu";
import { SettingsInputRow, SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";
import { Button } from "../ui/button";
import { Switch } from "../ui/switch";
import { cn } from "../../lib/utils";
import { useSettings, useUpdateCloudOffering, useUpdateSessionInterface } from "../../hooks/useSettings";
import type { SessionMode } from "../../types/workspace";
import type { TerminalShellKind } from "../../../shared/ui-locale";
import { isWindowsPlatform } from "../../lib/platform";

/**
 * Default interface for new sessions. Daemon-owned so `ao spawn` and mobile
 * resolve the same value. Only affects sessions created afterwards — a
 * session's interface is fixed when it is born.
 */
function SessionInterfaceRow() {
	const { t } = useTranslation();
	const { settings, isLoading, error } = useSettings();
	const { update, saving, error: saveError } = useUpdateSessionInterface();
	const interfaceOptions = [
		{ value: "tui", label: t("settings.sessionInterface.terminal") },
		{ value: "chat", label: t("settings.sessionInterface.chat") },
	] satisfies SettingsOption<SessionMode>[];

	const chatAvailable = (settings?.chatHarnesses.length ?? 0) > 0;
	// Silent when everything works; speak up only when the control is limited
	// (no chat-capable agent installed) or a save failed.
	const note = saveError ?? error ?? (!chatAvailable ? t("settings.sessionInterface.unavailable") : null);

	return (
		<div className="flex w-full flex-col">
			<SettingsRow className="rounded-none" label={t("settings.sessionInterface.label")}>
				<SettingsOptionMenu
					aria-label={t("settings.sessionInterface.label")}
					value={settings?.defaultSessionMode ?? "tui"}
					options={interfaceOptions}
					onChange={(mode) => update(mode)}
					disabled={isLoading || saving || !chatAvailable}
				/>
			</SettingsRow>
			{note ? (
				<p
					className={cn(
						"px-3 pt-0 pb-4 text-xs leading-relaxed",
						saveError || error ? "text-destructive" : "text-muted-foreground",
					)}
				>
					{note}
				</p>
			) : null}
		</div>
	);
}

function TerminalShellRows() {
	const { t } = useTranslation();
	const preference = useTerminalShellStore((state) => state.preference);
	const load = useTerminalShellStore((state) => state.load);
	const setPreference = useTerminalShellStore((state) => state.setPreference);
	const saving = useTerminalShellStore((state) => state.saving);
	const saveError = useTerminalShellStore((state) => state.saveError);
	const [customPath, setCustomPath] = useState(preference.path ?? "");

	useEffect(() => {
		void load();
	}, [load]);

	useEffect(() => {
		setCustomPath(preference.path ?? "");
	}, [preference.path]);

	const shellOptions = [
		{ value: "auto", label: t("settings.terminalShell.auto") },
		{ value: "git-bash", label: t("settings.terminalShell.gitBash") },
		{ value: "pwsh", label: t("settings.terminalShell.pwsh") },
		{ value: "powershell", label: t("settings.terminalShell.windowsPowerShell") },
		{ value: "cmd", label: t("settings.terminalShell.cmd") },
		{ value: "custom", label: t("settings.terminalShell.custom") },
	] satisfies SettingsOption<TerminalShellKind>[];

	return (
		<>
			<SettingsRow label={t("settings.terminalShell.label")}>
				<SettingsOptionMenu
					aria-label={t("settings.terminalShell.label")}
					value={preference.kind}
					options={shellOptions}
					disabled={saving}
					onChange={(kind) => {
						void setPreference(kind === "custom" ? { kind, path: customPath } : { kind });
					}}
				/>
			</SettingsRow>
			{preference.kind === "custom" ? (
				<SettingsInputRow
					id="terminal-shell-custom-path"
					label={t("settings.terminalShell.customPath")}
					value={customPath}
					onChange={setCustomPath}
					onCommit={(path) => void setPreference({ kind: "custom", path })}
					onCancel={() => setCustomPath(preference.path ?? "")}
					placeholder={t("settings.terminalShell.customPathPlaceholder")}
				/>
			) : null}
			{saveError ? (
				<p role="alert" className="px-3 text-caption leading-4 text-error">
					{t("settings.terminalShell.saveFailed")}
				</p>
			) : null}
		</>
	);
}

const BACKGROUND_IMAGE_MAX_EDGE = 2560;

/** Downscale so the data URL stays well inside the localStorage quota. */
async function readBackgroundImage(file: File): Promise<string> {
	const bitmap = await createImageBitmap(file);
	const scale = Math.min(1, BACKGROUND_IMAGE_MAX_EDGE / Math.max(bitmap.width, bitmap.height));
	const canvas = document.createElement("canvas");
	canvas.width = Math.round(bitmap.width * scale);
	canvas.height = Math.round(bitmap.height * scale);
	canvas.getContext("2d")?.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
	bitmap.close();
	return canvas.toDataURL("image/jpeg", 0.85);
}

function BackgroundImageRow() {
	const { t } = useTranslation();
	const backgroundImage = useUiStore((state) => state.backgroundImage);
	const setBackgroundImage = useUiStore((state) => state.setBackgroundImage);
	const inputRef = useRef<HTMLInputElement>(null);
	const [saveError, setSaveError] = useState(false);
	const choose = async (file: File | undefined) => {
		if (!file) return;
		try {
			setBackgroundImage(await readBackgroundImage(file));
			setSaveError(false);
		} catch {
			setSaveError(true);
		}
	};
	return (
		<>
			<SettingsRow label={t("settings.backgroundImage")}>
				<div className="flex items-center gap-1.5">
					{backgroundImage ? (
						<>
							<span
								aria-hidden="true"
								className="h-6 w-9 rounded-sm border border-border bg-cover bg-center"
								style={{ backgroundImage: `url("${backgroundImage}")` }}
							/>
							<Button type="button" variant="outline" onClick={() => setBackgroundImage(null)}>
								{t("settings.backgroundImage.remove")}
							</Button>
						</>
					) : null}
					<Button type="button" variant="outline" onClick={() => inputRef.current?.click()}>
						{t("settings.backgroundImage.choose")}
					</Button>
					<input
						ref={inputRef}
						type="file"
						accept="image/*"
						hidden
						onChange={(event) => {
							void choose(event.target.files?.[0]);
							event.target.value = "";
						}}
					/>
				</div>
			</SettingsRow>
			{saveError ? (
				<p role="alert" className="px-3 text-caption leading-4 text-error">
					{t("settings.backgroundImage.saveFailed")}
				</p>
			) : null}
		</>
	);
}

const COLOR_THEME_OPTIONS = [
	{ value: "orchestrate", label: "Orchestrate" },
	{ value: "orchestrate-deep", label: "Orchestrate Deep" },
	{ value: "github", label: "GitHub" },
	{ value: "catppuccin", label: "Catppuccin" },
	{ value: "dracula", label: "Dracula" },
	{ value: "tokyo-night", label: "Tokyo Night" },
	{ value: "rose-pine", label: "Rosé Pine" },
	{ value: "nord", label: "Nord" },
	{ value: "gruvbox", label: "Gruvbox" },
	{ value: "solarized", label: "Solarized" },
] satisfies SettingsOption<ThemeStyle>[];

const KEYBOARD_SOUND_OPTIONS = [
	{ value: "clicky", label: "Clicky (Blue)" },
	{ value: "tactile", label: "Tactile (Brown)" },
	{ value: "linear", label: "Linear (Red)" },
	{ value: "thock", label: "Thock" },
	{ value: "typewriter", label: "Typewriter" },
] satisfies SettingsOption<KeyboardSoundProfile>[];

function KeyboardSoundsRow() {
	const { t } = useTranslation();
	const enabled = useUiStore((state) => state.keyboardSoundsEnabled);
	const setEnabled = useUiStore((state) => state.setKeyboardSoundsEnabled);
	const profile = useUiStore((state) => state.keyboardSoundProfile);
	const setProfile = useUiStore((state) => state.setKeyboardSoundProfile);
	return (
		<SettingsRow label={t("settings.keyboardSounds")} description={t("settings.keyboardSounds.description")}>
			<div className="flex items-center gap-1.5">
				<SettingsOptionMenu
					aria-label={t("settings.keyboardSounds.profile")}
					disabled={!enabled}
					value={profile}
					options={KEYBOARD_SOUND_OPTIONS}
					onChange={(next) => {
						setProfile(next);
						playKeySound(next, "a");
					}}
				/>
				<Switch aria-label={t("settings.keyboardSounds")} checked={enabled} onCheckedChange={setEnabled} />
			</div>
		</SettingsRow>
	);
}

export function GeneralSettingsSection({
	titleHidden,
}: {
	titleHidden?: boolean;
}) {
	const { t } = useTranslation();
	const themePreference = useUiStore((state) => state.themePreference);
	const setThemePreference = useUiStore((state) => state.setThemePreference);
	const themeStyle = useUiStore((state) => state.themeStyle);
	const setThemeStyle = useUiStore((state) => state.setThemeStyle);
	const locale = useLocaleStore((state) => state.locale);
	const setLocale = useLocaleStore((state) => state.setLocale);
	const localeSaving = useLocaleStore((state) => state.saving);
	const localeSaveError = useLocaleStore((state) => state.saveError);
	const soundNotificationsEnabled = useSoundNotificationsStore((state) => state.enabled);
	const setSoundNotificationsEnabled = useSoundNotificationsStore((state) => state.setEnabled);
	const soundNotificationsSaving = useSoundNotificationsStore((state) => state.saving);
	const soundNotificationsSaveError = useSoundNotificationsStore((state) => state.saveError);
	const ambientSoundsEnabled = useSoundNotificationsStore((state) => state.ambientEnabled);
	const setAmbientSoundsEnabled = useSoundNotificationsStore((state) => state.setAmbientEnabled);
	const developerMode = useUiStore((state) => state.developerMode);
	const setDeveloperMode = useUiStore((state) => state.setDeveloperMode);
	const remoteHosts = useUiStore((state) => state.remoteHosts);
	const setRemoteHosts = useUiStore((state) => state.setRemoteHosts);
	const terminalCopyOnSelect = useUiStore((state) => state.terminalCopyOnSelect);
	const setTerminalCopyOnSelect = useUiStore((state) => state.setTerminalCopyOnSelect);

	const themeOptions = [
		{ value: "light", label: t("settings.theme.light") },
		{ value: "dark", label: t("settings.theme.dark") },
		{ value: "system", label: t("settings.theme.system") },
	] satisfies SettingsOption<ThemePreference>[];

	const languageOptions = [
		{ value: "en", label: t("settings.language.en") },
		{ value: "zh-CN", label: t("settings.language.zhCN") },
		{ value: "ja", label: t("settings.language.ja") },
		{ value: "ko", label: t("settings.language.ko") },
		{ value: "es", label: t("settings.language.es") },
		{ value: "fr", label: t("settings.language.fr") },
		{ value: "de", label: t("settings.language.de") },
		{ value: "pt-BR", label: t("settings.language.ptBR") },
	] satisfies SettingsOption<AppLocale>[];

	return (
		<>
			{/* Appearance */}
			<SettingsSection title={t("settings.appearance")} titleHidden={titleHidden} grouped>
				<SettingsRow label={t("settings.theme")}>
					<div className="flex items-center gap-1.5">
						<SettingsOptionMenu
							aria-label={t("settings.colorTheme")}
							value={themeStyle}
							options={COLOR_THEME_OPTIONS}
							onChange={setThemeStyle}
						/>
						<SettingsOptionMenu
							aria-label={t("settings.theme")}
							value={themePreference}
							options={themeOptions}
							onChange={setThemePreference}
						/>
					</div>
				</SettingsRow>
				<BackgroundImageRow />
				<SettingsRow label={t("settings.language")}>
					<SettingsOptionMenu
						aria-label={t("settings.language")}
						disabled={localeSaving}
						value={locale}
						options={languageOptions}
						onChange={(next) => {
							void setLocale(next);
						}}
					/>
				</SettingsRow>
				{localeSaveError ? (
					<p role="alert" className="px-3 text-caption leading-4 text-error">
						{t("settings.language.saveFailed")}
					</p>
				) : null}
			</SettingsSection>

			{/* Sessions */}
			<SettingsSection title={t("settings.sessions")} grouped>
				<SessionInterfaceRow />
				{isWindowsPlatform() ? <TerminalShellRows /> : null}
				<SettingsRow label={t("settings.terminalCopyOnSelect")}>
					<Switch
						aria-label={t("settings.terminalCopyOnSelect")}
						checked={terminalCopyOnSelect}
						onCheckedChange={setTerminalCopyOnSelect}
					/>
				</SettingsRow>
				<SettingsRow label={t("settings.soundNotifications")}>
					<Switch
						aria-label={t("settings.soundNotifications")}
						checked={soundNotificationsEnabled}
						disabled={soundNotificationsSaving}
						onCheckedChange={(next) => {
							void setSoundNotificationsEnabled(next);
						}}
					/>
				</SettingsRow>
				<SettingsRow label={t("settings.ambientSounds")} description={t("settings.ambientSounds.description")}>
					<Switch
						aria-label={t("settings.ambientSounds")}
						checked={soundNotificationsEnabled && ambientSoundsEnabled}
						disabled={soundNotificationsSaving || !soundNotificationsEnabled}
						onCheckedChange={(next) => {
							void setAmbientSoundsEnabled(next);
						}}
					/>
				</SettingsRow>
				<KeyboardSoundsRow />
				{soundNotificationsSaveError ? (
					<p role="alert" className="px-3 text-caption leading-4 text-error">
						{t("settings.soundNotifications.saveFailed")}
					</p>
				) : null}
			</SettingsSection>

			<SettingsSection title={t("settings.privacy")} grouped>
				<TelemetryEventsRow />
			</SettingsSection>

			{/* Advanced */}
			<SettingsSection title={t("settings.advanced")} grouped>
				<SettingsRow label={t("settings.developerMode")}>
					<Switch
						aria-label={t("settings.developerMode")}
						checked={developerMode}
						onCheckedChange={setDeveloperMode}
					/>
				</SettingsRow>
				<SettingsRow label={t("settings.remoteHosts")}>
					<Switch
						aria-label={t("settings.remoteHosts")}
						checked={remoteHosts}
						onCheckedChange={setRemoteHosts}
					/>
				</SettingsRow>
				{developerMode && <CloudOfferingRow />}
			</SettingsSection>
		</>
	);
}

function TelemetryEventsRow() {
	const { t } = useTranslation();
	const view = useTelemetryPolicyStore((state) => state.view);
	const saving = useTelemetryPolicyStore((state) => state.saving);
	const saveError = useTelemetryPolicyStore((state) => state.saveError);
	const setEnabled = useTelemetryPolicyStore((state) => state.setEnabled);
	const checked = view?.eventsEnabled ?? false;
	const blockedEnable = !checked && (view?.environmentVeto || !view?.durabilitySupported);
	const status = saveError ? "failed"
		: !view ? null
		: !view.durabilitySupported ? "unsupported"
		: view.state === "cleanup_failed" ? "failed"
		: view.state === "cleanup_pending" ? "pending"
		: view.reason === "environment_veto" ? "veto"
		: view.reason === "release_blocked" ? "releaseBlocked"
		: null;
	return <div className="flex w-full flex-col">
		<SettingsRow label={t("settings.telemetryEvents.label")}>
			<Switch aria-label={t("settings.telemetryEvents.label")} checked={checked} disabled={saving || !view || blockedEnable} onCheckedChange={(enabled) => { void setEnabled(enabled); }} />
		</SettingsRow>
		<p className={cn("pb-2 text-xs leading-relaxed", status === "failed" ? "text-destructive" : "text-muted-foreground")} role={status === "failed" ? "alert" : undefined}>
			{t(status ? `settings.telemetryEvents.${status}` : "settings.telemetryEvents.description")}
		</p>
	</div>;
}

/**
 * Developer Mode-only toggle for the cloud offering. Persisted daemon-side (a
 * preference, not renderer state) so every surface resolves the same gate; the
 * daemon combines it with its baked control-plane URL into cloudEnabled, which
 * is what reveals sign-in, cloud projects, and sandbox sessions.
 */
function CloudOfferingRow() {
	const { t } = useTranslation();
	const { settings, isLoading } = useSettings();
	const { update, saving, error } = useUpdateCloudOffering();
	// Enabling requires an explicit confirmation (the offering is an unstable
	// preview); disabling never does.
	const [confirmOpen, setConfirmOpen] = useState(false);
	return (
		<div className="flex w-full flex-col">
			<SettingsRow label={t("settings.cloud")}>
				<Switch
					aria-label={t("settings.cloud")}
					checked={settings?.cloudOffering ?? false}
					disabled={isLoading || saving}
					onCheckedChange={(enabled) => {
						if (enabled) {
							setConfirmOpen(true);
							return;
						}
						update(false);
					}}
				/>
			</SettingsRow>
			<ConfirmDialog
				open={confirmOpen}
				title={t("settings.cloudConfirm.title")}
				description={t("settings.cloudConfirm.description")}
				confirmLabel={t("settings.cloudConfirm.confirm")}
				destructive
				busy={saving}
				onConfirm={() => {
					update(true);
					setConfirmOpen(false);
				}}
				onOpenChange={setConfirmOpen}
			/>
			<p className="px-3 pb-2 text-xs leading-relaxed text-muted-foreground">{t("settings.cloudToggleHint")}</p>
			{error ? (
				<p role="alert" className="px-3 pb-2 text-caption leading-4 text-error">
					{error}
				</p>
			) : null}
		</div>
	);
}
