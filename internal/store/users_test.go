// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var t0 = time.UnixMilli(1_800_000_000_000).UTC()

const pw = "correct horse battery"

func TestPasswordHash(t *testing.T) {
	h, err := HashPassword(pw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Errorf("hash %q", h)
	}
	if !CheckPassword(h, pw) || CheckPassword(h, "wrong horse battery") {
		t.Fatal("the password check is wrong")
	}
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("a short password was accepted")
	}
	for _, bad := range []string{"", "$bcrypt$x$y$z$w", "$argon2id$v=19$m=x$a$b", "$argon2id$v=19$m=1,t=1,p=1$!!$AAAA",
		"$argon2id$v=19$m=1,t=1,p=1$AAAA$", "$argon2id$v=19$m=1,t=1,p=1$AAAA$" + strings.Repeat("A", 2000)} {
		if CheckPassword(bad, pw) {
			t.Errorf("accepted the hash %q", bad)
		}
	}
}

func TestCleanUsername(t *testing.T) {
	for _, c := range [][2]string{{" alice ", "alice"}, {"Bob", "Bob"}, {"ünïcode", "ünïcode"}} {
		if got, err := CleanUsername(c[0]); err != nil || got != c[1] {
			t.Errorf("%q: %q %v", c[0], got, err)
		}
	}
	for _, bad := range []string{"", "   ", strings.Repeat("a", MaxUsernameLength+1), "a\nb"} {
		if _, err := CleanUsername(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestFirstAdmin(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		if n, err := s.CountUsers(ctx); err != nil || n != 0 {
			t.Fatalf("count: %d %v", n, err)
		}
		admin, err := s.CreateFirstAdmin(ctx, " Alice ", pw, t0)
		if err != nil {
			t.Fatal(err)
		}
		want := User{ID: admin.ID, Username: "Alice", Role: RoleAdmin, HasLocal: true, SSOPicture: true, CreatedAt: t0}
		if admin != want {
			t.Fatalf("first admin %+v", admin)
		}
		if _, err := s.CreateFirstAdmin(ctx, "bob", pw, t0); !errors.Is(err, ErrSetupDone) {
			t.Fatalf("a second first admin: %v", err)
		}
		var bad *InvalidError
		if _, err := s.CreateFirstAdmin(ctx, "", pw, t0); !errors.As(err, &bad) {
			t.Errorf("first admin without a name: %v", err)
		}
		if _, err := s.CreateFirstAdmin(ctx, "bob", "short", t0); !errors.As(err, &bad) {
			t.Errorf("first admin with a short password: %v", err)
		}
	})
}

// twoUsers makes alice, an admin, and bob, a viewer.
func twoUsers(t *testing.T, s *Store) (alice, bob User) {
	t.Helper()
	ctx := context.Background()
	alice, err := s.CreateFirstAdmin(ctx, "Alice", pw, t0)
	if err != nil {
		t.Fatal(err)
	}
	bob, err = s.CreateUser(ctx, "bob", pw, RoleViewer, t0)
	if err != nil || bob.IsAdmin() {
		t.Fatalf("bob: %+v %v", bob, err)
	}
	return alice, bob
}

func TestCreateUser(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		twoUsers(t, s)
		if _, err := s.CreateUser(ctx, "alice", pw, RoleViewer, t0); err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("usernames must be unique ignoring case: %v", err)
		}
		for _, c := range []struct{ name, pass, role string }{{"", pw, RoleViewer}, {"carol", "short", RoleViewer}, {"carol", pw, "root"}} {
			var bad *InvalidError
			if _, err := s.CreateUser(ctx, c.name, c.pass, c.role, t0); !errors.As(err, &bad) {
				t.Errorf("created %+v: %v", c, err)
			}
		}
		users, err := s.Users(ctx)
		if err != nil || len(users) != 2 || users[0].Username != "Alice" || users[1].Username != "bob" {
			t.Fatalf("users: %+v %v", users, err)
		}
	})
}

func TestAuthenticate(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		alice, bob := twoUsers(t, s)
		if _, err := s.Authenticate(ctx, "alice", "wrong horse battery", t0); !errors.Is(err, ErrBadLogin) {
			t.Errorf("wrong password: %v", err)
		}
		if _, err := s.Authenticate(ctx, "nobody", pw, t0); !errors.Is(err, ErrBadLogin) {
			t.Errorf("unknown user: %v", err)
		}
		later := t0.Add(time.Minute)
		u, err := s.Authenticate(ctx, "ALICE", pw, later)
		if err != nil || u.ID != alice.ID || !u.LastLogin.Equal(later) {
			t.Fatalf("sign-in: %+v %v", u, err)
		}
		if u, err := s.UserByName(ctx, "BOB"); err != nil || u.ID != bob.ID {
			t.Errorf("by name: %+v %v", u, err)
		}
		if _, err := s.UserByName(ctx, "nobody"); !errors.Is(err, ErrNotFound) {
			t.Errorf("unknown name: %v", err)
		}
		if _, err := s.UserByID(ctx, 9999); !errors.Is(err, ErrNotFound) {
			t.Errorf("unknown id: %v", err)
		}
	})
}

