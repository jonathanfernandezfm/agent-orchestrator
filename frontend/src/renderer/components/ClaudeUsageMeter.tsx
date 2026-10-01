import { useQuery } from "@tanstack/react-query";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import type { components } from "../../api/schema";
import claudeLogo from "../assets/agents/claude.svg";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { cn } from "../lib/utils";
import { formatPercentage, formatResetTime } from "./settings/CodexAccountDetails";
import { HoverCard, HoverCardContent, HoverCardTrigger } from "./ui/hover-card";

type ClaudeUsageWindow = components["schemas"]["ClaudeUsageWindowResponse"];

async function fetchClaudeUsage() {
	const { data, error } = await apiClient.GET("/api/v1/usage/claude");
	if (error || !data) throw new Error(apiErrorMessage(error));
	return data;
}

function windowLabel(window: ClaudeUsageWindow, t: TFunction): string {
	if (window.kind === "session") return t("shell.claudeUsage.session");
	if (window.kind === "weekly_all") return t("shell.claudeUsage.weeklyAll");
	if (window.model) return t("shell.claudeUsage.weeklyModel", { model: window.model });
	return window.kind.replaceAll("_", " ");
}

function barClass(window: ClaudeUsageWindow): string {
	if (window.usedPercent >= 100) return "bg-error";
	if (window.usedPercent >= 75 || window.severity !== "normal") return "bg-warning";
	return "bg-foreground/80";
}

function UsageBar({ window, className }: { window: ClaudeUsageWindow; className?: string }) {
	const used = Math.max(0, Math.min(100, window.usedPercent));
	return (
		<div className={cn("h-1.5 overflow-hidden rounded-full bg-muted", className)}>
			<div className={cn("h-full rounded-full transition-[width]", barClass(window))} style={{ width: `${used}%` }} />
		</div>
	);
}

/**
 * Sidebar footer meter for the signed-in Claude subscription: the current
 * session window as a bar, every window on hover. Hidden unless the daemon
 * found a claude.ai login and read its usage.
 */
export function ClaudeUsageMeter({ tabIndex }: { tabIndex: number }) {
	const { t, i18n } = useTranslation();
	const { data } = useQuery({
		queryKey: ["claude-usage"],
		queryFn: fetchClaudeUsage,
		refetchInterval: 60_000,
	});
	if (data?.state !== "available" || data.windows.length === 0) return null;
	const session = data.windows.find((window) => window.kind === "session") ?? data.windows[0];
	const sessionPercent = formatPercentage(session.usedPercent, i18n.language);

	return (
		<HoverCard openDelay={150}>
			<HoverCardTrigger asChild>
				<div
					aria-label={t("shell.claudeUsage.summary", { label: windowLabel(session, t), value: sessionPercent })}
					className="flex h-9 w-full items-center gap-2.5 rounded-lg px-2.5 text-sm font-medium text-muted-foreground outline-none hover:bg-interactive-hover focus-visible:bg-interactive-hover"
					role="group"
					tabIndex={tabIndex}
				>
					<img alt="" className="size-icon-md shrink-0" src={claudeLogo} />
					<UsageBar className="min-w-0 flex-1" window={session} />
					<span className="shrink-0 text-xs tabular-nums">{sessionPercent}</span>
				</div>
			</HoverCardTrigger>
			<HoverCardContent align="start" className="w-64 p-3 text-xs" side="top">
				<p className="mb-2.5 font-medium text-foreground">{t("shell.claudeUsage.title")}</p>
				<div className="space-y-3">
					{data.windows.map((window) => {
						const reset = formatResetTime(window.resetsAt, i18n.language);
						return (
							<div key={`${window.kind}-${window.model ?? ""}`}>
								<div className="mb-1 flex items-baseline justify-between gap-2">
									<span className="truncate text-foreground">{windowLabel(window, t)}</span>
									<span className="shrink-0 tabular-nums text-muted-foreground">
										{t("shell.claudeUsage.used", { value: formatPercentage(window.usedPercent, i18n.language) })}
									</span>
								</div>
								<UsageBar window={window} />
								{reset ? (
									<p className="mt-1 text-muted-foreground" title={reset.full}>
										{t("settings.codexAccounts.capacityResets", { value: reset.visible })}
									</p>
								) : null}
							</div>
						);
					})}
				</div>
			</HoverCardContent>
		</HoverCard>
	);
}
