package server

import (
	"encoding/json"
	"strings"

	"github.com/neonjelly-co/superit-drive/internal/text"
)

// Every text the server writes for a person to read, in English and Hungarian: the answers of the API (members' and
// guests'), the visitors' pages and the send page's words, and the Drive's own mail. Each is said in the language of
// the request it answers (text.Of); a mail in the language of the request whose act sends it. Hungarian is phrased so
// that a name or a number put into it needs no suffix and no article: it comes after a colon, in quotes („…”), or as
// the subject of the sentence. The log, which only the people running the Drive read, stays in English.

// Anyone.
var (
	// notHere is the one answer for a place, an item or an item in the trash that is not there, or that the person may
	// not do what they asked with.
	notHere = text.M("This is not in the Drive, or you have no access to it.",
		"Ez nincs a Drive-ban, vagy nincs hozzáférésed.")
	refusedOrigin = text.M("This change did not come from the Drive's own page, so it was refused.",
		"Ez a módosítás nem a Drive saját oldaláról érkezett, ezért a Drive nem fogadta el.")
	wentWrong = text.M("Something went wrong in the Drive. Try again in a moment.",
		"Valami hiba történt a Drive-ban. Próbáld újra egy kis idő múlva.")
	nothingHere = text.M("There is nothing at this address.", "Ezen a címen nincs semmi.")
	unreadable  = text.M("The Drive could not read this request.", "A Drive nem tudta értelmezni ezt a kérést.")
	noInterface = text.M("The interface was not built into this Drive. Build it with `make build`.",
		"Ebbe a Drive-ba nem került bele a felület. Építsd meg a `make build` paranccsal.")
)

// Members signing in through the reverse proxy (identity.go).
var (
	notThroughSignIn = text.M("This request did not come through the Drive's sign-in.",
		"Ez a kérés nem a Drive bejelentkezésén keresztül érkezett.")
	noOneSignedIn = text.M("This request does not say who is signed in.",
		"Ebből a kérésből nem derül ki, ki van bejelentkezve.")
	notAMember = text.M("You are not a member of this project, so its Drive is closed to you.",
		"Nem vagy tagja ennek a projektnek, ezért a Drive-ja zárva van előtted.")
)

// Files and folders.
var (
	notAFolderToList = text.M("This is a file, not a folder.", "Ez fájl, nem mappa.")
	notAFolderToMake = text.M("A folder can only be made inside a folder.", "Mappát csak mappán belül lehet létrehozni.")
	takenHere        = text.M("There is already something called “%s” here. Choose another name.",
		"Itt már van valami ezzel a névvel: „%s”. Válassz másik nevet.")
	takenThere = text.M("There is already something called “%s” in that folder. Rename one of them first.",
		"Abban a mappában már van valami ezzel a névvel: „%s”. Előbb nevezd át az egyiket.")
	beingChangedInside = text.M("“%s”, or something in it, is being uploaded or changed right now. Try again when that is done.",
		"Épp feltöltés vagy módosítás folyik rajta vagy benne: „%s”. Próbáld újra, ha befejeződött.")
	intoItself = text.M("A folder cannot be moved into itself, or into a folder inside it.",
		"Egy mappát nem lehet önmagába, sem egy benne lévő mappába áthelyezni.")
	onlyIntoAFolder = text.M("Things can only be moved into a folder.", "Csak mappába lehet áthelyezni.")
	tooDeep         = text.M("Folders can go at most %d deep.", "A mappák legfeljebb %d szint mélyre mehetnek.")
	goneFromStorage = text.M("This is no longer on the Drive storage. Reload the page to see what is there now.",
		"Ez már nincs a Drive-tárhelyen. Töltsd újra az oldalt, hogy lásd, mi van ott most.")
	storageFull  = text.M("The Drive storage is full.", "A Drive-tárhely megtelt.")
	cannotGoBack = text.M("This cannot go back: the folder it was deleted from is gone, or you can no longer add to it.",
		"Ez nem kerülhet vissza: a mappa, ahonnan törölték, már nincs meg, vagy már nem tehetsz bele semmit.")
	goneFromTrash = text.M("This is no longer in the trash on the Drive storage.",
		"Ez már nincs a kukában a Drive-tárhelyen.")
	storageSilent = text.M("The Drive storage did not answer. Try again in a moment.",
		"A Drive-tárhely nem válaszolt. Próbáld újra egy kis idő múlva.")
	storageRefusedLogin = text.M("The Drive storage refused the Drive's login. The storage settings need checking.",
		"A Drive-tárhely nem fogadta el a Drive belépését. A tárhely beállításait ellenőrizni kell.")
	noSuchDestination = text.M("The folder to move it into is not in the Drive, or you cannot add to it. Reload the page and try again.",
		"A célmappa nincs a Drive-ban, vagy nem tehetsz bele semmit. Töltsd újra az oldalt, és próbáld újra.")
	notBetweenPlaces = text.M("Only what is in your own My files can be moved to another place, and only into a shared folder, "+
		"where it becomes the project's. Anything else is moved within its own place.",
		"Más helyre csak azt lehet áthelyezni, ami a Saját fájljaidban van, és csak egy közös mappába, ahol a projekté lesz. "+
			"Minden mást a saját helyén belül lehet áthelyezni.")
	searchTooLong = text.M("This is longer than any name can be. Search for a part of the name.",
		"Ez hosszabb, mint amilyen egy név lehet. Keress a név egy részére.")
	searchNothing = text.M("Type a part of a name to search for.", "Írd be egy név egy részét, hogy rákeress.")
	searchBusy    = text.M("The Drive is busy with other searches. Try again in a moment.",
		"A Drive most más keresésekkel van elfoglalva. Próbáld újra egy kis idő múlva.")
	searchTookTooLong = text.M("This search took too long, so the Drive ended it. Try again in a moment.",
		"Ez a keresés túl sokáig tartott, ezért a Drive leállította. Próbáld újra egy kis idő múlva.")
)

