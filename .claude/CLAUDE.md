# Member Directory: project notes

The church's people, households and groups. Sibling of `../calendar`,
`../event_planning`, `../production_planning`, `../maintenance_tracking`
and `../user_management`. Same stack and conventions: Go + SQLite (modernc,
no CGO) + server-rendered templates + vendored Stimulus with the no-build
autoloader. No Node, no bundler, no new dependencies. Copy shared pieces
(auth, CSRF, layout, connections, SSO, Ansible) from `../calendar`, the
newest sibling.

- `PLAN.md` is the design and the TODO list. Tick its checkboxes as steps
  are done.
- A **person** (profile) is not a **user** (login). Most people never log
  in. The directory owns people; User Management only owns logins and
  never learns that profiles exist.
- Every shown field goes through the one `Visible()` function: screens,
  print and API alike. Never read a field for display without it.
- Under-13s never get a login: refuse on link, claim and "Can have their
  own login". A birth year with no month/day counts as the youngest age
  that fits. The directory never deletes users.
- Family links (`parent`, `spouse`) are between people and survive moving
  households; grandchildren, siblings etc. are derived, never stored.
  Addresses belong to households (several per household, each member
  assigned one).
- Passwords are never the directory's business beyond local admin resets:
  heads of household can't reset or recover logins.
- Notes are never printed, exported, logged, or sent by the API except
  notes with **Share with sign-ups** ticked. Those go to the event planner
  (read live, never stored there) for the event's team and its printed
  allergy list; see `PLAN.md` "Sign-ups and the API".
- A person added or renamed by anyone who isn't staff, whose name
  matches an existing person (last name, first name or goes-by, and the
  same suffix; no suffix only matches no suffix), is held for staff on
  **Needs action** (`pending_reason = 'name_match'`). Use the one
  name-match function everywhere; never show the person the match.
- Photos are re-encoded on the server (strips EXIF/GPS), stored with
  random names, served only to signed-in viewers who may see them.
- The service worker caches static files only, never people's details.
- Every app must run alone. Links to other apps are optional and off
  unless their URL and token are set (the `Configured()` pattern).
- Cookies `md_*`, tokens `md_…`; local port 8130 (tracker 8080, planner
  8090, User Management 8100, event planner 8110, calendar 8120).
- SSO client ID `directory`; secret `UM_DIRECTORY_SECRET` (Keychain
  `um-directory`). Local login when the SSO connection is off.
- Secrets never go in files or `export` lines in docs; the user keeps them
  in the macOS Keychain and runs make through `~/.zshrc` helpers.
- CSP is `style-src 'self'`: no inline `style=` attributes in templates.
- Deploys onto the maintenance tracker's droplet as `directory.<dns_zone>`;
  the main Caddyfile text must stay identical in every repo's deploy.yml.
- The user runs all `git commit`s themselves (YubiKey signing). Don't commit.
- `MEMBERS_PLAN.md`: this app's part of the website members area (design in `../.claude/plans/members-site.md`): `/api/v1/me/…` for members with User Management tokens, household editing by heads of household (existing rules), signed photo URLs. Tick its checkboxes as steps are done.
