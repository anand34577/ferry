# Single sign-on (OIDC)

Let people sign in to Ferry with an account they already have — Keycloak, Authentik, Authelia, Zitadel, Google, Microsoft Entra ID, GitLab or any other **OpenID Connect** provider. Password sign-in keeps working next to it.

- [How it works](#how-it-works)
- [Quick setup](#quick-setup)
- [Settings](#settings)
- [Keycloak step by step](#keycloak-step-by-step)
- [Other providers](#other-providers)
- [What users see](#what-users-see)
- [Administrators](#administrators)
- [Troubleshooting](#troubleshooting)
- [Security notes](#security-notes)

## How it works

When someone chooses **Sign in with …**, Ferry sends them to your provider, they sign in there, and the provider sends them back. Ferry then decides which Ferry account that person is, in this order:

| # | Situation | What happens |
|---|---|---|
| 1 | This provider account was connected before | Signed in. |
| 2 | An existing Ferry account has **the same email**, and the provider says the email is **verified** | The provider account is connected to it automatically, then signed in. Turn off with `FERRY_OIDC_LINK_BY_EMAIL=false`. |
| 3 | No match, and `FERRY_OIDC_AUTO_CREATE=true` | A new Ferry account is created (name and email from the provider), then signed in. |
| 4 | No match otherwise | Ferry asks the person to **sign in once with their existing Ferry email and password**, then shows **Connect accounts**. After confirming, the provider sign-in works on its own from then on. If they have no Ferry account, they see a message to ask an administrator, and **nothing is created**. |

So, with the default `FERRY_OIDC_AUTO_CREATE=false`, only people who already have a Ferry account can get in. An administrator creates the account first (**Admin → Users → Add user**, same email as in the provider), and it connects automatically on the first sign-in.

Connecting never merges or moves files — one provider account simply becomes another way to sign in to one Ferry account.

## Quick setup

1. In your provider, create a client (also called "application"):
   - Type: **OpenID Connect**, **confidential** (with a client secret). Public clients with PKCE also work — leave the secret empty.
   - Flow: **Authorization code** (called "Standard flow" in Keycloak).
   - Redirect URI: **`https://files.example.com/api/v1/auth/oidc/callback`** — your Ferry address followed by `/api/v1/auth/oidc/callback`.
   - Scopes: `openid profile email`.
2. In Ferry, open **Admin → Settings**:
   - under **General**, set **Public address** to the address people use, e.g. `https://files.example.com`;
   - under **Single sign-on (OIDC)**, copy the **Redirect URI** shown there into your provider, then fill in **Issuer URL**, **Client ID**, **Client secret** and a **Button label** such as `Keycloak`, and **Save**.
3. That's it — no restart. The sign-in page now shows **Sign in with Keycloak**.

Prefer configuration files (Docker, Ansible, …)? The same settings work as environment variables; they then show as locked in Admin → Settings:

```ini
FERRY_PUBLIC_URL=https://files.example.com
FERRY_OIDC_ISSUER=https://keycloak.example.com/realms/myrealm
FERRY_OIDC_CLIENT_ID=ferry
FERRY_OIDC_CLIENT_SECRET=paste-the-secret-here
FERRY_OIDC_NAME=Keycloak
```

> **Always set the public address** when Ferry runs behind a reverse proxy. Ferry builds the redirect URI from it; if it's missing or wrong, the provider rejects the sign-in with *invalid redirect_uri*.

## Settings

Each row is a field in **Admin → Settings → Single sign-on** and, alternatively, an environment variable.

| Variable | Default | Description |
|---|---|---|
| `FERRY_OIDC_ISSUER` | — | The provider's issuer URL. Setting it turns SSO on. Must match the `issuer` in `<issuer>/.well-known/openid-configuration` exactly (watch for trailing slashes, `http` vs `https`, the realm name). |
| `FERRY_OIDC_CLIENT_ID` | — | Client ID from the provider. Required. |
| `FERRY_OIDC_CLIENT_SECRET` | — | Client secret. Leave empty for a public client. |
| `FERRY_OIDC_NAME` | `Single sign-on` | Button label and name used in messages, e.g. `Keycloak`, `Company login`. |
| `FERRY_OIDC_SCOPES` | `openid profile email` | Scopes to request. Add e.g. `groups` if your provider needs it to send group membership. |
| `FERRY_OIDC_AUTO_CREATE` | `false` | Create a Ferry account on first sign-in for people without one. Needs a verified email. |
| `FERRY_OIDC_LINK_BY_EMAIL` | `true` | Connect automatically to an existing account with the same **verified** email. |
| `FERRY_OIDC_TRUST_UNVERIFIED_EMAIL` | `false` | Treat the provider's email as verified even when it doesn't say so. Only for providers where users can't choose their own email. |
| `FERRY_OIDC_ALLOWED_DOMAINS` | — | Comma-separated email domains allowed to sign in with SSO, e.g. `example.com,example.org`. Empty allows all. Strongly recommended with Google or Microsoft consumer accounts. |
| `FERRY_OIDC_ADMIN_GROUP` | — | Members of this group/role are made administrators, and non-members lose admin, at every SSO sign-in. The last remaining administrator is never demoted. Empty = roles are managed only in Ferry. |
| `FERRY_OIDC_GROUPS_CLAIM` | `groups` | Where to read groups/roles from the ID token. Dotted paths work, e.g. `realm_access.roles` (Keycloak realm roles) or `resource_access.ferry.roles` (Keycloak client roles). |
| `FERRY_OIDC_SKIP_VERIFY` | `false` | Accept a self-signed TLS certificate from the provider. Test setups only. |

## Keycloak step by step

Tested flow for Keycloak 20 and newer (the admin console layout below is from Keycloak 24–26).

**1. Create the client**

1. Open the admin console, pick your realm (not `master` unless you really use it), go to **Clients → Create client**.
2. *General settings*: Client type **OpenID Connect**, Client ID `ferry`, Name `Ferry`. **Next**.
3. *Capability config*: **Client authentication ON**, **Standard flow ON**. You can turn *Direct access grants* off. **Next**.
4. *Login settings*:
   - **Root URL**: `https://files.example.com`
   - **Valid redirect URIs**: `https://files.example.com/api/v1/auth/oidc/callback`
   - **Web origins**: `https://files.example.com`
5. **Save**, open the **Credentials** tab and copy the **Client secret**.

**2. Configure Ferry** — in **Admin → Settings → Single sign-on**: Issuer URL `https://keycloak.example.com/realms/myrealm`, Client ID `ferry`, the client secret, button label `Keycloak`. Or as variables:

```ini
FERRY_PUBLIC_URL=https://files.example.com
FERRY_OIDC_ISSUER=https://keycloak.example.com/realms/myrealm
FERRY_OIDC_CLIENT_ID=ferry
FERRY_OIDC_CLIENT_SECRET=<client secret>
FERRY_OIDC_NAME=Keycloak
```

The issuer is `https://<keycloak host>/realms/<realm name>`. Older Keycloak (before 17, WildFly-based) has `/auth` in front: `https://<host>/auth/realms/<realm>`. Check by opening `<issuer>/.well-known/openid-configuration` in a browser — you should see JSON whose `issuer` is exactly your value.

**3. Make emails verified** — automatic connection by email (row 2 above) and account creation only use verified emails. In Keycloak, users imported from LDAP or created by an admin often have *Email verified* off: turn it on per user (**Users → user → Email verified**), or enable *Trust Email* on your LDAP/identity-provider federation. As a last resort set `FERRY_OIDC_TRUST_UNVERIFIED_EMAIL=true` (only if users can't edit their own email in Keycloak).

**4. Optional: administrators from Keycloak**

*With a realm role* (simplest): **Realm roles → Create role** `ferry-admins`, assign it to users, then

```ini
FERRY_OIDC_ADMIN_GROUP=ferry-admins
FERRY_OIDC_GROUPS_CLAIM=realm_access.roles
```

*With a group*: create group `ferry-admins`, then in **Clients → ferry → Client scopes → ferry-dedicated → Add mapper → By configuration → Group Membership**: Name `groups`, Token Claim Name `groups`, *Full group path* off (on also works — Ferry ignores the leading `/`), *Add to ID token* **ON**. Then

```ini
FERRY_OIDC_ADMIN_GROUP=ferry-admins
FERRY_OIDC_GROUPS_CLAIM=groups
```

**5. Optional: let everyone in the realm use Ferry** — `FERRY_OIDC_AUTO_CREATE=true`. To allow only some people, create a separate realm for Ferry users, or restrict the client with an authentication flow / Keycloak's client policies.

**Keycloak behind the same reverse proxy?** Keycloak must know its public address, otherwise its issuer says `http://keycloak:8080/...` and Ferry refuses the tokens. Start Keycloak with `KC_HOSTNAME=https://keycloak.example.com` (and `KC_PROXY_HEADERS=xforwarded` behind a proxy).

**Docker: Ferry can't reach Keycloak by its public name?** Ferry calls the issuer URL from inside its container (discovery, token exchange, keys). The address must work from there *and* be the same one the browser uses. Use a public hostname that resolves inside Docker too (e.g. via `extra_hosts`), not `localhost`.

## Other providers

The same three values — issuer, client ID, secret — plus the redirect URI `https://files.example.com/api/v1/auth/oidc/callback` are all you need.

| Provider | Issuer | Notes |
|---|---|---|
| **Authentik** | `https://authentik.example.com/application/o/<app-slug>/` (keep the trailing slash — it's part of the issuer) | Create an *OAuth2/OpenID Provider* (confidential, redirect URI above) and an *Application* using it. Groups come in the `groups` claim with the default `profile` scope mapping. |
| **Authelia** | `https://auth.example.com` | Add a client under `identity_providers.oidc.clients` with `redirect_uris` set to the callback URL, `scopes: [openid, profile, email, groups]`, `authorization_policy: two_factor` if you like. Set `FERRY_OIDC_SCOPES=openid profile email groups`. |
| **Zitadel** | `https://<instance>.zitadel.cloud` | Web application, *Code* flow, auth method *Basic*. Enable *User Info inside ID Token* or rely on Ferry's automatic UserInfo lookup. |
| **Google** | `https://accounts.google.com` | Google Cloud Console → Credentials → OAuth client ID (*Web application*). Use `FERRY_OIDC_ALLOWED_DOMAINS=yourcompany.com` — otherwise any Google user who matches (or with auto-create, anyone) could get in. |
| **Microsoft Entra ID** | `https://login.microsoftonline.com/<tenant-id>/v2.0` | App registration → *Web* platform with the redirect URI → Certificates & secrets. Add the `email` optional claim, or Ferry falls back to the UserInfo endpoint. Entra doesn't send `email_verified`; if you rely on email matching or auto-create, set `FERRY_OIDC_TRUST_UNVERIFIED_EMAIL=true` (safe for single-tenant apps). |
| **GitLab** | `https://gitlab.com` or your instance URL | Applications → scopes `openid profile email`. |
| **Kanidm, Pocket ID, Dex, Okta, Auth0, …** | Their issuer URL | Any provider with OpenID discovery works. |

## What users see

- **Sign-in page:** a **Sign in with …** button above the email/password form.
- **First sign-in, account connected automatically** (same verified email): they land in Ferry, nothing else to do.
- **First sign-in, no automatic match:** "One more step to connect …" — they sign in once with their Ferry email and password (and two-factor code, if they use it), then confirm **Connect accounts**. **Not now** signs them in without connecting.
- **Settings → Connected accounts:** see the connected provider account, when it was last used, connect it (**Connect …**) or disconnect it.
- **Accounts created by SSO** have no password. They can add one in **Settings → Set a password** — needed for the Ferry Android app, which signs in with email and password. Disconnecting the only sign-in method is blocked until a password is set.
- **Signing out of Ferry** doesn't sign you out of the provider. On a shared computer, also sign out of the provider.

## Administrators

- **Admin → Users** marks connected users with an **SSO** chip. **⋯ → Disconnect SSO** removes the connection (e.g. when an account was connected to the wrong person); set a password first for users who have none.
- Every SSO event is in the **audit log**: `login` (detail *via …*), `oidc_connected`, `oidc_user_created`, `oidc_disconnected`, `oidc_role_synced`, `oidc_login_unmatched`, `oidc_login_denied`.
- **Admin → System** shows the issuer and the create/connect/admin-group settings.
- Disabling a user in Ferry blocks SSO sign-in too.

## Troubleshooting

The sign-in page shows the reason when SSO fails. The server log has the technical detail (search for `oidc`).

| Message or symptom | Cause and fix |
|---|---|
| Provider shows *Invalid parameter: redirect_uri* / *redirect_uri_mismatch* | The redirect URI registered in the provider doesn't match. It must be exactly `<FERRY_PUBLIC_URL>/api/v1/auth/oidc/callback` — same scheme (`https`), host, port and no trailing slash. Set `FERRY_PUBLIC_URL`. |
| Settings won't save: "… is set by FERRY_… in the server environment" | That setting comes from an environment variable, which always wins. Change or remove it in `.env` / `ferry.env` and restart once. |
| "… is not reachable right now" | Ferry can't load `<issuer>/.well-known/openid-configuration`. Check the issuer URL, DNS and firewall from the Ferry machine/container (`curl <issuer>/.well-known/openid-configuration`). Self-signed certificate: use a trusted one, or `FERRY_OIDC_SKIP_VERIFY=true` for testing. Ferry retries on the next attempt; no restart needed. |
| Server log: *issuer did not match the issuer returned by provider* | `FERRY_OIDC_ISSUER` differs from the `issuer` in the discovery document (trailing slash, `/auth` prefix, `http` vs `https`, internal hostname). Copy it from the discovery JSON. For Keycloak set `KC_HOSTNAME`. |
| "Couldn't complete sign-in … Check the client ID and secret" | Wrong client secret or ID, or the client isn't *confidential* while a secret is set (or the other way round). Regenerate the secret and paste it again. |
| "… couldn't be verified" | The ID token is signed with an unexpected key/algorithm, or the server clocks are far apart. Sync time (NTP) on both machines. |
| "The sign-in took too long or was started in another browser" | More than 10 minutes passed, cookies are blocked, or the browser opened the callback in a different profile. Start again from the Ferry sign-in page. Behind a proxy, make sure Ferry is always reached on the same host name. |
| Always asked to "connect" although the emails match | The provider doesn't mark the email as verified (see Keycloak step 3), or `FERRY_OIDC_LINK_BY_EMAIL=false`. |
| "No account yet? Ask your administrator" | Auto-create is off. Create the user in **Admin → Users** with the same email, or set `FERRY_OIDC_AUTO_CREATE=true`. |
| "… already connected to a different Ferry account" | That provider account is connected to someone else. An admin uses **⋯ → Disconnect SSO** on the other account. |
| "Accounts from this email domain can't sign in here" | The email's domain isn't in `FERRY_OIDC_ALLOWED_DOMAINS`. |
| Admin rights don't follow the group | Check that the groups/roles are in the **ID token** (Keycloak: *Add to ID token* on the mapper) and that `FERRY_OIDC_GROUPS_CLAIM` points at them. Roles sync at the next SSO sign-in. |
| SSO user can't sign in to the Android app | The app uses email + password. Set a password in **Settings**. |

## Security notes

- Ferry uses the authorization code flow with **PKCE**, a one-time `state` and `nonce`, and verifies the ID token signature, issuer, audience and expiry.
- **Automatic connection by email trusts your provider's `email_verified`.** Only enable `FERRY_OIDC_TRUST_UNVERIFIED_EMAIL` when users can't set arbitrary emails in the provider — otherwise someone could claim an admin's email and take over their Ferry account.
- **Ferry's own two-factor code isn't asked for SSO sign-ins** — multi-factor authentication is your provider's job. Enable it there (Keycloak: *Authentication → Required actions → Configure OTP*, or a conditional OTP flow).
- Redirects after sign-in only go to pages on your Ferry server.
