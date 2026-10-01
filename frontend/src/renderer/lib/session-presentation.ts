import {
	attentionZone,
	attentionZoneOrder,
	boardAttentionZoneOrder,
	boardKanbanColumnOrder,
	getAgentActivityView as getPortableAgentActivityView,
	getAttentionZoneView as getPortableAttentionZoneView,
	getAttentionZoneViewForZone as getPortableAttentionZoneViewForZone,
	getKanbanColumnView as getPortableKanbanColumnView,
	getSessionStatusView as getPortableSessionStatusView,
	getSessionTimelinePillView as getPortableSessionTimelinePillView,
	isAgentActivityWorking,
	isSessionIdle,
	type AgentActivityView,
	type AttentionZone,
	type AttentionZoneView,
	type KanbanColumn,
	type KanbanColumnView,
	type ProductUITranslator,
	type SessionStatusView,
	type SessionTimelinePillStatus,
	type SessionTimelinePillView,
} from "@aoagents/product-ui";
import type { TFunction } from "i18next";
import { appI18n, type MessageKey } from "../i18n";
import type { SessionActivity, SessionStatus } from "../types/workspace";

function translator(t: TFunction): ProductUITranslator {
	return (key, values) => t(key as MessageKey, values);
}

export function getAgentActivityView(
	activity?: SessionActivity | null,
	t: TFunction = appI18n.t,
): AgentActivityView {
	return getPortableAgentActivityView(activity, translator(t));
}

export function getSessionStatusView(
	status: SessionStatus,
	t: TFunction = appI18n.t,
): SessionStatusView {
	return getPortableSessionStatusView(status, translator(t));
}

export function getAttentionZoneView(
	status: SessionStatus,
	t: TFunction = appI18n.t,
): AttentionZoneView {
	return getPortableAttentionZoneView(status, translator(t));
}

export function getAttentionZoneViewForZone(
	zone: AttentionZone,
	t: TFunction = appI18n.t,
): AttentionZoneView {
	return getPortableAttentionZoneViewForZone(zone, translator(t));
}

export type SessionStatusIndicatorView = {
	/** Board-section status the icon is picked from; `closed` is a PR closed without merge. */
	status: SessionStatus | "closed";
	/** Text-colour tone class; icons paint with `currentColor`. */
	className: string;
	/** Raw agent activity: the indicator becomes a spinner while true. */
	working: boolean;
	label: string;
};

// The session indicator carries two independent signals. Tone and icon come
// from the board section represented by the SCM state, which survives a
// running agent — `status` is activity-first, so it collapses to `working` the
// moment an agent wakes and would otherwise take every pull request tone with
// it. Merged keeps its split-section tone instead of sharing Ready to merge's.
//
// Motion stays on raw agent activity: a working agent swaps the icon for a
// spinner in the section tone. A no-PR idle session is the exception to the
// preserved section tone: when its agent starts working it spins blue.
export function getSessionStatusIndicatorView(
	session: {
		activity?: SessionActivity | null;
		displayStatus?: string;
		scmStatus?: SessionStatus;
		status: SessionStatus;
	},
	t: TFunction = appI18n.t,
): SessionStatusIndicatorView {
	const working = isAgentActivityWorking(session.activity);
	if (session.displayStatus === "Closed without merge") {
		return {
			status: "closed",
			className: getSessionStatusView("exited", t).className,
			working,
			label: session.displayStatus,
		};
	}
	const sectionStatus = session.scmStatus ?? session.status;
	const toneStatus: SessionStatus = sectionStatus === "idle" && working ? "working" : sectionStatus;
	const statusView = getSessionStatusView(toneStatus, t);
	const className =
		toneStatus === "idle" || toneStatus === "merged"
			? statusView.className
			: getAttentionZoneView(toneStatus, t).titleClassName;
	const label =
		working && toneStatus !== "working"
			? `${getAgentActivityView(session.activity, t).label} · ${statusView.label}`
			: statusView.label;

	return { status: toneStatus, className, working, label };
}

export function getKanbanColumnView(
	column: KanbanColumn,
	t: TFunction = appI18n.t,
): KanbanColumnView {
	return getPortableKanbanColumnView(column, translator(t));
}

export function getSessionTimelinePillView(
	status: SessionTimelinePillStatus,
	t: TFunction = appI18n.t,
): SessionTimelinePillView {
	return getPortableSessionTimelinePillView(status, translator(t));
}

/** Live labels for the current locale (getters re-resolve on each access). */
export const attentionZoneLabel: Record<AttentionZone, string> = {
	get merge() {
		return getAttentionZoneViewForZone("merge").label;
	},
	get action() {
		return getAttentionZoneViewForZone("action").label;
	},
	get pending() {
		return getAttentionZoneViewForZone("pending").label;
	},
	get working() {
		return getAttentionZoneViewForZone("working").label;
	},
	get done() {
		return getAttentionZoneViewForZone("done").label;
	},
};

export {
	attentionZone,
	attentionZoneOrder,
	boardAttentionZoneOrder,
	boardKanbanColumnOrder,
	isAgentActivityWorking,
	isSessionIdle,
};
export type {
	AgentActivityView,
	AttentionZone,
	AttentionZoneView,
	KanbanColumnView,
	SessionStatusView,
	SessionTimelinePillStatus,
	SessionTimelinePillView,
};
