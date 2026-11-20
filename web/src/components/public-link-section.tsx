import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Copy, Link2 } from "lucide-react";
import { useId, useState } from "react";
import { LINK_KIND_MEANS, nameOf } from "../access";
import { api, type LinkKind, type PublicLink } from "../api";
import { formatCapacity, formatChangedFull, formatSize } from "../format";
import { Button } from "./button";
import { Dialog } from "./dialog";

type Props = {
	placeId: number;
	itemId: number;
	what: "file" | "folder";
	/**
	 * True when the person may make a link here, change what it lets visitors do, and make room in an upload-only one
	 * (not where sharing outside the project is off).
	 */
	canMake: boolean;
	/** Called after a link was made, changed or removed, so the list and "who can see this" follow it. */
	onChanged: () => void;
};

/**
 * The addresses of the links made in this tab, until the page is reloaded. The Drive keeps only a fingerprint of a
 * link's address and never shows it again, so it is shown here, to the person who made it, while they are on the page.
 */
const madeHere = new Map<string, string>();
const keyOf = (placeId: number, itemId: number, link: PublicLink) => `${placeId}/${itemId}/${link.made}`;

type Asking = "remove" | "replace" | "show" | "reset";

/**
 * "Public link", in the details panel, for whoever may share an item: making a link anyone can open without signing
 * in, its address (shown once, when it is made), who made it and how often it was used, and removing it or making a new
 * one in its place. A folder's link either lets people open and download what is in it, or only send files into it
 * (upload-only): chosen when it is made, and changed at the same address. An upload-only link says how much it has
 * taken in; at its limit it takes no more until room is made.
 */
