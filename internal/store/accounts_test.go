// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const issuer = "https://id.example.com"

func TestLinkIdentity(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		a, _ := s.CreateFirstAdmin(ctx, "alice", pw, t0)
		if _, ok, err := s.UserByIdentity(ctx, issuer, "sub-a"); ok || err != nil {
			t.Fatalf("unlinked: %v %v", ok, err)
		}
		if err := s.LinkIdentity(ctx, a.ID, issuer, "sub-a", "alice@example.com", t0); err != nil {
			t.Fatal(err)
		}
		// Linking again updates the email.
		if err := s.LinkIdentity(ctx, a.ID, issuer, "sub-a", "alice@example.net", t0.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		u, ok, err := s.UserByIdentity(ctx, issuer, "sub-a")
		if !ok || err != nil || u.ID != a.ID || !u.HasSSO {
			t.Fatalf("linked: %+v %v %v", u, ok, err)
		}
		ids, err := s.Identities(ctx, a.ID)
		if err != nil || len(ids) != 1 || ids[0].Email != "alice@example.net" || !ids[0].LinkedAt.Equal(t0) {
			t.Fatalf("identities: %+v %v", ids, err)
		}
		if err := s.UnlinkIdentities(ctx, 9999); !errors.Is(err, ErrNotFound) {
			t.Errorf("unlink unknown: %v", err)
		}
		if err := s.UnlinkIdentities(ctx, a.ID); err != nil {
			t.Fatal(err)
		}
		if _, ok, _ := s.UserByIdentity(ctx, issuer, "sub-a"); ok {
			t.Error("still linked")
		}
	})
}

func TestSSOUsers(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		a, _ := s.CreateFirstAdmin(ctx, "alice", pw, t0)
		// A new SSO user gets a free name based on the one asked for.
		b, err := s.CreateSSOUser(ctx, "ALICE", RoleViewer, issuer, "sub-b", "", t0)
		if err != nil || b.Username != "ALICE-2" || b.HasLocal || !b.HasSSO || b.IsAdmin() {
			t.Fatalf("sso user: %+v %v", b, err)
		}
		if err := s.LinkIdentity(ctx, a.ID, issuer, "sub-b", "", t0); err == nil || !strings.Contains(err.Error(), "ALICE-2") {
			t.Errorf("linked an identity of someone else: %v", err)
		}
		if err := s.UnlinkIdentities(ctx, b.ID); err == nil {
			t.Error("unlinked the only way in")
		}
		c, err := s.CreateSSOUser(ctx, " ", RoleAdmin, issuer, "sub-c", "", t0)
		if err != nil || c.Username != "user" || !c.IsAdmin() {
			t.Errorf("unnamed sso user: %+v %v", c, err)
		}
		long, err := s.CreateSSOUser(ctx, strings.Repeat("é", 100), RoleViewer, issuer, "sub-d", "", t0)
		if err != nil || len([]rune(long.Username)) != MaxUsernameLength-4 {
			t.Errorf("long sso name: %q %v", long.Username, err)
		}
		if _, err := s.CreateSSOUser(ctx, "x", "root", issuer, "sub-e", "", t0); err == nil {
			t.Error("unknown role")
		}
		// The identity is unique: the user is not created either.
		if _, err := s.CreateSSOUser(ctx, "dave", RoleViewer, issuer, "sub-c", "", t0); err == nil {
			t.Error("the same identity twice")
		}
		if _, err := s.UserByName(ctx, "dave"); !errors.Is(err, ErrNotFound) {
			t.Error("the failed sign-up left a user")
		}
		// Deleting a user removes their links.
		if err := s.DeleteUser(ctx, b.ID); err != nil {
			t.Fatal(err)
		}
		if _, ok, _ := s.UserByIdentity(ctx, issuer, "sub-b"); ok {
			t.Error("the link survived the delete")
		}
	})
}

