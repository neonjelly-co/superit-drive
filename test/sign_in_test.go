package test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/neonjelly-co/superit-drive/test/harness"
)

func TestAMemberWithTheSecretIsTreatedAsThatMember(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence)

	for _, m := range []harness.Member{reka, bence} {
		res := d.As(m).Get(t, "/api/me")
		if res.Status != http.StatusOK {
			t.Fatalf("%s: got %d %s", m.Email, res.Status, res.Body)
		}
		var got meJSON
		res.JSON(t, &got)
		want := meJSON{UserID: m.UserID, Email: m.Email, DisplayName: m.DisplayName, Role: m.Role, Project: "Kovács Accounting"}
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
	}
}

// Every address the Drive answers, and some it does not.
var everyAddress = []string{"/", "/favicon.svg", "/assets/index.js", "/api/me", "/api/places", "/api/places/1/items", "/settings", "/no/such/address"}

func TestARequestWithoutTheRightSecretIsRefusedWhateverItsIdentityHeadersSay(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)

	identity := http.Header{
		"Remote-User-Id": {reka.UserID},
		"Remote-Email":   {reka.Email},
		"Remote-Name":    {reka.DisplayName},
		"Remote-Role":    {reka.Role},
	}
	secrets := map[string][]string{
		"no secret":            nil,
		"an empty secret":      {""},
		"a wrong secret":       {strings.Repeat("0", len(d.Secret))},
		"the secret and more":  {d.Secret + "0"},
		"part of the secret":   {d.Secret[:len(d.Secret)-1]},
		"the secret uppercase": {strings.ToUpper(d.Secret)},
	}
	for name, secret := range secrets {
		h := identity.Clone()
		if secret != nil {
			h["Drive-Secret"] = secret
		}
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
			for _, path := range everyAddress {
				res := d.WithHeaders(h).Do(t, method, path, nil)
				if res.Status != http.StatusForbidden {
					t.Errorf("%s %s with %s: got %d, want 403", method, path, name, res.Status)
				}
				if strings.Contains(string(res.Body), reka.Email) {
					t.Errorf("%s %s with %s: the answer mentions the member", method, path, name)
				}
			}
		}
	}

	// Nothing happened on the storage either: Réka's "My files" was never made.
	if _, ok := d.Storage.List(t, "My files"); ok {
		t.Error("a refused request made folders on the storage")
	}
}

func TestAPersonNotInTheMemberListIsRefused(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)

	stranger := harness.Member{UserID: "u-stranger", Email: "stranger@example.com", DisplayName: "Stranger", Role: "admin"}
	// Someone using a member's email address with another user id is a stranger too: the user id is the identity.
	impostor := harness.Member{UserID: "u-impostor", Email: reka.Email, DisplayName: reka.DisplayName, Role: "admin"}

	for _, who := range []harness.Member{stranger, impostor} {
		for _, path := range everyAddress {
			if res := d.As(who).Get(t, path); res.Status != http.StatusForbidden {
				t.Errorf("%s as %s: got %d, want 403", path, who.UserID, res.Status)
			}
		}
	}

	// The secret alone, without a user id, is not a member either.
	noUser := d.WithHeaders(http.Header{"Drive-Secret": {d.Secret}, "Remote-Email": {reka.Email}})
	if res := noUser.Get(t, "/api/me"); res.Status != http.StatusForbidden {
		t.Errorf("without a user id: got %d, want 403", res.Status)
	}
}

func TestTheMemberListIsReReadWhenItChanges(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)

	if res := d.As(bence).Get(t, "/api/me"); res.Status != http.StatusForbidden {
		t.Fatalf("Bence is not a member yet: got %d", res.Status)
	}

	// Bence joins.
	d.SetMembers(t, reka, bence)
	harness.Eventually(t, "Bence is let in", func() bool { return d.As(bence).Get(t, "/api/me").Status == http.StatusOK })

	// Réka leaves.
	d.SetMembers(t, bence)
	harness.Eventually(t, "Réka is refused", func() bool { return d.As(reka).Get(t, "/api/me").Status == http.StatusForbidden })

	// Bence becomes an admin: the role comes from the list.
	promoted := bence
	promoted.Role = "admin"
	d.SetMembers(t, promoted)
	harness.Eventually(t, "Bence is an admin", func() bool {
		var me meJSON
		res := d.As(bence).Get(t, "/api/me")
		res.JSON(t, &me)
		return me.Role == "admin"
	})
}