// Shared folders.
var (
	sharedFolderTaken = text.M("There is already a shared folder called “%s”. Choose another name.",
		"Már van közös mappa ezzel a névvel: „%s”. Válassz másik nevet.")
	sharedFolderBusy = text.M("Something in “%s” is being uploaded or changed right now. Try again when that is done.",
		"Épp feltöltés vagy módosítás folyik ebben: „%s”. Próbáld újra, ha befejeződött.")
	sharedFolderChanging = text.M("“%s” is being changed right now. Try again in a moment.",
		"Épp módosítás folyik ezen: „%s”. Próbáld újra egy kis idő múlva.")
	sharedFolderBlocked = text.M("Something called “%s” was put where this shared folder goes back to on the Drive storage, "+
		"so it cannot go back. Move that away on the storage first.",
		"A Drive-tárhelyen oda, ahová ez a közös mappa visszakerülne, valami más került ezzel a névvel: „%s”, ezért nem "+
			"kerülhet vissza. Előbb vidd el onnan a tárhelyen.")
)

// Giving access, and inviting guests.
var (
	notToYourself = text.M("You cannot give yourself access. Ask someone who may share it.",
		"Saját magadnak nem adhatsz hozzáférést. Kérd meg valakit, aki megoszthatja.")
	theirOwnFiles = text.M("This is in their own My files: they have it already.",
		"Ez az ő Saját fájljaiban van, így már hozzáfér.")
	notInTheProject = text.M("This person is not a member of the project.", "Ez a személy nem tagja a projektnek.")
	needsAManager   = text.M("A shared folder needs a manager. Make someone else a manager first.",
		"Egy közös mappának kell kezelő. Előbb tegyél meg valaki mást kezelőnek.")
	rolesOnAnItem = text.M("Access to a file or folder is given as viewer or editor.",
		"Fájlhoz vagy mappához megtekintőként vagy szerkesztőként lehet hozzáférést adni.")
	rolesForEveryone = text.M("Everyone in the project can be given viewer or editor on a shared folder, not manager.",
		"A projekt minden tagja megtekintő vagy szerkesztő lehet egy közös mappában, kezelő nem.")
	rolesOnASharedFolder = text.M("Access to a shared folder is given as viewer, editor or manager.",
		"Közös mappához megtekintőként, szerkesztőként vagy kezelőként lehet hozzáférést adni.")
	notAnEmail = text.M("This is not an email address the Drive can send mail to. Check it, and try again.",
		"Erre a címre a Drive nem tud e-mailt küldeni. Ellenőrizd, és próbáld újra.")
	guestRoles = text.M("A guest is given a file or folder as viewer or editor.",
		"Vendég megtekintőként vagy szerkesztőként kaphat meg egy fájlt vagy mappát.")
	endHasPassed = text.M("The end date has passed. Choose one that is still to come, or none.",
		"Ez a nap már elmúlt. Válassz egy későbbit, vagy ne adj meg semmit.")
	// cannotInvite says why guests cannot be invited, after why mail does not work (%s).
	cannotInvite = text.M("Inviting guests is unavailable: the Drive mails each guest an invitation and the links they sign in with. "+
		"%s Make a public link instead.",
		"Vendéget most nem lehet meghívni: a Drive minden vendégnek e-mailben küldi a meghívót és a belépési linkeket. "+
			"%s Készíts helyette nyilvános linket.")
	// tooManyInvitations is for a member who had as many invitations mailed in an hour as one may (%d).
	tooManyInvitations = text.M("You have invited guests %d times within the last hour, and the Drive mails each invitation. "+
		"That is as many as it sends for one person in an hour. Try again later.",
		"Az elmúlt egy órában %d alkalommal hívtál meg vendéget, és a Drive minden meghívót e-mailben küld el. "+
			"Egy embernek óránként ennyit küld. Próbáld újra később.")
)

