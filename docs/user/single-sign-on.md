# Single sign-on

Eyrie can let people sign in with an OpenID Connect (OIDC) provider,
alongside passwords or instead of them.

## How it works in brief

- The sign-in page shows a **Sign in with SSO** button. The label is
  yours to set, for example "Sign in with Example ID".
- Eyrie uses the authorization code flow with PKCE, and checks the ID
  token's signature, audience, issuer and nonce.
- Accounts are matched on the provider's **issuer and subject** (its stable
  user ID), never on username or email. An SSO user called `alice` is not
  the same account as a local user `alice` until you link them.
- Eyrie asks for the scopes `openid profile email`, plus `groups` unless the
  provider lists its scopes without it (see [Other providers](#other-providers)).

## 1. Register Eyrie with your provider

Create an OIDC client (also called an application) at your provider:

| Field | Value |
| --- | --- |
| Name | `Eyrie` |
| Redirect or callback URL | The one shown in Eyrie under **Settings → Single sign-on**, for example `https://eyrie.example.com/auth/oidc/callback` |
| Client type | Confidential (with a client secret) is recommended. A public client also works: Eyrie uses PKCE and can sign in without a secret. |
| PKCE | On, if the provider asks |

Copy the **client ID** and **client secret**.

The callback URL comes from `EYRIE_BASE_URL` when it is set, otherwise from
the address you are using to view Eyrie. Behind a reverse proxy, set
`EYRIE_BASE_URL` (see the
[configuration reference](../technical/configuration-reference.md)) so the
URL is the public one, and register exactly that.

### Groups (optional)

To use the allowed-group or admin-group options, create the groups at your
provider (for example `eyrie-users` and `eyrie-admins`), add people, and make
sure the provider sends group membership to the client in a claim named
`groups`. Eyrie reads groups from the ID token, or from the userinfo
endpoint when the token has none. Group names are compared ignoring case, so
`Eyrie-Admins` matches `eyrie-admins`. Each single sign-on sign-in logs the
groups the provider sent (`msg="sso sign-in"` with `user` and `groups`): if a
group option does not work, compare the name there with the one in Eyrie.

## 2. Configure Eyrie

**Settings → Single sign-on** (admins):

| Field | Meaning |
| --- | --- |
| Turn on single sign-on | Shows the button on the sign-in page and turns on `/auth/oidc/…`. |
| Issuer URL | Your provider's address, for example `https://id.example.com`. Eyrie loads `/.well-known/openid-configuration` from it. A trailing slash is removed. |
| Client ID | From your provider. |
| Client secret | From your provider. Stored encrypted. Leave it blank to keep the saved one; tick "Remove the saved client secret" to delete it. |
| Button label | The provider's name on the button and in messages, for example "Example ID"; "SSO" when empty. |
| Allowed group | If set, only members of this group may sign in. Others see "Your SSO account is not in the "…" group." (with the button label in place of SSO). |
| Admin group | If set, members get the admin role; see below. |
| Create an account the first time someone signs in | On by default. Off: only people whose accounts are already linked can sign in. |
| Turn off password sign-in | Makes single sign-on the only way in. See [SSO only](#sso-only). |

**Test connection** checks that the issuer's discovery document loads,
without saving: "✓ Found https://id.example.com (sign-in at …)." or "✗" with
the reason.

Save. The sign-in page now has the button.

## What happens when someone signs in

1. If an account is linked to their identity, they are signed in. If that
   account is disabled, they are refused: "Your account is disabled."
2. Otherwise, if account creation is on, Eyrie creates an account:
   - **Username:** `preferred_username`, else the email address, else the
     subject, cut to 60 characters. If the name is taken, a number is
     added: `alice-2`, `alice-3`, … up to `-100`.
   - **Role:** admin if they are in the admin group, otherwise viewer.
   - The account has no password: it signs in only with single sign-on.
3. Otherwise they see "No Eyrie account is linked to your SSO account. Ask
   an admin to add you."

Any failure (the provider refusing, a wrong client secret, an expired
sign-in) returns to the sign-in page with the reason. A sign-in must be
finished at the provider within 10 minutes, and each one works once.

### Admin group, precisely

- **New accounts** made by single sign-on are admins if they are in the
  admin group, viewers if not.
- **Accounts made by single sign-on** (no password) follow the group at
  every sign-in: added to it, they become admins; removed, viewers. The
  change is logged. The last admin is never made a viewer. While an admin
  group is set, Users shows their role as "from the SSO admin group" and
  has no control to change it.
- **Accounts with a password** (including linked ones) keep the role an
  admin gave them; the group does not change them.
- Without an admin group, admins set every role in Users.

### Profile pictures

At each single sign-on sign-in, and when an account is linked, Eyrie fetches
the image in the provider's `picture` claim and stores it as the account's
picture. It must be a PNG, JPEG, GIF or WebP of up to 1 MB, fetched within
10 seconds; otherwise it is skipped with a warning in the log and the
sign-in goes on. If the picture is on the provider's own host, the request
carries the sign-in's access token, for providers that protect pictures. A
sign-in without a `picture` claim removes the stored one.

A picture the user uploaded is never replaced. A user who removes the
provider's picture does not get it back at later sign-ins, until they click
**Use the … picture again** in Your account.

## Linking an existing account

To use single sign-on for an account that has a password (your own admin
account, typically):

1. Sign in with your password.
2. **Settings → Your account → Link my … account.**
3. Sign in at your provider. You come back to Your account with "… account
   linked."

The account can now sign in either way. **Unlink** removes the link; it is
offered only to accounts that also have a password, so nobody locks
themselves out.

Linking turns off the account's
[two-factor sign-in](web-ui.md#two-factor-sign-in), if it had it, and it is
not offered while linked: use your provider's. One identity can be linked to
one Eyrie account only.

## SSO only

"Turn off password sign-in" hides the password fields and refuses password
sign-ins. Eyrie accepts this setting only when **your own account is
linked**, so you cannot lock yourself out by saving it.

### If your provider cannot be reached

1. Set `EYRIE_LOCAL_LOGIN=force` in the Compose file and recreate the
   container (`docker compose up -d`). Password sign-in works again,
   whatever the setting says. Eyrie logs a warning at start while it is set.
2. Sign in with a password. If you have none (an account made by single
   sign-on), set one from the host:

   ```sh
   echo 'a new long password' | docker compose exec -T eyrie /eyrie reset-password alice
   ```

3. Remove `EYRIE_LOCAL_LOGIN` again when your provider is back.

## Security notes

- The client secret is encrypted with AES-256-GCM before it is stored, with
  `EYRIE_SECRET_KEY` or the key in `EYRIE_DATA_DIR/secret.key`. Keep that
  file with the database: without it the saved secret cannot be read and
  must be entered again.
- Sign-ins in progress are lost if Eyrie restarts.
- Signing out of Eyrie does not sign you out of your provider.
- Details: [Security](../technical/security.md#single-sign-on).

## Other providers

Eyrie uses standard OpenID Connect only, so any provider with a discovery
document and the authorization code flow should work. The issuer URL must
match the token's `iss` claim exactly.

What Eyrie adapts to, from the discovery document:

- **Scopes.** `groups` is not a standard scope, and some providers refuse a
  sign-in that asks for a scope they do not know. Eyrie asks for `groups`
  only if the provider lists it in `scopes_supported`, or lists no scopes
  at all.
- **Token signatures.** RS256/384/512, ES256/384/512, PS256/384/512 and
  EdDSA are checked against the provider's published keys. HS256 (signed
  with the client secret) is accepted only if the provider lists it, a client
  secret is saved, and the secret is at least 32 bytes long.

The group options read a claim named `groups` that holds a list of group
names. Providers that send groups another way can still be used to sign in;
leave the allowed and admin groups empty.

Notes per provider, from each provider's documentation; they have not been
tested against Eyrie:

| Provider | Notes |
| --- | --- |
| Authelia | Works as is; it has a `groups` scope and claim. |
| Authentik | Select a **Signing Key** on the provider for RS256. Without one, Authentik signs with HS256 using the client secret, which also works (its generated secrets are long enough). Groups arrive in `groups` with the `profile` scope. |
| Keycloak | Groups need a **Group Membership** mapper on the client (or a client scope) with the token claim name `groups`. Its **Full group path** option is on by default, so groups arrive as `/eyrie-admins`: turn it off or enter the group with the leading `/`. |
| Kanidm | Groups arrive as `name@domain` (for example `eyrie-admins@idm.example.com`), so enter them that way. |
| Okta | Add a **groups claim** to the app (or authorization server) for the groups you need. |
| Microsoft Entra ID | Sign-in works. Its `groups` claim holds group object IDs, not names, so enter the ID in the group fields. |
| Google | Sign-in works. Google has no groups claim, so leave the group options empty. |
| Zitadel, Auth0 | Sign-in works. Neither sends a `groups` claim by default, so leave the group options empty. |
