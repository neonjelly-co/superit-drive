import type { ButtonHTMLAttributes } from "react";

const base =
	"inline-flex h-9 shrink-0 items-center justify-center gap-1.5 rounded-md px-3 text-sm font-medium outline-none " +
	"focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:pointer-events-none disabled:opacity-50 [&_svg]:size-4";

const variants = {
	primary: "bg-primary text-primary-foreground hover:bg-primary/80",
	outline: "border border-border bg-card hover:bg-muted",
	ghost: "hover:bg-muted",
	/** Deleting goes to the trash and can be undone: a quiet button, red only in its words. */
	danger: "border border-border bg-card text-destructive hover:bg-destructive/10",
};

type Props = ButtonHTMLAttributes<HTMLButtonElement> & { variant?: keyof typeof variants; small?: boolean };

/** The interface's one button, in the Pilot light look. */
export function Button({ variant = "outline", small, className = "", type = "button", ...props }: Props) {
	return (
		<button
			type={type}
			className={`${base} ${small ? "h-8 px-2.5" : ""} ${variants[variant]} ${className}`}
			{...props}
		/>
	);
}