func TestPasswords(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		_, bob := twoUsers(t, s)
		if ok, err := s.VerifyPassword(ctx, bob.ID, pw); !ok || err != nil {
			t.Errorf("verify: %v %v", ok, err)
		}
		if ok, _ := s.VerifyPassword(ctx, bob.ID, "nope nope nope"); ok {
			t.Error("verified a wrong password")
		}
		if _, err := s.VerifyPassword(ctx, 9999, pw); !errors.Is(err, ErrNotFound) {
			t.Errorf("verify unknown: %v", err)
		}
		if err := s.SetPassword(ctx, bob.ID, "short"); err == nil {
			t.Error("set a short password")
		}
		if err := s.SetPassword(ctx, 9999, pw); !errors.Is(err, ErrNotFound) {
			t.Errorf("set password of unknown: %v", err)
		}
		if err := s.SetPassword(ctx, bob.ID, "another long password"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Authenticate(ctx, "bob", "another long password", t0); err != nil {
			t.Errorf("new password: %v", err)
		}
	})
}

func TestRolesAndLastAdmin(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		a, _ := s.CreateFirstAdmin(ctx, "alice", pw, t0)
		for name, err := range map[string]error{
			"demote":  s.SetUserRole(ctx, a.ID, RoleViewer),
			"disable": s.SetUserDisabled(ctx, a.ID, true),
			"delete":  s.DeleteUser(ctx, a.ID),
		} {
			if err == nil || !strings.Contains(err.Error(), "last admin") {
				t.Errorf("%s the last admin: %v", name, err)
			}
		}
		if err := s.SetUserRole(ctx, a.ID, "root"); err == nil {
			t.Error("unknown role")
		}
		if err := s.SetUserRole(ctx, 9999, RoleAdmin); !errors.Is(err, ErrNotFound) {
			t.Errorf("role of unknown: %v", err)
		}
		if err := s.SetUserDisabled(ctx, 9999, true); !errors.Is(err, ErrNotFound) {
			t.Errorf("disable unknown: %v", err)
		}
		if err := s.DeleteUser(ctx, 9999); !errors.Is(err, ErrNotFound) {
			t.Errorf("delete unknown: %v", err)
		}

		b, _ := s.CreateUser(ctx, "bob", pw, RoleViewer, t0)
		if err := s.SetUserRole(ctx, b.ID, RoleAdmin); err != nil {
			t.Fatal(err)
		}
		if err := s.SetUserRole(ctx, a.ID, RoleViewer); err != nil {
			t.Fatalf("demote with another admin: %v", err)
		}
		if err := s.SetUserRole(ctx, a.ID, RoleAdmin); err != nil {
			t.Fatal(err)
		}

		token, _ := s.CreateSession(ctx, a.ID, "192.0.2.1", "test", t0)
		if err := s.SetUserDisabled(ctx, a.ID, true); err != nil {
			t.Fatal(err)
		}
		if _, ok, _ := s.SessionByToken(ctx, token, t0); ok {
			t.Error("a session survived disabling")
		}
		if _, err := s.Authenticate(ctx, "alice", pw, t0); !errors.Is(err, ErrBadLogin) {
			t.Error("a disabled user signed in")
		}
		// alice is disabled, so bob is the last enabled admin.
		if err := s.SetUserDisabled(ctx, b.ID, true); err == nil {
			t.Error("disabled the last enabled admin")
		}
		// A disabled admin can be deleted.
		if err := s.DeleteUser(ctx, a.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.SetUserDisabled(ctx, b.ID, false); err != nil {
			t.Errorf("enable: %v", err)
		}
		if n, _ := s.CountUsers(ctx); n != 1 {
			t.Errorf("users = %d", n)
		}
	})
}

