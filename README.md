# Member Directory

The church's **people**: who's here, which households they belong to, how to
reach them, and which groups they're in. Members browse it on their phone or
print it and bind it. Staff keep it up to date, and heads of household look
after their own households.

It works on its own. See `PLAN.md` for the design and the list of steps;
only step 1 (the skeleton: sign-in, roles, database) is built so far.

A **person** (a profile) is not a **user** (a login). Most people never log
in. The directory owns people; logins are local users, or come from User
Management once that's connected. The directory is never public: everything
except the sign-in page needs a login.

Built the same way as the other church apps:

- **Backend:** Go, one static binary with its templates, JS and CSS embedded
- **Database:** SQLite (one file, pure-Go driver, no CGO)
- **Frontend:** server-rendered HTML + [Stimulus](https://stimulus.hotwired.dev), vendored, with the
  no-build autoloader. No Node, no bundler. Installable as an app (PWA); the
  service worker caches static files only, never people's details.

## Running alone, and joining up later

| Setup | What works |
|-------|-----------|
| Directory alone | Everything in `PLAN.md`, with local login. Staff link profiles to local users |
| + User Management | Single sign-on, local login off. **Link to user** picks from the users with access to the `directory` app. Turning off an under-13's access uses User Management's `PATCH` with `suspended` |
| + Event planner sign-ups | Members tick people from their household on a sign-up form; the event's team sees age, household and the notes shared with sign-ups, and gets an allergy list |

Connections are off until they're set up (on `/admin/connections` or with
environment variables), and the directory acts as if the other app doesn't
exist. The sign-ups API is step 14.

## Quick start

Needs Go 1.27 or newer.

```sh
make dev     # http://127.0.0.1:8130, edits to web/ show on refresh
make test    # go vet and go test
make build   # bin/church-directory
```

On a new install, **Sign in** goes to `/setup` to create the first
administrator. It runs on port 8130, so the other church apps can run
alongside it.

## Roles

| Role | Can |
|------|-----|
| `member` | See the directory (only what each person shows), edit their own profile, and edit their households if they're a head of household |
| `editor` | Staff (the office): also add, edit, archive and delete any person or household, set visibility, link profiles to users, approve profiles, manage groups |
| `admin` | Also default visibility, settings, connections, API tokens, users (local login) |

## Settings

Server-only (environment, or the matching flag):

| Var | Default | Use |
|-----|---------|-----|
| `ADDR` | `:8130` | Listen address |
| `DB_PATH` | `data/directory.db` | The database. Photos go in `photos/` next to it, and `secrets.key` too |
| `DEV` | | `1` serves templates and static files from `./web` and reloads on each request |
| `TRUST_PROXY` | | `1` trusts `X-Forwarded-For` and `X-Forwarded-Proto` from a reverse proxy |
| `TZ` | | The server's time zone |

In the app (`/admin/settings`): the site name for now; church name and
logo, **self sign-up** (off) and **who may print** (everyone) arrive with
the steps that use them.

Connections on `/admin/connections` (an environment variable set on the
server wins over what's saved there):

| Connection | Env override | Use |
|------------|--------------|-----|
| Single sign-on | `SSO_URL`, `SSO_INTERNAL_URL`, `SSO_CLIENT_ID`, `SSO_CLIENT_SECRET`, `BASE_URL` | Off = local login |

## Single sign-on

People sign in through User Management (the `directory` app there, roles
`member`, `editor`, `admin`; any other role means no access). The directory
only caches who they are (`users.sso_subject`); profiles never go to User
Management.

- Register the app on User Management's Apps page (client ID `directory`,
  redirect URI `<this app's address>/auth/callback`), then fill in
  **Single sign-on** on `/admin/connections`. Saving only stores the values as
  pending: press **Test sign-in** and come back as an admin of the directory
  and they go live. A wrong value can't lock everyone out.
- The client secret is kept in the macOS Keychain as `um-directory`. The
  deploy gets it as `UM_DIRECTORY_SECRET` (through the `with-um` helper in
  your `~/.zshrc`); it is never written in a file or typed as an `export`
  line. The server reads it as `SSO_CLIENT_SECRET`, with `SSO_URL`,
  `SSO_INTERNAL_URL`, `SSO_CLIENT_ID` and `BASE_URL`, which override the
  saved settings.
- With single sign-on on, **Link to user** lists the people User Management
  says have `directory` access and links by their User Management ID; the
  user row here is made the first time someone is picked or signs in.
- If a linked user turns out to be under 13, the staff member's own sign-in
  grant is used to suspend them in User Management (`PATCH
  /api/v1/apps/directory/users/{id}`), and the item is closed. If that
  isn't possible (local login, User Management down, refused) it stays on
  **Needs action** with a **Suspend in User Management** button. The
  directory never deletes users.
- Break-glass when User Management is down: on the server run
  `church-directory local-login on`, give an admin a password with
  `reset-password NAME`, and turn it off again afterwards. `church-directory
  connection clear sso` forgets saved settings that are wrong.

## Commands

Flags such as `-db` go before the command.

```sh
church-directory                          # start the web server
church-directory create-user NAME [--role member|editor|admin | --admin]
                                          # create a user (prompts for a password)
church-directory reset-password NAME      # set a new password for a user
church-directory local-login on|off       # break-glass: password sign-in even with single sign-on on
church-directory connection clear ID      # break-glass: forget a connection's saved settings
church-directory backup FILE              # write a consistent copy of the database
```

`/admin/backup` downloads the same copy. Copy `secrets.key` from next to the
database too, or saved connection secrets have to be entered again.

## Security notes

- Passwords are bcrypt-hashed; sign-in is rate limited per address.
- Forms are protected by a CSRF token and `SameSite=Lax` cookies (`md_*`).
- The Content-Security-Policy is `style-src 'self'` (no inline styles) and
  `img-src 'self' blob:` (the photo preview).
- API tokens start with `md_` and only a hash is stored.
- Nothing about a person is cached by the service worker.

## API

Read-only, for other church apps. Make an app token on `/admin/tokens` and
send `Authorization: Bearer md_…`. Answers are JSON, `Cache-Control:
no-store`, errors are `{"error", "code"}`. No cookies.

- `GET /api/v1/people/by-user/{sub}` (SSO subject): `{"profile", "people"}`,
  each `{id, name, role, age?, household}`. A head gets their households,
  anyone else only themselves.
- `GET /api/v1/people?ids=1,2&notes=allergy,dietary,medical`: at most 500
  ids; `{"people": [{id, name, age?, household, notes: [{kind, body}]}]}`.
  Only notes with Share with sign-ups ticked. Unknown, archived, pending or
  contact IDs come back `{"id": 9, "gone": true}`.
- `GET /api/v1/groups`, `GET /api/v1/groups/{id}`: groups visible to
  members, with `{id, name, label}` for people.

## Layout

```
main.go                  commands and server start
internal/store/          SQLite: migrations, users, sessions, settings, tokens
internal/server/         routes, sessions, CSRF, handlers
internal/secrets/        sealing saved secrets with secrets.key
internal/connections/    links to other apps: environment first, then settings
web/                     templates, CSS, vendored Stimulus, service worker
deploy/                  Ansible and Caddy
```