// Public links (links.go), and sharing outside the project switched off (oversight.go).
var (
	linkKinds = text.M("A public link is either “view” or “upload-only”.",
		"A nyilvános link vagy „view”, vagy „upload-only”.")
	fileLinkKind = text.M("Only a folder's link can be upload-only. A file's link lets people open and download it.",
		"Feltöltési link csak mappára készülhet. Egy fájl linkjével megnyitni és letölteni lehet.")
	linkExists = text.M("This already has a public link. Make a new one in its place to change its address.",
		"Ennek már van nyilvános linkje. Ha új címet szeretnél, készíts újat a helyére.")
	noLinkYet = text.M("This has no public link. Make one first.",
		"Ennek nincs nyilvános linkje. Előbb készíts egyet.")
	offForThisFolder = text.M("Sharing outside the project is switched off for this shared folder, so no public link can be made "+
		"and no guest invited here. Its managers can switch it back on.",
		"Ebben a közös mappában ki van kapcsolva a projekten kívüli megosztás, ezért itt nem lehet nyilvános linket készíteni "+
			"és vendéget meghívni. A mappa kezelői visszakapcsolhatják.")
	offForTheProject = text.M("Sharing outside the project is switched off for the whole project, so no public link can be made "+
		"and no guest invited. A project admin can switch it back on.",
		"A projekten kívüli megosztás az egész projektben ki van kapcsolva, ezért nem lehet nyilvános linket készíteni és "+
			"vendéget meghívni. Egy projektadmin visszakapcsolhatja.")
	sayOnOrOff = text.M(`Say whether sharing outside the project is on: {"on": true} or {"on": false}.`,
		`Add meg, hogy be van-e kapcsolva a projekten kívüli megosztás: {"on": true} vagy {"on": false}.`)
	// guestUnavailable is what a guest is told about something given to them while sharing outside the project is off
	// where it is (%s: the project's name).
	guestUnavailable = text.M("This is not available right now: %s has switched off sharing outside the project. It works again "+
		"once they switch it back on. Ask whoever invited you.",
		"Ez most nem érhető el: %s kikapcsolta a projekten kívüli megosztást. Újra működik, amint visszakapcsolják. "+
			"Kérdezd meg azt, aki meghívott.")
)

// The held My files of people who left (held.go).
var (
	chooseWhom = text.M("Choose a member of the project to hand the files to.",
		"Válaszd ki, melyik tag kapja meg a fájlokat.")
	theirsAgain = text.M("The person these files belong to is a member of the project again: the files are theirs, as they were.",
		"Ezeknek a fájloknak a gazdája újra tagja a projektnek: a fájlok ismét az övéi, ahogy voltak.")
	actedAlready = text.M("These files, or the My files they were to go to, are being handed over or deleted already. "+
		"Try again once that is done.",
		"Ezeket a fájlokat, vagy azt a Saját fájlokat, ahová kerültek volna, már épp átadják vagy törlik. "+
			"Próbáld újra, ha ez befejeződött.")
	stillChanging = text.M("Something in these files is still being changed. Try again in a moment.",
		"Valami még változik ezekben a fájlokban. Próbáld újra egy kis idő múlva.")
)