export function PublicLinkSection({ placeId, itemId, what, canMake, onChanged }: Props) {
	const queryClient = useQueryClient();
	const key = ["link", placeId, itemId];
	const link = useQuery({ queryKey: key, queryFn: () => api.link(placeId, itemId) });
	const [busy, setBusy] = useState(false);
	const [problem, setProblem] = useState<string>();
	const [asking, setAsking] = useState<Asking>();
	const [copied, setCopied] = useState(false);
	const [kind, setKind] = useState<LinkKind>("view");
	const addressId = useId();

	const change = async (run: () => Promise<PublicLink | null>) => {
		setBusy(true);
		setProblem(undefined);
		try {
			const made = await run();
			if (made?.address) madeHere.set(keyOf(placeId, itemId, made), made.address);
			queryClient.setQueryData(key, made);
			setCopied(false);
			void queryClient.invalidateQueries({ queryKey: ["access", placeId, itemId] });
			onChanged();
		} catch (e) {
			setProblem(e instanceof Error ? e.message : String(e));
			void link.refetch();
		} finally {
			setBusy(false);
			setAsking(undefined);
		}
	};
	const make = (replace: boolean, as: LinkKind) => change(() => api.makeLink(placeId, itemId, replace, as));
	const remove = () => change(() => api.removeLink(placeId, itemId).then(() => null));
	const changeKind = (as: LinkKind) => change(() => api.setLinkKind(placeId, itemId, as));
	const reset = () => change(() => api.resetLink(placeId, itemId));

	const current = link.data;
	const address = current ? (current.address ?? madeHere.get(keyOf(placeId, itemId, current))) : undefined;
	const reaches = what === "folder" ? "this folder and everything in it" : "this file";

	return (
		<section aria-labelledby={`${addressId}-title`} className="mt-6">
			<h3 id={`${addressId}-title`} className="text-sm font-semibold">
				Public link
			</h3>
			{link.error ? (
				<p className="mt-2 text-sm text-destructive">{link.error.message}</p>
			) : link.isPending ? (
				<p className="mt-2 text-sm text-muted-foreground">Loading…</p>
			) : !current ? (
				<>
					<p className="mt-0.5 text-sm text-muted-foreground">
						{what === "folder"
							? "Anyone with the link can use it without signing in, until the link is removed."
							: `Anyone with the link can open and download ${reaches}, without signing in, until the link is removed.`}
					</p>
					{canMake ? (
						<>
							{what === "folder" && <KindChoice name={`${addressId}-kind`} value={kind} disabled={busy} onChange={setKind} />}
							<Button className="mt-3" disabled={busy} onClick={() => void make(false, what === "folder" ? kind : "view")}>
								<Link2 aria-hidden /> Make a public link
							</Button>
						</>
					) : (
						<p className="mt-2 text-sm text-muted-foreground">Sharing outside the project is switched off here.</p>
					)}
				</>
			) : (
				<>
					<p className="mt-0.5 text-sm text-muted-foreground">
						{current.kind === "upload-only"
							? "Anyone with the link can send files into this folder, without signing in. They cannot see what is in it."
							: `Anyone with the link can open and download ${reaches}, without signing in.`}
					</p>
					{address ? (
						<div className="mt-3">
							<label htmlFor={addressId} className="sr-only">
								The link’s address
							</label>
							<div className="flex gap-2">
								<input
									id={addressId}
									readOnly
									value={address}
									onFocus={(e) => e.currentTarget.select()}
									className="h-9 min-w-0 flex-1 rounded-md border border-input bg-muted px-2 font-mono text-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
								/>
								<Button
									onClick={() => {
										void navigator.clipboard?.writeText(address).then(() => setCopied(true));
									}}
								>
									{copied ? <Check aria-hidden /> : <Copy aria-hidden />} {copied ? "Copied" : "Copy"}
								</Button>
							</div>
							<p className="mt-2 text-sm text-attention">
								Copy it now. For safety the Drive does not keep the address, so it cannot show it again once you
								leave this page.
							</p>
						</div>
					) : (
						<p className="mt-2 text-sm">
							The address was shown when the link was made. To send it to someone else, use the one you sent, or make
							a new link: the old address then stops working.
						</p>
					)}
					{what === "folder" && canMake && (
						<KindChoice
							name={`${addressId}-change`}
							value={current.kind}
							disabled={busy}
							// Showing the folder to everyone who has the address is asked first; hiding it is not.
							onChange={(as) => (as === "view" ? setAsking("show") : void changeKind(as))}
						/>
					)}
					<dl className="mt-3 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
						<dt className="text-muted-foreground">Made</dt>
						<dd>
							{formatChangedFull(current.made)}
							{current.madeBy ? ` by ${nameOf(current.madeBy)}` : current.former ? " by someone no longer in the project" : ""}
						</dd>
						<dt className="text-muted-foreground">Used</dt>
						<dd>
							{current.uses === 0
								? "Not yet"
								: `${current.uses === 1 ? "Once" : `${current.uses} times`}, last on ${formatChangedFull(current.used ?? current.made)}`}
						</dd>
						{current.kind === "upload-only" && current.takenIn !== undefined && current.limit !== undefined && (
							<>
								<dt className="text-muted-foreground">Taken in</dt>
								<dd>
									<TakenIn takenIn={current.takenIn} limit={current.limit} />
								</dd>
							</>
						)}
					</dl>
					{current.kind === "upload-only" && current.full && (
						<p className="mt-2 text-sm text-attention">
							It takes no more files until you make room. What was sent through it stays in the folder.
						</p>
					)}
					{!current.works && (
						<p className="mt-2 text-sm text-attention">
							It opens nothing at the moment: whoever made it can no longer share this, or sharing outside the project
							is switched off here. Remove it if it is no longer needed.
						</p>
					)}
					<div className="mt-3 flex flex-wrap gap-2">
						{canMake && current.kind === "upload-only" && (current.takenIn ?? 0) > 0 && (
							<Button disabled={busy} onClick={() => setAsking("reset")}>
								Make room for more
							</Button>
						)}
						{canMake && (
							<Button disabled={busy} onClick={() => setAsking("replace")}>
								Make a new link
							</Button>
						)}
						<Button variant="danger" disabled={busy} onClick={() => setAsking("remove")}>
							Remove link
						</Button>
					</div>
				</>
			)}
			{problem && (
				<p role="alert" className="mt-3 text-sm text-destructive">
					{problem}
				</p>
			)}
			{asking && current && (
				<Dialog
					title={QUESTIONS[asking].title}
					description={
						asking === "reset" && current.limit !== undefined
							? `It takes in up to ${formatCapacity(current.limit)} again, counted from now. The files it took in stay where they are.`
							: QUESTIONS[asking].description
					}
					onClose={() => setAsking(undefined)}
					onSubmit={() => {
						if (asking === "remove") void remove();
						else if (asking === "replace") void make(true, current.kind);
						else if (asking === "show") void changeKind("view");
						else void reset();
					}}
					footer={
						<>
							<Button onClick={() => setAsking(undefined)}>Cancel</Button>
							<Button type="submit" variant={asking === "remove" ? "danger" : "primary"} disabled={busy}>
								{QUESTIONS[asking].confirm}
							</Button>
						</>
					}
				/>
			)}
		</section>
	);
}