func TestSessions(t *testing.T) {
	eachDB(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		u, _ := s.CreateFirstAdmin(ctx, "alice", pw, t0)
		token, err := s.CreateSession(ctx, u.ID, "192.0.2.1", strings.Repeat("x", 300), t0)
		if err != nil {
			t.Fatal(err)
		}
		var stored string
		s.db.QueryRow(s.q(`SELECT id FROM sessions`)).Scan(&stored)
		if stored == token || stored != hashToken(token) {
			t.Error("the session token must be stored hashed")
		}
		sess, ok, err := s.SessionByToken(ctx, token, t0.Add(time.Minute))
		if err != nil || !ok || sess.User.ID != u.ID || sess.CSRF == "" {
			t.Fatalf("session: %+v %v %v", sess, ok, err)
		}
		if _, ok, _ := s.SessionByToken(ctx, "nope", t0); ok {
			t.Error("unknown token accepted")
		}
		// Used after two hours, the expiry slides.
		if _, ok, _ := s.SessionByToken(ctx, token, t0.Add(2*time.Hour)); !ok {
			t.Fatal("session lost")
		}
		if _, ok, _ := s.SessionByToken(ctx, token, t0.Add(SessionTTL+time.Hour)); !ok {
			t.Error("the expiry did not slide")
		}
		if _, ok, _ := s.SessionByToken(ctx, token, t0.Add(3*SessionTTL)); ok {
			t.Error("expired session accepted")
		}
		if err := s.PruneSessions(ctx, t0.Add(3*SessionTTL)); err != nil {
			t.Fatal(err)
		}
		var n int
		s.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n)
		if n != 0 {
			t.Errorf("%d sessions after pruning", n)
		}

		token, _ = s.CreateSession(ctx, u.ID, "", "", t0)
		if err := s.DeleteSession(ctx, token); err != nil {
			t.Fatal(err)
		}
		if _, ok, _ := s.SessionByToken(ctx, token, t0); ok {
			t.Error("a deleted session works")
		}
		token, _ = s.CreateSession(ctx, u.ID, "", "", t0)
		s.SetPassword(ctx, u.ID, "another long password")
		if _, ok, _ := s.SessionByToken(ctx, token, t0); ok {
			t.Error("a session survived a password change")
		}
	})
}

func TestClosedStore(t *testing.T) {
	s := openSQLite(t)
	ctx := context.Background()
	u, _ := s.CreateFirstAdmin(ctx, "alice", pw, t0)
	token, _ := s.CreateSession(ctx, u.ID, "", "", t0)
	s.Close()
	checks := map[string]error{}
	_, checks["count"] = s.CountUsers(ctx)
	_, checks["create"] = s.CreateUser(ctx, "bob", pw, RoleViewer, t0)
	_, checks["first"] = s.CreateFirstAdmin(ctx, "bob", pw, t0)
	_, checks["auth"] = s.Authenticate(ctx, "alice", pw, t0)
	_, checks["users"] = s.Users(ctx)
	checks["password"] = s.SetPassword(ctx, u.ID, pw)
	_, checks["verify"] = s.VerifyPassword(ctx, u.ID, pw)
	checks["role"] = s.SetUserRole(ctx, u.ID, RoleViewer)
	checks["disable"] = s.SetUserDisabled(ctx, u.ID, true)
	checks["delete"] = s.DeleteUser(ctx, u.ID)
	_, _, checks["session"] = s.SessionByToken(ctx, token, t0)
	_, checks["new session"] = s.CreateSession(ctx, u.ID, "", "", t0)
	_, _, checks["identity"] = s.UserByIdentity(ctx, "i", "s")
	checks["link"] = s.LinkIdentity(ctx, u.ID, "i", "s", "", t0)
	checks["unlink"] = s.UnlinkIdentities(ctx, u.ID)
	_, checks["identities"] = s.Identities(ctx, u.ID)
	_, checks["sso user"] = s.CreateSSOUser(ctx, "carol", RoleViewer, "i", "s", "", t0)
	checks["2fa on"] = s.EnableTwoFactor(ctx, u.ID, "x", 1, nil)
	checks["codes"] = s.ReplaceRecoveryCodes(ctx, u.ID, nil)
	_, checks["2fa off"] = s.DisableTwoFactor(ctx, u.ID)
	_, checks["secret"] = s.TwoFactorSecret(ctx, u.ID)
	_, checks["step"] = s.UseTOTPStep(ctx, u.ID, 1)
	_, checks["recovery"] = s.UseRecoveryCode(ctx, u.ID, "x")
	_, checks["left"] = s.RecoveryCodesLeft(ctx, u.ID)
	_, checks["set picture"] = s.SetUserPicture(ctx, u.ID, "image/png", nil, PictureUpload, t0)
	_, _, checks["picture"] = s.UserPicture(ctx, u.ID)
	checks["remove picture"] = s.RemoveUserPicture(ctx, u.ID)
	_, _, checks["new token"] = s.CreateAPIToken(ctx, "ci", "alice", t0)
	_, checks["tokens"] = s.APITokens(ctx)
	_, _, checks["use token"] = s.UseAPIToken(ctx, APITokenPrefix+"x", t0)
	for name, err := range checks {
		if err == nil {
			t.Errorf("%s worked on a closed database", name)
		}
	}
}