// Moving bytes: downloads, zips and uploads (transfers.go).
var (
	busyMessage = text.M("The Drive is busy with other uploads and downloads. This one waits its turn.",
		"A Drive épp más feltöltésekkel és letöltésekkel foglalkozik. Ez kivárja a sorát.")
	restartMessage = text.M("The Drive storage cannot continue an upload where it stopped, so this upload starts again from the beginning.",
		"A Drive-tárhely nem tudja ott folytatni a feltöltést, ahol abbamaradt, ezért ez a feltöltés elölről kezdődik.")
	onlyFoldersZip = text.M("Only a folder downloads as a zip. Download the file itself instead.",
		"ZIP-fájlként csak mappát lehet letölteni. Ezt a fájlt töltsd le önmagában.")
	onlyIntoFolders = text.M("Files can only be uploaded into a folder.", "Fájlt csak mappába lehet feltölteni.")
	aFolderToZip    = text.M("This is a folder. Download it as a zip instead.", "Ez mappa. Töltsd le inkább ZIP-fájlként.")
	aFolderToOpen   = text.M("This is a folder. Open it in the list instead.", "Ez mappa. Nyisd meg inkább a listában.")
	pastTheEnd      = text.M("This part of the file starts after its end.", "A fájlnak ez a része a vége után kezdődne.")
	fileGone        = text.M("This file is no longer on the Drive storage. Reload the page to see what is there now.",
		"Ez a fájl már nincs a Drive-tárhelyen. Töltsd újra az oldalt, hogy lásd, mi van ott most.")
	tooManyForAZip = text.M("This holds more than %d files and folders, too many for one zip. Download the folders inside it one at a time.",
		"Ebben több mint %d fájl és mappa van, ez túl sok egy ZIP-fájlhoz. Töltsd le egyenként a benne lévő mappákat.")
	folderGoneGoUp = text.M("This folder is no longer on the Drive storage. Go up a level to see what is there now.",
		"Ez a mappa már nincs a Drive-tárhelyen. Lépj egy szinttel feljebb, hogy lásd, mi van ott most.")
	folderGoneReload = text.M("This folder is no longer on the Drive storage. Reload the page and try again.",
		"Ez a mappa már nincs a Drive-tárhelyen. Töltsd újra az oldalt, és próbáld újra.")
	uploadNoLength = text.M("The upload did not say how large the file is, so nothing was stored. Try again.",
		"A feltöltés nem jelezte a fájl méretét, ezért semmi nem lett eltárolva. Próbáld újra.")
	uploadNoSize = text.M("The upload did not say how large the file is.", "A feltöltés nem jelezte a fájl méretét.")
	uploadCutOff = text.M("The upload stopped before the whole file arrived, so nothing was stored. Try again.",
		"A feltöltés megszakadt, mielőtt az egész fájl megérkezett, ezért semmi nem lett eltárolva. Próbáld újra.")
	uploadExisting = text.M("The upload asked for something the Drive does not do with a name that is taken.",
		"A feltöltés egy foglalt névvel olyasmit kért, amit a Drive nem csinál.")
	fileOverFolder = text.M("There is a folder called “%s” here, and a file cannot replace a folder. Keep both instead.",
		"Itt van egy mappa ezzel a névvel: „%s”, és fájl nem léphet egy mappa helyére. Tartsd meg inkább mindkettőt.")
	folderTaken  = text.M("There is already a folder called “%s” here.", "Itt már van egy mappa ezzel a névvel: „%s”.")
	fileTaken    = text.M("There is already a file called “%s” here.", "Itt már van egy fájl ezzel a névvel: „%s”.")
	beingChanged = text.M("“%s” is being uploaded or changed right now. Try again when that is done.",
		"Épp feltöltés vagy módosítás folyik rajta: „%s”. Próbáld újra, ha befejeződött.")
	fullNotUploaded = text.M("The Drive storage is full, so this file was not uploaded.",
		"A Drive-tárhely megtelt, ezért ez a fájl nem töltődött fel.")
	partNoLength = text.M("The part did not say how large it is, so nothing was stored. Try again.",
		"A rész nem jelezte a méretét, ezért semmi nem lett eltárolva. Próbáld újra.")
	partDoesNotFit = text.M("This part does not fit the file.", "Ez a rész nem illik a fájlhoz.")
	partOnItsWay   = text.M("Another part of this upload is still on its way. Ask where it stands, and go on from there.",
		"A feltöltés egy másik része még úton van. Kérdezd le, hol tart, és onnan folytasd.")
	partCutOff = text.M("The part stopped before it all arrived. Ask where the upload stands, and go on from there.",
		"A rész megszakadt, mielőtt teljesen megérkezett. Kérdezd le, hol tart a feltöltés, és onnan folytasd.")
	partNotNext = text.M("This part does not follow on from what has arrived.",
		"Ez a rész nem ott kezdődik, ahol a már megérkezett vége.")
	notAllArrived = text.M("Not all of the file has arrived yet.", "A fájl még nem érkezett meg teljesen.")
)

