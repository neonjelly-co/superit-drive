package server

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/neonjelly-co/superit-drive/internal/members"
	"github.com/neonjelly-co/superit-drive/internal/text"
)

// The headers a signed-in request carries. The reverse proxy in front of the Drive signs people in, removes any copies
// a browser sent, and adds these.
//
// The Drive identifies the person by UserIDHeader alone and takes their email address, display name and project role
// from the member list, which is the authority. The gateway also sends Remote-Email, Remote-Name and Remote-Role; the
// Drive does not need them. (Using the list also sidesteps display names that are not valid in a header.)
const (
	// SecretHeader carries the secret the reverse proxy adds to every signed-in request (DRIVE_HEADER_SECRET).
	SecretHeader = "Drive-Secret"
	// UserIDHeader carries the member's stable user id from the platform.
	UserIDHeader = "Remote-User-Id"
)

// refusal is why a request was refused before anything else happened.
type refusal struct {
	status  int
	message text.Message
}

var (
	refusedSecret = refusal{http.StatusForbidden, notThroughSignIn}
	refusedNoUser = refusal{http.StatusForbidden, noOneSignedIn}
	refusedMember = refusal{http.StatusForbidden, notAMember}
)

// identify says who is asking. A request without the right secret is refused whatever its identity headers say: only
// the reverse proxy knows the secret, so only it can vouch for them.
func (s *Server) identify(r *http.Request) (members.Member, *refusal) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get(SecretHeader)), []byte(s.secret)) != 1 {
		return members.Member{}, &refusedSecret
	}
	userID := strings.TrimSpace(r.Header.Get(UserIDHeader))
	if userID == "" {
		return members.Member{}, &refusedNoUser
	}
	m, ok := s.members.Current().Lookup(userID)
	if !ok {
		return members.Member{}, &refusedMember
	}
	return m, nil
}
