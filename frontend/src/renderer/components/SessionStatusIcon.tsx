import {
	Archive,
	BadgeCheck,
	Circle,
	CircleCheck,
	CircleDashed,
	CircleDot,
	CircleQuestionMark,
	CircleX,
	GitMerge,
	GitPullRequest,
	GitPullRequestClosed,
	GitPullRequestDraft,
	MessageCircleQuestionMark,
	MessageSquareWarning,
	PowerOff,
	type LucideIcon,
} from "lucide-react";
import { getSessionStatusIndicatorView, type SessionStatusIndicatorView } from "../lib/session-presentation";
import { cn } from "../lib/utils";

// One glyph per board-section status so the row says what state it is in
// without relying on colour alone.
const statusIcons: Record<SessionStatusIndicatorView["status"], LucideIcon> = {
	working: CircleDot,
	idle: Circle,
	needs_input: MessageCircleQuestionMark,
	exited: PowerOff,
	no_signal: CircleDashed,
	ci_failed: CircleX,
	changes_requested: MessageSquareWarning,
	review_pending: GitPullRequest,
	draft: GitPullRequestDraft,
	pr_open: GitPullRequest,
	approved: BadgeCheck,
	mergeable: CircleCheck,
	merged: GitMerge,
	terminated: Archive,
	unknown: CircleQuestionMark,
	closed: GitPullRequestClosed,
};

// Faint track plus a spinning arc: reads as "busy" at 14px where a pulsing dot
// did not. Reduced motion keeps the ring static — still distinct from any icon.
function StatusSpinner({ className }: { className?: string }) {
	return (
		<svg className={cn("animate-spin motion-reduce:animate-none", className)} fill="none" viewBox="0 0 16 16">
			<circle cx="8" cy="8" r="6" stroke="currentColor" strokeOpacity={0.25} strokeWidth={2} />
			<path d="M8 2a6 6 0 0 1 6 6" stroke="currentColor" strokeLinecap="round" strokeWidth={2} />
		</svg>
	);
}

export function SessionStatusIcon({
	className,
	session,
}: {
	className?: string;
	session: Parameters<typeof getSessionStatusIndicatorView>[0];
}) {
	const view = getSessionStatusIndicatorView(session);
	const Icon = statusIcons[view.status] ?? CircleQuestionMark;
	return (
		<span
			aria-hidden="true"
			className={cn("inline-flex size-4 shrink-0 items-center justify-center", view.className, className)}
			data-session-indicator={view.working ? "working" : view.status}
			data-session-status={session.status}
			title={view.label}
		>
			{view.working ? <StatusSpinner className="size-3.5" /> : <Icon className="size-3.5" strokeWidth={2.25} />}
		</span>
	);
}