// Guests (guests.go).
var (
	codeAsked = text.M("If anything on this Drive is shared with this address, a sign-in link is on its way to it. It works once, for 30 minutes. "+
		"If one was sent to it in the last few minutes, use that one: no other is sent so soon.",
		"Ha ezen a Drive-on bármi meg van osztva ezzel a címmel, egy belépési link már úton van rá. Egyszer használható, 30 percig. "+
			"Ha az elmúlt néhány percben már ment rá link, azt használd: ilyen hamar nem megy újabb.")
	wrongCode = text.M("This sign-in link no longer works: it was used already, or it is more than 30 minutes old. Ask for a new one.",
		"Ez a belépési link már nem érvényes: egyszer már használták, vagy több mint 30 perces. Kérj újat.")
	signInFirst = text.M("Sign in to see this.", "Jelentkezz be, hogy ezt lásd.")
)

// Visitors with a public link: the answers, and the words of their pages (visitors.go, sendin.go).
var (
	// visitorNotHere is the one answer for a link that does not work, and for anything a link does not cover.
	visitorNotHere = text.M("This link does not work. It may have been removed, or its address may be incomplete. "+
		"Ask whoever sent it to you for a new one.",
		"Ez a link nem működik. Lehet, hogy törölték, vagy hiányos a címe. Kérj újat attól, aki küldte.")
	onlyReading = text.M("A public link only opens and downloads: nothing can be changed through one.",
		"Nyilvános linkkel csak megnyitni és letölteni lehet, módosítani semmit.")
	visitorsBusy = text.M("The Drive is busy. Try again in a moment.",
		"A Drive most túlterhelt. Próbáld újra egy kis idő múlva.")
	// visitorUnavailable is a visitor's page for a link that would open, while sharing outside the project is off.
	visitorUnavailable = text.M("Sharing outside the organisation that shared this is switched off for now, so the link opens "+
		"nothing. It works again once they switch it back on. Ask whoever sent it to you.",
		"A megosztó szervezet most kikapcsolta a szervezeten kívüli megosztást, ezért a link semmit nem nyit meg. "+
			"Újra működik, amint visszakapcsolják. Kérdezd meg azt, aki küldte.")
	linkDoesNotWork  = text.M("This link does not work", "Ez a link nem működik")
	didNotWork       = text.M("This did not work", "Ez nem sikerült")
	linkNotAvailable = text.M("This link is not available", "Ez a link nem érhető el")
	sharedBy         = text.M("Shared by %s", "Megosztotta: %s")
	sharedFrom       = text.M("Shared from %s", "Megosztva innen: %s")
	onlyTheFirst     = text.M("Only the first %d items are listed here. Download all as zip to get everything.",
		"Itt csak az első %d elem látszik. Az összeset egyben, ZIP-fájlként töltheted le.")

	// linkFull is the page, and the answer, of an upload-only link that has taken in all it may until it is reset.
	linkFull = text.M("This link has taken in all it can for now. Ask whoever sent it to you to make room for more.",
		"Ez a link most nem fogad több fájlt. Kérd meg azt, aki küldte, hogy engedjen még fájlokat.")
	linkTooSmall = text.M("This file is larger than this link can still take in. Smaller files may still fit.",
		"Ez a fájl nagyobb annál, amennyit ez a link még fogadhat. Kisebb fájlok még beférhetnek.")
	sendCutOff = text.M("The file stopped before it all arrived, so nothing was kept. Send it again.",
		"A fájl megszakadt, mielőtt teljesen megérkezett, ezért semmi nem maradt meg belőle. Küldd el újra.")
	sendNoLength = text.M("The file did not say how large it is, so nothing was kept. Send it again.",
		"A fájl nem jelezte a méretét, ezért semmi nem maradt meg belőle. Küldd el újra.")
	sendFolderNow = text.M("The folder changed while the file arrived, so nothing was kept. Send it again.",
		"A mappa megváltozott, amíg a fájl érkezett, ezért semmi nem maradt meg belőle. Küldd el újra.")
	sendNoRoom = text.M("There is no room for more files at the moment.", "Most nincs hely több fájlnak.")
)