/** What each change asks before it is made. A reset's description says the link's limit, so it is made there. */
const QUESTIONS: Record<Asking, { title: string; description: string; confirm: string }> = {
	remove: {
		title: "Remove the public link?",
		description: "Nobody can open it any more, and it cannot be brought back. A new link would get a new address.",
		confirm: "Remove link",
	},
	replace: {
		title: "Make a new public link?",
		description: "The address you sent stops working for good. Only the new address opens it.",
		confirm: "Make a new link",
	},
	show: {
		title: "Let people with the link see this folder?",
		description:
			"Everyone who has the address can then open and download everything in this folder, including what others sent through it. The address stays the same.",
		confirm: "Let them see it",
	},
	reset: { title: "Make room for more files?", description: "", confirm: "Make room" },
};

/** The choice of what a folder's public link lets people do. */
function KindChoice({
	name,
	value,
	disabled,
	onChange,
}: {
	name: string;
	value: LinkKind;
	disabled: boolean;
	onChange: (kind: LinkKind) => void;
}) {
	return (
		<fieldset className="mt-3" disabled={disabled}>
			<legend className="text-sm font-medium">People with the link can</legend>
			{(["view", "upload-only"] as const).map((k) => (
				<label key={k} className="flex cursor-pointer gap-2.5 rounded-md py-1.5 text-sm">
					<input
						type="radio"
						name={name}
						value={k}
						checked={value === k}
						onChange={() => onChange(k)}
						className="mt-0.5 size-4 shrink-0 accent-foreground"
					/>
					<span>
						<span className="font-medium">{LINK_KIND_MEANS[k].name}</span>
						<span className="block text-muted-foreground">{LINK_KIND_MEANS[k].means}</span>
					</span>
				</label>
			))}
		</fieldset>
	);
}

/** How much an upload-only link has taken in, against its limit. */
function TakenIn({ takenIn, limit }: { takenIn: number; limit: number }) {
	const share = limit > 0 ? Math.min(1, takenIn / limit) : 1;
	const words = `${takenIn === 0 ? "Nothing yet" : formatSize(takenIn)} of ${formatCapacity(limit)}`;
	return (
		<>
			<span>{words}</span>
			<div
				role="meter"
				aria-label="Taken in through the link"
				aria-valuemin={0}
				aria-valuemax={limit}
				aria-valuenow={Math.min(takenIn, limit)}
				aria-valuetext={words}
				className="mt-1 h-1.5 overflow-hidden rounded-full bg-muted"
			>
				<div
					className={`h-full ${share >= 1 ? "bg-attention" : "bg-foreground/60"}`}
					style={{ width: `${Math.max(share * 100, takenIn > 0 ? 1 : 0)}%` }}
				/>
			</div>
		</>
	);
}
