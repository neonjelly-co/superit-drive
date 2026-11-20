import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Copy, Link2 } from "lucide-react";
import { useId, useState } from "react";
import { nameOf } from "../access";
import { api, type PublicLink } from "../api";
import { formatChangedFull } from "../format";
import { Button } from "./button";
import { Dialog } from "./dialog";

type Props = {
	placeId: number;
	itemId: number;
	what: "file" | "folder";
	/** True when the person may make a link here (not where sharing outside the project is off). */
	canMake: boolean;
	/** Called after a link was made or removed, so the list and "who can see this" follow it. */
	onChanged: () => void;
};

/**
 * The addresses of the links made in this tab, until the page is reloaded. The Drive keeps only a fingerprint of a
 * link's address and never shows it again, so it is shown here, to the person who made it, while they are on the page.
 */
const madeHere = new Map<string, string>();
const keyOf = (placeId: number, itemId: number, link: PublicLink) => `${placeId}/${itemId}/${link.made}`;

/**
 * "Public link", in the details panel, for whoever may share an item: making a link anyone can open without signing
 * in, its address (shown once, when it is made), who made it and how often it was used, and removing it or making a new
 * one in its place.
 */
export function PublicLinkSection({ placeId, itemId, what, canMake, onChanged }: Props) {
	const queryClient = useQueryClient();
	const key = ["link", placeId, itemId];
	const link = useQuery({ queryKey: key, queryFn: () => api.link(placeId, itemId) });
	const [busy, setBusy] = useState(false);
	const [problem, setProblem] = useState<string>();
	const [asking, setAsking] = useState<"remove" | "replace">();
	const [copied, setCopied] = useState(false);
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
	const make = (replace: boolean) => change(() => api.makeLink(placeId, itemId, replace));
	const remove = () => change(() => api.removeLink(placeId, itemId).then(() => null));

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
						Anyone with the link can open and download {reaches}, without signing in, until the link is removed.
					</p>
					{canMake ? (
						<Button className="mt-3" disabled={busy} onClick={() => void make(false)}>
							<Link2 aria-hidden /> Make a public link
						</Button>
					) : (
						<p className="mt-2 text-sm text-muted-foreground">Sharing outside the project is switched off here.</p>
					)}
				</>
			) : (
				<>
					<p className="mt-0.5 text-sm text-muted-foreground">
						Anyone with the link can open and download {reaches}, without signing in.
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
					</dl>
					{!current.works && (
						<p className="mt-2 text-sm text-attention">
							It opens nothing at the moment: whoever made it can no longer share this, or sharing outside the project
							is switched off here. Remove it if it is no longer needed.
						</p>
					)}
					<div className="mt-3 flex flex-wrap gap-2">
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
			{asking && (
				<Dialog
					title={asking === "remove" ? "Remove the public link?" : "Make a new public link?"}
					description={
						asking === "remove"
							? "Nobody can open it any more, and it cannot be brought back. A new link would get a new address."
							: "The address you sent stops working for good. Only the new address opens it."
					}
					onClose={() => setAsking(undefined)}
					onSubmit={() => void (asking === "remove" ? remove() : make(true))}
					footer={
						<>
							<Button onClick={() => setAsking(undefined)}>Cancel</Button>
							<Button type="submit" variant={asking === "remove" ? "danger" : "primary"} disabled={busy}>
								{asking === "remove" ? "Remove link" : "Make a new link"}
							</Button>
						</>
					}
				/>
			)}
		</section>
	);
}