// visitorWords are the words of a visitor's page in one language, made once for each (wordsOf).
type visitorWords struct {
	Folders, DownloadAll, Open, Download, Empty                        string
	SendIntro, DropOrChoose, ChooseFiles, FilesOnly, Send, NeedsScript string
	// Other is the switch to the other language, in that language, and OtherCode that language's tag.
	Other, OtherCode string
}

var visitorWordsOf = func() map[text.Lang]visitorWords {
	folders := text.M("Folders", "Mappák")
	downloadAll := text.M("Download all as zip", "Az összes letöltése ZIP-fájlként")
	open := text.M("Open", "Megnyitás")
	download := text.M("Download", "Letöltés")
	empty := text.M("This folder is empty.", "Ez a mappa üres.")
	intro := text.M("Send files to this folder. What is in it stays hidden: you cannot see its files, their names, or what anyone "+
		"else sent. Nothing you send replaces anything in it.",
		"Ebbe a mappába küldhetsz fájlokat. A tartalma rejtve marad: nem látod a benne lévő fájlokat, a nevüket, sem azt, "+
			"amit mások küldtek. Amit küldesz, semmit nem ír felül benne.")
	dropOrChoose := text.M("Drop files here, or choose them", "Húzd ide a fájlokat, vagy válaszd ki őket")
	chooseFiles := text.M("Choose files", "Fájlok kiválasztása")
	filesOnly := text.M("Files only: send what is in a folder, not the folder.",
		"Csak fájlokat: egy mappából a benne lévő fájlokat küldd, ne magát a mappát.")
	send := text.M("Send", "Küldés")
	needsScript := text.M("Sending files needs JavaScript, which is switched off in this browser. Switch it on, then reload this page.",
		"A fájlok küldéséhez JavaScript kell, ebben a böngészőben viszont ki van kapcsolva. Kapcsold be, aztán töltsd újra az oldalt.")
	// The switch to the other language, in that language.
	other := text.M("Magyarul", "In English")
	out := map[text.Lang]visitorWords{}
	for _, l := range text.Langs {
		otherCode := text.Hungarian.Code()
		if l == text.Hungarian {
			otherCode = text.English.Code()
		}
		out[l] = visitorWords{Folders: folders.In(l), DownloadAll: downloadAll.In(l), Open: open.In(l), Download: download.In(l),
			Empty: empty.In(l), SendIntro: intro.In(l), DropOrChoose: dropOrChoose.In(l), ChooseFiles: chooseFiles.In(l),
			FilesOnly: filesOnly.In(l), Send: send.In(l), NeedsScript: needsScript.In(l), Other: other.In(l), OtherCode: otherCode}
	}
	return out
}()

// sendWords are what the send page's script says, in one language: given to it in the page (data-words), so that one
// script, allowed by one hash, serves every language. "{n}" stands for a number.
var sendWordsOf = func() map[text.Lang]string {
	words := map[string]text.Message{
		"send":      text.M("Send", "Küldés"),
		"sendN":     text.M("Send %s files", "%s fájl küldése"),
		"gone":      text.M("This link does not work any more. Ask whoever sent it to you for a new one.", "Ez a link már nem működik. Kérj újat attól, aki küldte."),
		"off":       text.M("This link is not available at the moment. Try again later.", "Ez a link most nem érhető el. Próbáld újra később."),
		"folders":   text.M("Folders cannot be sent here. Send the files in them instead.", "Mappát ide nem lehet küldeni. Küldd el inkább a benne lévő fájlokat."),
		"sentOne":   text.M("The file was sent.", "A fájl elment."),
		"sentAll":   text.M("All %s files were sent.", "Minden fájl elment, összesen %s."),
		"failedOne": text.M("One file was not sent.", "Egy fájl nem ment el."),
		"failedN":   text.M("%s files were not sent.", "Nem ment el: %s fájl."),
		"again":     text.M("Send again to try once more.", "A Küldés gombbal újra megpróbálhatod."),
		"sent":      text.M("Sent", "Elküldve"),
		"waiting":   text.M("Waiting for its turn", "Sorára vár"),
		"notSent":   text.M("Not sent", "Nem ment el"),
		"failed":    text.M("This file was not sent.", "Ez a fájl nem ment el."),
		"retrying":  text.M("Connection lost: trying again", "Megszakadt a kapcsolat, újrapróbálkozás"),
		"stopped":   text.M("Stopped before it all arrived", "Megszakadt, mielőtt teljesen megérkezett"),
	}
	out := map[text.Lang]string{}
	for _, l := range text.Langs {
		said := map[string]string{"decimal": map[text.Lang]string{text.English: ".", text.Hungarian: ","}[l]}
		for k, m := range words {
			said[k] = m.In(l)
			if strings.Contains(said[k], "%s") {
				said[k] = m.In(l, "{n}")
			}
		}
		out[l] = jsonOf(said)
	}
	return out
}()