func TestSSOUsernamesRunOut(t *testing.T) {
	s := openSQLite(t)
	ctx := context.Background()
	for i := 1; i <= 100; i++ {
		if _, err := s.CreateSSOUser(ctx, "bob", RoleViewer, issuer, "bob-"+strings.Repeat("x", i), "", t0); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateSSOUser(ctx, "bob", RoleViewer, issuer, "bob-last", "", t0); err == nil {
		t.Error("more than 100 users of one name")
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, hashes := NewRecoveryCodes()
	if len(codes) != RecoveryCodeCount || len(hashes) != RecoveryCodeCount {
		t.Fatal(len(codes))
	}
	for _, c := range codes {
		if len(c) != 14 || c[4] != '-' || c[9] != '-' || strings.ContainsAny(c, "01ilo") {
			t.Errorf("code %q", c)
		}
	}
	if normalizeRecoveryCode("ABCD EFGH-JKMN") != "abcd-efgh-jkmn" || normalizeRecoveryCode("abc") != "abc" {
		t.Error("normalize")
	}
}

func TestTwoFactor(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		u, _ := s.CreateFirstAdmin(ctx, "alice", pw, t0)
		_, hashes := NewRecoveryCodes()
		if err := s.EnableTwoFactor(ctx, 9999, "sealed", 5, hashes); !errors.Is(err, ErrNotFound) {
			t.Errorf("unknown user: %v", err)
		}
		if err := s.EnableTwoFactor(ctx, u.ID, "sealed", 5, hashes); err != nil {
			t.Fatal(err)
		}
		if u, _ := s.UserByID(ctx, u.ID); !u.TwoFactor {
			t.Error("not on")
		}
		if sec, err := s.TwoFactorSecret(ctx, u.ID); sec != "sealed" || err != nil {
			t.Errorf("secret: %q %v", sec, err)
		}
		if _, err := s.TwoFactorSecret(ctx, 9999); !errors.Is(err, ErrNotFound) {
			t.Errorf("secret of unknown: %v", err)
		}
		// With two-factor sign-in, the password alone does not record a sign-in.
		if u, _ := s.Authenticate(ctx, "alice", pw, t0); !u.LastLogin.IsZero() {
			t.Error("sign-in recorded before the code")
		}
		for _, c := range []struct {
			step int64
			want bool
		}{{5, false}, {4, false}, {6, true}, {6, false}} {
			if ok, _ := s.UseTOTPStep(ctx, u.ID, c.step); ok != c.want {
				t.Errorf("step %d: %v", c.step, ok)
			}
		}
		if on, err := s.DisableTwoFactor(ctx, u.ID); !on || err != nil {
			t.Errorf("off: %v %v", on, err)
		}
		if on, _ := s.DisableTwoFactor(ctx, u.ID); on {
			t.Error("turned off twice")
		}
		if n, _ := s.RecoveryCodesLeft(ctx, u.ID); n != 0 {
			t.Errorf("codes left after turning off: %d", n)
		}
	})
}

func TestUseRecoveryCode(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		u, _ := s.CreateFirstAdmin(ctx, "alice", pw, t0)
		codes, hashes := NewRecoveryCodes()
		s.EnableTwoFactor(ctx, u.ID, "sealed", 5, hashes)
		typed := strings.ToUpper(strings.ReplaceAll(codes[0], "-", " "))
		if ok, err := s.UseRecoveryCode(ctx, u.ID, typed); !ok || err != nil {
			t.Errorf("recovery code: %v %v", ok, err)
		}
		for _, bad := range []string{codes[0], "abc"} {
			if ok, _ := s.UseRecoveryCode(ctx, u.ID, bad); ok {
				t.Errorf("%q worked", bad)
			}
		}
		if n, _ := s.RecoveryCodesLeft(ctx, u.ID); n != RecoveryCodeCount-1 {
			t.Errorf("left %d", n)
		}
		_, fresh := NewRecoveryCodes()
		if err := s.ReplaceRecoveryCodes(ctx, u.ID, fresh[:3]); err != nil {
			t.Fatal(err)
		}
		if n, _ := s.RecoveryCodesLeft(ctx, u.ID); n != 3 {
			t.Errorf("after replacing: %d", n)
		}
		if ok, _ := s.UseRecoveryCode(ctx, u.ID, codes[1]); ok {
			t.Error("an old code worked after replacing")
		}
	})
}

