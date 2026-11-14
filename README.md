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

## Registering with User Management

Before turning SSO on, register this app from the `user_management` repo.
Secrets live in the macOS Keychain and reach `make` through the `with-*`
helpers in `~/.zshrc`: `with-do-church` (DigitalOcean token, service
`digitalocean-church`) and `with-um` (the User Management client secrets).

`with-um` needs one more line for this app: `UM_DIRECTORY_SECRET`, read from
the Keychain service `um-directory` the same way as the others.

```sh
# in ../user_management (set directory_url in its vars.yml first)
with-do-church make register-app APP=directory                             # dry run: shows what would change
with-do-church make register-app APP=directory DRY_RUN=false               # registers; saves the secret in the Keychain as um-directory
with-do-church make register-app APP=directory DRY_RUN=false ROTATE=true   # lost or leaked secret: a new one, saved over the old
```

The SSO client ID is `directory`. A new secret stops the old one working at
once, so deploy here straight after rotating.

## Deploying

The app is one binary plus one `.db` file, `secrets.key` and a `photos/`
folder beside it, behind [Caddy](https://caddyserver.com) for HTTPS.
`deploy/` has the systemd unit (`deploy/church-directory.service`) and a
Caddyfile (`deploy/Caddyfile`) for doing it by hand.

### DigitalOcean with Ansible

`deploy/ansible/` works like the other apps', and puts the directory on an
**apps box** (`apps_box`, default `apps-1`) shared with the other apps, behind
a separate edge box that runs Caddy for every app
([edge_proxy](../edge_proxy)). It finds the box by tag and adds itself
alongside, on port 8130, with its own service user (`directory`) and data
folder (`/var/lib/church-directory`), at `directory.<dns_zone>`. Nothing
private is committed: the API token comes from the environment, droplets are
found by tag on every run, and your domain and settings go in the gitignored
`deploy/ansible/vars.yml`.

You need Ansible on your machine, Go, a DigitalOcean API token with read/write
scope, your domain added under **Networking → Domains** in DigitalOcean with
the registrar's nameservers pointed at it, and the edge box provisioned first
(edge_proxy's `make provision`, which also makes the firewalls).

```sh
cp deploy/ansible/vars.example.yml deploy/ansible/vars.yml   # set app_domain, dns_zone
with-do-church make provision  # first time
with-do-church make deploy     # every update after that
```

`with-do-church` runs a command with `DIGITALOCEAN_TOKEN` set for the church
account. Set `do_team` in `vars.yml` to the account's team name and every play
stops if the token belongs to another account.

`make provision` creates the apps box if no other app has (weekly droplet
backups on), tags it `apps` and `apps-1` plus `do_extra_tags` (the `ssh` tag
is what edge_proxy's ssh firewall targets), adds a 1 GB swap file and
monitoring alerts at 80% memory and disk, points the app's DNS A record at the
edge box (never at the apps box; `manage_dns: false` skips it), installs
updates and the service user, and runs the deploy. There are no firewalls
here: edge_proxy makes them, including `apps-internal`, which lets only the
edge box reach the apps' ports.

`make deploy` runs the tests, builds the Linux binary here, backs up when the
binary changes (keeping 10 sets: the database, `secrets.key` and `photos/`
together, as `pre-deploy-<time>.db`, `.key` and `.photos.tar.gz` in
`/var/lib/church-directory/backups`), and installs it with
`deploy/church-directory.service`. The unit listens on the box's VPC address (a
drop-in sets `ADDR`), not on a public port. A second play writes the app's
Caddy site, `/etc/caddy/sites/church-directory.caddy`, on every edge box: it
proxies the domain to the VPC address, then validates the whole config and
reloads Caddy. The deploy stops before building if there's no edge box, and
fails if the app doesn't answer `/healthz` on the VPC address afterwards.

On the first deploy the admin user is created before the app starts, so
`/setup` is never exposed. Set `DIR_ADMIN_PASSWORD` to choose its password,
or a random one is printed at the end. With `sso_url` set there's no local
admin to create: people come from User Management.

The SSO connection is optional in `vars.yml`. Leave it blank to set it up in
the app on `/admin/connections` (the usual way): sign in with the local
admin, fill in **Single sign-on**, press **Test sign-in**. To fix it in
`vars.yml` instead, set `sso_url` and `base_url` and deploy through
`with-um` so `UM_DIRECTORY_SECRET` is set; the deploy stops before building
if `sso_url` is set without the secret or `base_url`. The secret goes only
into the root-only systemd drop-in. To turn it back off, remove `sso_url`
and deploy again.

With a hardware SSH key, use a deploy key as described in the tracker's
README (`ssh_private_key_file` in `vars.yml`; the same key works for all the
apps). This app's own `make backup` (below) copies its database, `secrets.key`
and `photos/` to your machine; there are also the pre-deploy backups and the
weekly droplet snapshots.

### Moving between hosts

Backup and restore are separate steps that go through this machine, so they
work between DigitalOcean accounts and need no network path between the boxes.
Backups land in `~/Backups/mountainview/church-directory/<UTC time>/`
(`local_backup_dir`; folders 0700, files 0600, outside every repo), holding
`directory.db`, `secrets.key`, `photos.tgz` (the `photos/` folder) and a
`manifest.yml` with the source, time, binary checksum and each file's sha256.
Those three are all the app stores. A missing or empty `photos/` doesn't fail
the backup; a missing one is just left out.

```sh
# Copy from a box. FROM is an IP (needs only SSH, no token) or a droplet name
# (looked up in the token's account). The app keeps running.
make backup FROM=203.0.113.10
# The final copy: stops the app first (so the photos are archived after it
# stops) and leaves it stopped and disabled.
make backup FROM=203.0.113.10 STOP=true

# Put a backup on the apps box. Provision first, without starting the app.
with-do-church make provision RESTORE_PENDING=true
with-do-church make restore BACKUP=latest      # or BACKUP=<dir>
```

`RESTORE_PENDING=true` installs the binary and unit but doesn't start the
app or create an admin, because restore refuses to replace a database and the
app would make an empty one on first start. Restore checks the manifest's
checksums, installs the database and key owned by the service user at mode
0600, unpacks `photos.tgz` into `photos/` (owned by the service user), removes
stale `-wal`/`-shm` files, starts the app and waits for `/healthz`. If the box
already has a database it refuses, unless `FORCE=true`, which first saves
`pre-restore-<time>.db`, `.key` and `.photos.tar.gz` in the box's backups
folder, then replaces `photos/` rather than merging so it matches the backup.
A backup with no `photos.tgz` leaves the box's photos alone.

To move to another apps box in the same account, set `apps_box` in `vars.yml`
to the new box and run `with-do-church make move FROM=apps-1`. It
backs up the old box and stops the app there, provisions the new one with
`RESTORE_PENDING=true`, restores, and rewrites the edge site file for the new
address. DNS isn't touched. `move` refuses when `FROM` is the current
`apps_box`; pass `FORCE=true` to replace a database already on the new box.
The old box keeps its data folder as a fallback, and moving back is the same
command the other way.

By hand, without Ansible: **Settings → Download backup** (or the `backup`
command) writes a consistent copy of the database while the app runs. Stop the
app on the new host, put the file at its database path
(`/var/lib/church-directory/directory.db`, owned by `directory`), copy
`secrets.key` and the `photos/` folder next to it, and start it. Without
`secrets.key` the saved connection secrets have to be entered again; without
`photos/` the photos are gone.

**Changing domain.** List the old and new host names as `address_rewrites` in
`vars.yml`, and each `make restore` swaps them in the database before the app
starts. It's all or nothing: a failure leaves the database as it was and stops the
restore. Names not listed are left alone.

## Layout

```
main.go                  commands and server start
internal/store/          SQLite: migrations, users, sessions, settings, tokens
internal/server/         routes, sessions, CSRF, handlers
internal/secrets/        sealing saved secrets with secrets.key
internal/connections/    links to other apps: environment first, then settings
web/                     templates, CSS, vendored Stimulus, service worker
deploy/                  systemd unit, Caddyfile, ansible/ (provision, deploy, backup, restore, move)
```
