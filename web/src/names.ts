// What the Drive says about a name before sending anything, so a person is not made to wait for a whole file to
// travel only to be refused. The Drive checks again and its answer is the one that counts (internal/drive/files.go);
// the messages here are the same as its own.

/** The longest name the Drive stores, in bytes of UTF-8. */
export const MAX_NAME_BYTES = 255;

export const TOO_LONG =
	"This name is too long. Names can be up to 255 characters, and fewer when they have accented letters, " +
	"other alphabets or symbols. Shorten the name and try again.";

const encoder = new TextEncoder();

/** Why a file of this name cannot be uploaded where these items are, or undefined when it can. */
export function uploadProblem(name: string, here: { name: string }[] | undefined): string | undefined {
	if (encoder.encode(name).length > MAX_NAME_BYTES) return TOO_LONG;
	if (here?.some((item) => item.name === name)) {
		return `There is already something called “${name}” here, so this file was not uploaded. Rename it and try again.`;
	}
	return undefined;
}