func TestPictureSources(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		u, _ := s.CreateFirstAdmin(ctx, "alice", pw, t0)
		if _, ok, err := s.UserPicture(ctx, u.ID); ok || err != nil {
			t.Fatalf("none: %v %v", ok, err)
		}
		if _, err := s.SetUserPicture(ctx, u.ID, "image/png", []byte{1}, "camera", t0); err == nil {
			t.Error("unknown source")
		}
		for _, c := range []struct {
			data   byte
			source string
			stored bool
		}{{2, PictureSSO, true}, {3, PictureUpload, true}, {4, PictureSSO, false}} {
			if ok, err := s.SetUserPicture(ctx, u.ID, "image/png", []byte{c.data}, c.source, t0.Add(time.Duration(c.data)*time.Second)); ok != c.stored || err != nil {
				t.Errorf("%+v: %v %v", c, ok, err)
			}
		}
		p, ok, err := s.UserPicture(ctx, u.ID)
		if !ok || err != nil || !bytes.Equal(p.Data, []byte{3}) || p.Source != PictureUpload || !p.UpdatedAt.Equal(t0.Add(3*time.Second)) {
			t.Fatalf("picture %+v %v %v", p, ok, err)
		}
		if u, _ := s.UserByID(ctx, u.ID); !u.Picture.Equal(t0.Add(3 * time.Second)) {
			t.Errorf("user picture time %v", u.Picture)
		}
		// Deleting the provider's picture leaves an upload alone.
		s.DeleteUserPicture(ctx, u.ID, PictureSSO)
		if _, ok, _ := s.UserPicture(ctx, u.ID); !ok {
			t.Error("the upload was deleted")
		}
	})
}

func TestRemovePicture(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		u, _ := s.CreateFirstAdmin(ctx, "alice", pw, t0)
		// Removing an upload keeps single sign-on allowed.
		s.SetUserPicture(ctx, u.ID, "image/png", []byte{1}, PictureUpload, t0)
		if err := s.RemoveUserPicture(ctx, u.ID); err != nil {
			t.Fatal(err)
		}
		if u, _ := s.UserByID(ctx, u.ID); !u.SSOPicture || !u.Picture.IsZero() {
			t.Errorf("after removing an upload: %+v", u)
		}
		// Removing the provider's picture stops single sign-on setting it.
		s.SetUserPicture(ctx, u.ID, "image/png", []byte{5}, PictureSSO, t0)
		s.RemoveUserPicture(ctx, u.ID)
		if u, _ := s.UserByID(ctx, u.ID); u.SSOPicture {
			t.Error("single sign-on may still set the picture")
		}
		if err := s.SetSSOPicture(ctx, u.ID, true); err != nil {
			t.Fatal(err)
		}
		if u, _ := s.UserByID(ctx, u.ID); !u.SSOPicture {
			t.Error("single sign-on picture not allowed again")
		}
	})
}

func TestCreateAPIToken(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		for _, bad := range []string{"", "  ", strings.Repeat("n", 65)} {
			if _, _, err := s.CreateAPIToken(ctx, bad, "alice", t0); err == nil {
				t.Errorf("name %q accepted", bad)
			}
		}
		tok, secret, err := s.CreateAPIToken(ctx, " ci ", "alice", t0)
		if err != nil || tok.Name != "ci" || tok.ID == 0 || !strings.HasPrefix(secret, APITokenPrefix) || len(secret) != 4+43 {
			t.Fatalf("token %+v %q %v", tok, secret, err)
		}
		list, err := s.APITokens(ctx)
		if err != nil || len(list) != 1 || !list[0].LastUsed.IsZero() || list[0].CreatedBy != "alice" || !list[0].CreatedAt.Equal(t0) {
			t.Fatalf("list %+v %v", list, err)
		}
	})
}

func TestUseAPIToken(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		tok, secret, _ := s.CreateAPIToken(ctx, "ci", "alice", t0)
		for _, bad := range []string{"nope", APITokenPrefix + "nope"} {
			if _, ok, _ := s.UseAPIToken(ctx, bad, t0); ok {
				t.Errorf("%q accepted", bad)
			}
		}
		name, ok, err := s.UseAPIToken(ctx, secret, t0.Add(time.Hour))
		if !ok || err != nil || name != "ci" {
			t.Fatalf("use: %q %v %v", name, ok, err)
		}
		if list, _ := s.APITokens(ctx); !list[0].LastUsed.Equal(t0.Add(time.Hour)) {
			t.Errorf("last used %v", list[0].LastUsed)
		}
		if name, err := s.DeleteAPIToken(ctx, tok.ID); name != "ci" || err != nil {
			t.Errorf("delete: %q %v", name, err)
		}
		if _, err := s.DeleteAPIToken(ctx, tok.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("delete twice: %v", err)
		}
		if _, ok, _ := s.UseAPIToken(ctx, secret, t0); ok {
			t.Error("a revoked token works")
		}
	})
}
