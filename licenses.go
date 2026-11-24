// Package superitdrive holds the texts that go with every copy of the Drive: its own licence, and the notices of the
// software that is not its own and goes out with it. They are built into the binary, which prints them
// (`superit-drive licenses`).
package superitdrive

import _ "embed"

// License is the GNU Affero General Public License, version 3, which the Drive is published under.
//
//go:embed LICENSE
var License string

// ThirdPartyNotices are the copyright notices and licence texts of what is compiled into the Drive, sent to the
// browser with the interface, or carried in the image. `make notices` writes the file.
//
//go:embed THIRD-PARTY-NOTICES.txt
var ThirdPartyNotices string