// The Drive's own mail: "shared with you" to a member, an invitation to a guest, and a guest's sign-in link.
var (
	sharedFileWithYou = text.M("%s shared the file “%s” with you in the Drive of %s.",
		"%s megosztott veled egy fájlt: „%s”. Projekt: %s.")
	sharedFolderWithYou = text.M("%s shared the folder “%s” with you in the Drive of %s.",
		"%s megosztott veled egy mappát: „%s”. Projekt: %s.")
	sharedSharedFolderWithYou = text.M("%s shared the shared folder “%s” with you in the Drive of %s.",
		"%s megosztott veled egy közös mappát: „%s”. Projekt: %s.")
	sharedSubject = text.M("%s shared “%s” with you", "%s megosztott veled valamit: „%s”")
	canOpen       = text.M("You can open and download it.", "Megnyithatod és letöltheted.")
	canEdit       = text.M("As an editor you can also upload, rename, move and delete, and share it further.",
		"Szerkesztőként fel is tölthetsz, átnevezhetsz, áthelyezhetsz és törölhetsz, és tovább is megoszthatod.")
	canManage = text.M("As a manager you can also decide who has access to it, and delete it.",
		"Kezelőként te döntöd el azt is, ki férhet hozzá, és törölheted is.")
	openIt = text.M("Open it: %s", "Megnyitás: %s")

	invitedToFile = text.M("%s invited you to the file “%s” in the Drive of %s.",
		"%s meghívott a Drive-ba, és megosztott veled egy fájlt: „%s”. Projekt: %s.")
	invitedToFolder = text.M("%s invited you to the folder “%s” in the Drive of %s.",
		"%s meghívott a Drive-ba, és megosztott veled egy mappát: „%s”. Projekt: %s.")
	invitedSubject = text.M("%s invited you to “%s”", "%s megosztott veled valamit: „%s”")
	guestCanEdit   = text.M("As an editor you can also upload, make folders, rename, move and delete inside it.",
		"Szerkesztőként fel is tölthetsz bele, mappákat hozhatsz létre benne, és átnevezhetsz, áthelyezhetsz és törölhetsz benne.")
	accessUntil   = text.M("You have access until %s.", "A hozzáférésed eddig tart: %s.")
	confirmByCode = text.M("You will be asked to confirm your address: we mail it a link that signs you in. You need no account.",
		"A címedet meg kell majd erősítened: e-mailben küldünk rá egy linket, amellyel beléphetsz. Fiók nem kell hozzá.")

	codeSubject = text.M("Sign in to the Drive of %s", "Belépés a Drive-ba (%s)")
	codeBody    = text.M("Open this link to sign in:\n\n%s\n\nIt works once, for 30 minutes, and signs in the browser you open it in.\n\n"+
		"If you did not ask for it, you can ignore this mail: nobody can sign in without the link.\n",
		"Ezzel a linkkel léphetsz be:\n\n%s\n\nEgyszer használható, 30 percig, és abban a böngészőben léptet be, amelyikben megnyitod.\n\n"+
			"Ha nem te kérted, nyugodtan hagyd figyelmen kívül ezt az e-mailt: a link nélkül senki nem tud belépni.\n")
)

// jsonOf is words as one JSON object, for the send page's data-words.
func jsonOf(words map[string]string) string {
	b, err := json.Marshal(words)
	if err != nil {
		panic("server: the send page's words: " + err.Error())
	}
	return string(b)
}
