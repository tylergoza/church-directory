# Member Directory: plan

The church's **people**: who's here, which households they belong to, how to
reach them, and which groups they're in. Members browse it on their phone
or print it and bind it. Staff keep it up to date, and heads of household
look after their own households.

Status: steps 1–14 built 2026-10-16 (not yet deployed). Still to do: check printing to PDF in Chrome and Safari (step 10) and register the app in User Management (step 11). The website members area's `/api/v1/me/…` part is in `MEMBERS_PLAN.md`.

## The idea in one paragraph

A **person** (member profile) and a **user** (a login) are different things.
Most people in a church directory will never log in: children, grandparents,
people the office typed in. So the directory owns people, and a login is
something a person *may* be linked to. When the app runs alone, logins are
its own local users. When User Management is connected, logins come from
there and a profile is linked by the user's SSO subject. User Management
never learns that profiles exist. It stays a small service for logins only.

## Who owns what

| App | Owns | Link to the directory |
|-----|------|-----------------------|
| **Member Directory** | People, households, groups, visibility, photos, notes | — |
| User Management | Accounts and per-app roles | Directory signs in through it, lists users for the link picker (optional) |
| Event planner (sign-ups) | Sign-up forms and who signed up (`../event_planning/SIGNUPS_PLAN.md`) | Reads a member's household to tick, and people's age, household and **shared** notes for the event's team (see "Sign-ups and the API" at the end) |
| Production planner / event planner | Their own people lists | Could read directory groups later ("Singers"). Not planned yet |

Groups here are **labels, not permissions**. Being in "Deacons" never gives
anyone rights in this app or any other. Roles stay in User Management (or
in the local users table when the app runs alone).

## Running alone, and joining up later

| Setup | What works |
|-------|-----------|
| Directory alone | Everything below, with local login. Staff link profiles to local users |
| + User Management | Single sign-on, local login off. **Link to user** picks from the users with access to the `directory` app (`GET /api/v1/apps/directory/users`). Turning off an under-13's access uses `PATCH .../users/{id}` with `suspended` |
| + Event planner sign-ups | Members tick people from their household on a sign-up form; the event's team sees age, household and the notes shared with sign-ups, and gets an allergy list |

## Stack and conventions

Same as the siblings. Copy from `../calendar`, the newest, because it has
the connections plumbing built:

- Go, one static binary, templates/JS/CSS embedded. SQLite (modernc, no
  CGO), numbered migrations in `internal/store/migrations/`.
- Server-rendered HTML + vendored Stimulus with the no-build autoloader. A
  PWA (manifest + service worker). The service worker caches **static files
  only**, never directory pages or photos. People's details shouldn't stay
  on a shared tablet after sign-out.
- No new dependencies. Photos use `image`, `image/jpeg` and `image/png`
  from the standard library (see Photos).
- Port **8130**. Cookies `md_*`. Tokens start `md_`.
- SSO client ID `directory`; secret `UM_DIRECTORY_SECRET`, Keychain service
  `um-directory`.
- Deployed by Ansible to the tracker's droplet as `directory.<dns_zone>`,
  service user `directory`, data in `/var/lib/church-directory`, Caddy site
  `/etc/caddy/sites/church-directory.caddy`, binary
  `/usr/local/bin/church-directory`.
- CSP `style-src 'self'` (no inline styles), `img-src 'self' blob:` (the
  photo preview).

## Roles

| Role | Can |
|------|-----|
| `member` | See the directory (only what each person shows), edit their own profile, and edit their households if they're a head of household |
| `editor` | Staff (the office). Also add, edit, archive and delete any person or household, set per-profile visibility and **force hide**, link profiles to users, approve self-made profiles, manage groups |
| `admin` | Also default visibility, settings, connections, API tokens, users (local login) |

"Staff" below means `editor` or `admin`. The directory is **never public**.
Visitors who aren't signed in get the sign-in page and nothing else.

## People, households and groups

### People

- **Name**: first, last, an optional "goes by", and an optional
  **suffix** (Jr., Sr., II, III…). The name always shows, with the suffix
  after it ("Tyler Goza Jr."). The suffix is what tells a father and son
  apart (see Same name as someone already here).
- **Birthday** and **anniversary**: month, day and year are stored
  separately and each one can be left empty. A day needs a month. The day
  is checked against the month, and Feb 29 is allowed when there's no year.
- **Email**, **phone**: one of each, optional.
- **Photo**: optional (see Photos).
- **Kind**:
  - `member`: listed in the directory.
  - `contact`: never listed or printed. For example, a child's other
    parent who doesn't attend. Only staff and the heads of that child's
    other households can see them.
- **Archived** (moved away, passed away): hidden everywhere but staff's
  archive list. **Delete** (staff, with a confirmation) really removes the
  person, their notes and their photo, for privacy requests.
- **Pending**: waiting for staff to approve it. Only the person who made
  it and staff see it until then. Two reasons, stored on the row:
  - `self_signup`: a profile someone made for themselves while self
    sign-up is on.
  - `name_match`: anyone who isn't staff added a person whose name
    matches someone already in the directory (below).

### Same name as someone already here

People get entered twice: staff import or type in a family, someone
forgets to link a member to their profile, and the member (or a head of
household) adds themselves again. So an entry that **matches an existing
person isn't allowed in until staff approve it** (user, 2026-10-14).

- **A match** is the same last name, the same first name or goes-by (either
  one against either one), and the **same suffix**, where no suffix only
  matches no suffix. Compared without case, accents, dots and extra
  spaces, so "Jr" and "Jr." are the same. "Tyler Goza" matches "Tyler
  Goza" and "Ty Goza" who goes by Tyler; it doesn't match "Tyler Goza Jr.".
  Archived people and contacts are matched too (people come back).
- **Who's held**: a person added by someone who isn't staff, through self
  sign-up, a head adding people to their household, or a pending
  household's members. Changing a name (by anyone who isn't staff) so it
  matches someone is held the same way: the old name stays until staff
  approve. Entries with no match go ahead as before (self sign-up is
  still always pending).
- **What they see**: "Waiting for the office to check this." Never the
  matching entry: it may be a contact or hidden from them.
- **Staff** (editors and admins) see it on **Needs action** with the
  matching entries side by side (name, household, birthday, linked user)
  and choose:
  - **Same person**: the held entry goes. The existing person is used
    instead: linked to the user who made it (self sign-up; the usual
    "forgot to link" case), or added to the household it was added to
    (a head adding them). Fields the held entry filled in that the
    existing one has empty are offered, ticked, to copy over.
  - **Different people**: approve it. Staff are asked to add a suffix or
    goes-by to tell them apart, but don't have to. The pair is recorded
    (`not_same_people`) so it isn't flagged again.
  - **Turn down**: the held entry is deleted, with a note to the person
    who made it on their `/me` page.
- **Staff adding someone** aren't held (they're the ones who approve), but
  the form shows the matches first: "Already in the directory: Tyler
  Goza (The Goza Family). Use that one / Add anyway". The CSV import
  (step 13) matches the same way.

### Addresses belong to the household

**Change from the first idea:** addresses are stored on the **household**,
not on each person, and a household can have **more than one**. For
example, grandparents who live in their own house but are part of their
son's household at church. Each address has a label ("Main", "Mom and
Dad's"). Each person in the household is assigned to one of them. This
avoids copies of the same address that drift apart. The household's heads
decide whether each address shows.

The directory card and the printed page group people under their address:

```
The Goza Family
  123 Oak St, Springfield         Tyler, Jane, Sam, Lily
  45 Elm Rd, Springfield          Bob and Carol
```

A child in two households has an address in each one, because the
address is set on each household membership.

### Households

- A household has a name ("The Goza Family"), one or more addresses, an
  optional family photo, and its members. Each member has a role (`head`,
  `adult` or `child`) and an address.
- A household can have **more than one head** (both spouses). Heads are
  set by staff, or by another head of that household.
- One household per person, except for the shared-household case below.
  When someone moves out (an adult child), staff or a head uses **Start
  their own household** (see Family across households).
- **Families show together** as one card in the directory, and printed.

### Family across households

Families don't end when someone moves out. So relationships are stored
**between people**, not only as household membership, and they stay
when someone starts their own household.

- **Relationships**: `parent` (A is a parent of B) and `spouse`. That's
  all that's stored; everything else is worked out from those links:
  - Children: the parent link turned around.
  - Grandparents and grandchildren: parents of parents, children of
    children.
  - Siblings: people who share a parent.
- **Filled in for you**: adding a child to a household suggests its heads
  as parents (ticked, can be unticked; for a step-parent, say). Making a
  second head suggests a spouse link. Staff and heads can edit the links
  for people they can edit.
- **Start their own household** (staff, or a head of the household the
  person is leaving):
  - Makes a new household and moves the person into it, plus their
    spouse and children if picked.
  - Relationship links stay as they were.
  - Until someone in the new household has a login and is its head, the
    person who started it keeps editing it (`managed_by`). That keeps a
    parent looking after a newly-married child's profile until the child
    takes it over.
- **What shows**:
  - A person's page has a **Family** section: parents, spouse, siblings,
    children, grandparents and grandchildren, each with a link to the
    household they're in.
  - A household's page has **Related households**: every household
    where someone has a parent, child or grandparent link to someone in
    this one.
  - So the grandkids show up on the grandparents' page, and the other way
    round.
- **Same visibility as everything else**: names and household links show
  for listed members. Contacts show only to the people who could already
  see them. Archived people show only to staff. The Family section shows
  nothing that isn't already in the directory; it only connects it.

### Children in more than one household

- Staff, or a head of one of the child's households, ticks **Lives in more
  than one household** on the child, with a reason: *parents divorced or
  separated* or *other*.
- Only then can the child be added to a second household (role `child`
  only; adults move instead of being in two). One of the child's households
  is marked **primary**: that's where they're sorted on their own, and in
  the printed index.
- Adding the child to a household **someone else heads**: the head who
  asked sends a request, and a head of the other household or staff accepts
  it. It shows on their `/me` page and on staff's **Needs action** page.
- **Other parent not at the church:** the head can make a **contact
  household** for the other parent: kind `contact`, never listed or
  printed, edited by the head who made it (`managed_by`) and by staff. It's useful for
  pick-ups and emergencies. If that parent starts attending, staff turn the
  household and the person into `member` and make them its head. The head
  who made it then stops being able to edit it.

### Groups

- Staff make groups (Singers, Drama Team, Deacons, Trustees, ...) with a
  name, an optional description, and who can see them: `members` (listed
  in the directory and printed) or `staff` (staff only, e.g. "Needs a
  ride").
- Staff add people (members only, not contacts) with an optional label
  such as *Chair* or *Leader*, and a sort order.
- A group page lists its people with whatever each person shows.

## Visibility

Every shown field goes through **one function**, `Visible(viewer, person,
field)`, and every page, the print view and the API use it. The fields:
`email`, `phone`, `photo`, `birthday.month`, `birthday.day`,
`birthday.year`, `anniversary.month`, `anniversary.day`, `anniversary.year`,
plus each household address and `household.photo` on households.

It decides in this order. The first rule that matches wins:

1. **Not entered**: nothing to show.
2. **Force hidden** by staff: hidden from everyone but staff, the person,
   and their household heads. It's for protective situations, and the
   person can see that it's locked but can't change it.
3. **Day needs month**: the day never shows unless the month does.
4. **The profile's own setting**, if there is one:
   - **Unlinked** profile: set by staff or by a head of the person's
     household.
   - **Linked** profile: set by the person. For someone with role `child`,
     the household's heads can still set it too. Parents keep a hand on
     what a linked teen shows.
5. **The defaults** an admin sets on `/admin/defaults`, one set for adults
   and one for children (role `child` in any household).

Starting defaults, which the admin can change:

| Field | Adults | Children |
|-------|--------|----------|
| Email, phone | Shown | Hidden |
| Photo | Shown | Hidden |
| Birthday month and day | Shown | Hidden |
| Birthday year | Hidden | Hidden |
| Anniversary month and day | Shown | — |
| Anniversary year | Shown | — |
| Household addresses | Shown | — |

A few more rules:

- **When a profile is linked**, the settings already on it (staff's
  overrides) carry over as the person's own starting choices. Nothing
  resets to the defaults.
- **Viewers who always see everything** about a person: staff, the person,
  and the heads of the person's households.
- **Dates display** as "March 14", "March", "March 14, 1985", "March 1985"
  or "1985", depending on what shows.
- **Anniversary on a family card** shows once when both heads have the same
  date and both show it.

## Linking profiles to users

- **Staff link**: on a profile, **Link to user** picks a local user, or a
  User Management user once SSO is on. They can link when making the
  profile or any time after. One user has at most one profile.
- **Claim link**: a head of household turns on **Can have their own login**
  for someone in the household (staff can too). That makes a one-time link,
  valid for 14 days, that the head sends to that person. The person signs
  in (an admin has made their account) and opens the link, and the profile
  is theirs.
- **Self sign-up** (setting, off by default): a signed-in user with no
  profile gets **Make my profile**. They can also make their household and
  its members. Everything stays pending until staff approve it, and a
  name that matches someone already here is shown to staff with the
  match (see Same name as someone already here).
- **Unlink** (staff): the profile stays, the user stays, and the link goes.

### Children under 13

Children under 13 may **not** have their own login. Wherever **Can have
their own login** or **Link to user** appears, show:

> Children under 13 can't have their own login. If we learn that someone
> under 13 has one, it will be turned off and may be deleted.

- **Known age** means there's a birth year. With no month or day, the app
  assumes the youngest age that fits (born Dec 31). If the known age is
  under 13, the app won't turn on login, make or accept a claim link, or
  link a user.
- **Unknown age** (no birth year, which is allowed because people may keep
  the year private): turning login on needs a ticked **This person is 13
  or older**. The app records who ticked it and when.
- **Found later** (a birth year is entered or corrected, or a person
  reports it): the link is removed at once and the profile stays. Then:
  - Local login: the local user is turned off.
  - SSO: when staff do it, the app suspends the person's `directory`
    access through User Management's `PATCH` with the staff member's
    grant. Otherwise it goes on **Needs action** until staff do.
  - Turning off their whole User Management account, or deleting it, stays
    a person's decision in User Management. The directory never deletes
    users.

## Heads of household

A head who has a login (from `/me`):

- Edits the household's name, addresses (add, remove, who lives at which),
  photo, and whether each address shows.
- Adds people to the household. New people start unlinked and as
  `member`. One whose name matches someone already in the directory is
  held for staff (see Same name as someone already here).
- For unlinked members of the household, and for linked children, edits
  their profile, visibility, photo and notes.
- Makes another adult in the household a head.
- Removes unlinked people from the household. Linked adults can only leave
  themselves, or be moved by staff.
- Ticks **Lives in more than one household**, sends join requests, and
  makes contact households.
- Turns on **Can have their own login** and sends the claim link.

Once a linked adult controls their own profile, heads can't edit it, unless
that adult ticks **Let my household's heads edit my profile** on `/me`.
This is for parents or grandparents who'd rather someone else kept their
details up to date. They can untick it any time.

Staff can also set it, either way, and **lock** it so only staff can
change it:

- **On and locked**: for someone who can no longer manage the technology,
  such as an aging parent. It applies to the heads of that person's
  households, and staff write a short reason. The person still sees their profile and the "managed by"
  note on `/me`, and every change the heads make goes in `changes`.
- **Off and locked**: for protective situations, like force hide.

Either way, heads still see what heads always see (see Visibility).

Heads **don't** help with logins. Passwords live in User Management (or in
the directory's local users when it runs alone), and only its admins reset
them. A forgotten password goes to the office.

## Notes

Notes hold information about a person that the church may need later:
allergies, medical needs, dietary needs, accessibility, or anything else.
They're the base for future sign-ups.

- **Kind**: `allergy`, `medical`, `dietary`, `accessibility`, `general`.
  Kinds let a sign-up ask only for what it needs (a potluck wants `allergy`
  and `dietary`).
- **Who can see a note**, set on each note:
  - `household` (the default): the person, their household heads, staff.
  - `private`: the person and staff, not the other household members. For
    example, an adult's own medical note.
  - `directory`: every member, on the person's page.
- **Share with sign-ups**: a separate tick on each note. It's consent for
  the event planner to read this note when this person is signed up for
  an event, show it to that event's team, and put it on that event's
  printed allergy list (the kitchen or children's leaders need it on the
  day). The event planner never stores it. Off by default. The tick says
  so in plain words: "Shown to the team running events this person signs
  up for, including their printed allergy list."
- **Who can write**: the person (if linked), the heads (for people they can
  edit), and staff.
- **No secret notes.** The person (and, for children, their heads) can
  always see every note about them. Pastoral care records are out of scope
  (see Things not to do).
- Notes are **never printed**, never in exports, never in logs, and never
  in the API except a sign-up's read of shared notes. (The event planner's
  allergy list printing shared notes is that consent, not a directory
  print.)

## Photos

- One photo per person and one per household. Uploaded by staff, by the
  person, or by a head (for the people they can edit).
- **In the browser**: a `photo` controller draws the picked file onto a
  canvas, shrinks it to 1200 px on its longest side, and sends a JPEG.
  This handles iPhone HEIC pictures (Safari decodes them) and the photo's
  rotation, and keeps uploads small. A preview shows before saving.
- **On the server**: the upload is capped at 5 MB.
  1. `image.DecodeConfig` refuses anything over 6000 px or that isn't
     JPEG or PNG.
  2. The image is decoded and shrunk again if it's needed, with a small
     area-average resize written here (about 40 lines).
  3. It's saved as a 1200 px JPEG and a 300 px thumbnail. Re-encoding
     **removes EXIF, including GPS location**.
  4. Without JavaScript, upload still works; the photo may come out
     rotated.
- **Storage**: `photos/` in the data directory with random file names (not
  person IDs), and a new name on every replace. They're served only to
  signed-in users at `/photos/{name}` with
  `Cache-Control: private, max-age=31536000, immutable`. A photo whose
  person or household can't be seen by the viewer gets a 404.
- Backups include `photos/`.

## Printing

`/print` builds the whole directory as one page for the browser to print or
save as a PDF. It uses print CSS only, with no PDF library.

- **Options** (in the form, kept in the URL):
  - The whole directory, or one group.
  - Photos on or off.
  - **Leave a gutter for binding**.
  - Paper size: Letter, or half-letter for a booklet.
- **Contents**:
  1. A cover with the church name, logo (setting) and "As of <date>".
  2. Households A–Z by last name, one card each, never split across
     pages.
  3. Groups that members can see.
  4. Birthdays and anniversaries by month (shown fields only).
  5. An index of every person, so children with another last name and
     children in two households can be found.
- **Every page** has a footer: "For members of <church>. Please don't
  share." and the page number (CSS `@page` margin boxes).
- **Binding gutter**: `@page :left` and `:right` get a larger inside
  margin.
- **It's the same `Visible()`**: the print view shows exactly what the
  person printing could see on screen. Contacts, staff-only groups and
  notes are never printed.
- **Setting**: who may print, either everyone (the default) or staff only.
  A printed copy can't be taken back.

## Pages

| Page | Who |
|------|-----|
| `/` directory: search, A–Z households, filter by group | Members |
| `/households/{id}`, `/people/{id}` | Members (only what each person shows) |
| `/groups`, `/groups/{id}` | Members (`members` groups), staff (all) |
| `/birthdays`: this month's and next month's birthdays and anniversaries | Members |
| `/me`: my profile, my households, join requests | Members |
| `/me/claim/{code}` | Signed-in user with no profile |
| `/print` | Members or staff (setting) |
| `/photos/{name}` | Signed in |
| `/admin/people` (filters: unlinked, contacts, pending, archived), `/admin/people/new` | Staff |
| `/admin/households`, `/admin/groups` | Staff |
| `/admin/needs-action`: pending profiles, held name matches (with the matching entries: **Same person** / **Different people** / **Turn down**), join requests, under-13 links to remove or suspend | Staff |
| `/admin/defaults` | Admin |
| `/admin/settings`, `/admin/connections`, `/admin/tokens`, `/admin/users` (local login) | Admin |

## Data model (first migration)

Sketch only. Step 1 settles it, and it stays changeable until the first
deploy.

```sql
people(
  id, first_name, last_name, goes_by, suffix,
  birth_month, birth_day, birth_year,     -- each nullable; day needs month
  anniv_month, anniv_day, anniv_year,
  email, phone, photo,                    -- photo: file name or ''
  kind TEXT CHECK (kind IN ('member','contact')),
  multi_household INTEGER, multi_reason,  -- 'divorced' | 'other'
  user_id REFERENCES users UNIQUE,        -- the link; nullable
  login_allowed INTEGER,
  heads_may_edit INTEGER,                 -- linked adults: opt-in, or set by staff
  heads_may_edit_locked_by REFERENCES users, heads_may_edit_reason,  -- staff lock, on or off
  age_attested_by REFERENCES users, age_attested_at,
  pending INTEGER, pending_reason,        -- 'self_signup' | 'name_match'
  pending_name,                           -- a held rename (JSON: first, last, goes_by, suffix);
                                          -- the person stays listed under the old name until approved
  archived_at,
  created_by, created_at, updated_at)

not_same_people(                          -- staff said "different people"; never flagged again
  person_a REFERENCES people, person_b REFERENCES people,  -- a < b
  decided_by REFERENCES users, decided_at)

households(
  id, name, photo, kind ('member'|'contact'),
  managed_by REFERENCES people,           -- contact households, and new ones from
                                          -- "Start their own household", until a
                                          -- linked head exists
  archived_at, created_at, updated_at)

household_addresses(
  id, household_id, label, address1, address2, city, state, postal,
  shown INTEGER, force_hidden INTEGER,    -- nullable shown = use the default
  position)

household_members(
  household_id, person_id, role ('head'|'adult'|'child'),
  address_id REFERENCES household_addresses,  -- same household; nullable
  is_primary INTEGER, position,
  PRIMARY KEY (household_id, person_id))

relationships(                            -- kept when people change households
  person_id, related_id,
  kind ('parent'|'spouse'),               -- parent: person_id is a parent of related_id
  PRIMARY KEY (person_id, related_id, kind))

household_requests(                       -- add a child to someone else's household
  id, person_id, household_id, asked_by, asked_at,
  answered_by, answered_at, accepted INTEGER)

field_settings(                           -- per-profile visibility
  person_id, field, shown INTEGER,        -- nullable shown = use the default
  force_hidden INTEGER, set_by, set_at,
  PRIMARY KEY (person_id, field))
household_settings(household_id, field, shown, force_hidden, set_by, set_at)  -- photo; addresses carry their own
defaults(audience ('adult'|'child'), field, shown, PRIMARY KEY (audience, field))

groups(id, name, description, visible_to ('members'|'staff'), position, archived_at)
group_members(group_id, person_id, label, position, PRIMARY KEY (group_id, person_id))

notes(
  id, person_id, kind, body,
  visible_to ('household'|'private'|'directory'),
  share_with_signups INTEGER,
  created_by, created_at, updated_by, updated_at)

claims(code_hash, person_id, made_by, expires_at, used_at)

changes(id, person_id, household_id, by_user, what, at)  -- who changed what, no values

users(...)            -- local users / SSO cache, as the siblings (sso_subject)
settings(key, value)  -- self_signup, print_for, church_name, logo, ...
api_tokens(...)       -- as the calendar
```

## Settings

Server-only (environment):

| Var | Default | Use |
|-----|---------|-----|
| `ADDR` | `:8130` | |
| `DB_PATH` | `data/directory.db` | Photos go in `photos/` next to it |
| `DEV`, `TRUST_PROXY`, `TZ` | | As the siblings |

In the app (`/admin/settings`): church name and logo (cover page),
**self sign-up** (off), **who may print** (everyone).

Connections on `/admin/connections`, as in `../event_planning/CONNECTIONS_PLAN.md`:

| Connection | Env override | Use |
|------------|--------------|-----|
| Single sign-on | `SSO_URL`, `SSO_INTERNAL_URL`, `SSO_CLIENT_ID`, `SSO_CLIENT_SECRET`, `BASE_URL` | Off = local login |

## Sign-ups and the API

Designed in `../.claude/plans/event-signups.md` (2026-10-14); the event
planner's side is `../event_planning/SIGNUPS_PLAN.md` step 6. Read-only,
with app tokens (kind `app`) made on `/admin/tokens`:

- `GET /api/v1/people/by-user/{sub}`: the user's profile and the people
  they may sign up. That's everyone in households where they're a head,
  otherwise only themselves. Each with ID, display name, household role
  (`head` / `adult` / `child`) and age in years when the birth year is
  known. Archived and `contact` people are left out.
- `GET /api/v1/people?ids=1,2,3&notes=allergy,dietary,medical`: for the
  event's team pages, one call per page. Each person's display name, age
  in years (when known), household name, and **only** notes of those
  kinds with **Share with sign-ups** ticked. At most 500 IDs. Unknown or
  archived IDs come back as `{"id": 9, "gone": true}`.
- `GET /api/v1/groups`, `GET /api/v1/groups/{id}`: for apps that want
  "Singers" etc.

Age (settled 2026-10-14): age in years when the birth year is known, shown
only to the event's team in the event planner (never public, never on the
form). Never the birthday itself. Grade isn't stored.

No search endpoint: the event planner doesn't match typed names to
directory people.

## Things not to do (yet)

- **Pastoral care notes** (visits, prayer requests, counselling). These
  are a different kind of record, with different rules about who sees
  them. If it's wanted, it's its own design.
- **Email or text blasts** to groups. Show the addresses and let people
  use their own mail.
- **vCard** (`.vcf`) download of a person or household. Cheap to add later
  through `Visible()`.
- **Offline directory** in the PWA (see Stack).
- **More than one phone or email** per person.
- **Adults in two households.** Move them instead.

## Steps

Each step is a working app; stop after any of them.

### 1. Skeleton
- [x] `go mod init`, copy layout, CSRF, sessions, local login, `/setup`, rate limiter, PWA bits (static-only service worker), Stimulus autoloader, `internal/secrets` and `internal/connections` from `../calendar` (SSO left out until step 11)
- [x] Migration `001_initial.sql` with the tables above
- [x] Roles `member`, `editor`, `admin`; `requireStaff` / `requireAdmin`
- [x] `Makefile` (`dev`, `test`, `build`), `.gitignore`, `README.md` (with the running alone / connected table), `LICENSE`
- [x] `go vet ./... && go test ./...` pass

### 2. People and households (staff)
- [x] `/admin/people`: add, edit, archive, delete (with confirmation), kinds `member` / `contact`, filters
- [x] Suffix on names; the one name-match function (case, accents, dots, spaces, goes-by, suffix) and `not_same_people`; staff's add form shows matches first (**Use that one** / **Add anyway**)
- [x] Partial dates: form (month / day / year), validation (day needs month, day fits the month, Feb 29 with no year), display helper
- [x] Households: add, edit, more than one address with labels, who lives at which, members with roles, more than one head
- [x] `changes` written on every edit
- [x] Tests: partial-date validation and display (table test), moving people, delete removes notes and photos, name matching (table test: "Jr" = "Jr.", no suffix ≠ "Jr.", goes-by against first name, accents, archived and contacts matched, a `not_same_people` pair not flagged)

### 3. Visibility and the directory
- [x] `Visible()` with the five rules, a table test per rule, plus "who always sees everything"
- [x] `/admin/defaults` (adults / children), per-profile settings and **force hide** on the staff edit page
- [x] `/`, `/households/{id}`, `/people/{id}`, search, A–Z, `/birthdays`. Family cards with the shared-anniversary rule
- [x] Tests: contacts never listed, archived never listed, pending only for self and staff, children get child defaults

### 4. Linking and logins
- [x] **Link to user** / **Unlink** (local users for now), one profile per user, staff overrides carry over on link
- [x] **Can have their own login**, the 13+ tick, the under-13 warning, claim links (`/me/claim/{code}`, one-time, 14 days, stored hashed)
- [x] Under-13 checks on link, on claim and on every birthday save; `/admin/needs-action`
- [x] Self sign-up setting, pending profiles and households, approve / turn down
- [x] Held name matches on **Needs action**: matching entries side by side, **Same person** (links the user to the existing profile, offers empty fields to copy), **Different people** (approve, suffix or goes-by suggested, pair recorded), **Turn down** (note on `/me`)
- [x] Tests: youngest-age rule for year-only birthdays, under-13 refused everywhere, a found-later under-13 is unlinked, claim links can't be reused, a matching self sign-up held and **Same person** links the user without making a second profile, the person never sees the matching entry

### 5. Heads of household
- [x] `/me`: my profile (edit, visibility), my households
- [x] Head rights as listed above, including editing linked children, not editing linked adults, and **Let my household's heads edit my profile** (staff can set it on or off and lock it, with a reason)
- [x] **Lives in more than one household**, primary household, join requests (send, accept, turn down), contact households
- [x] A head adding a person, or renaming one, to a name that matches is held (`name_match`, or `pending_name` for a rename); **Same person** adds the existing person to the household instead
- [x] Tests: head rights table (head / adult / child × linked / unlinked), a second household refused without the tick, contact households never listed, a head's matching add held while a non-matching one goes straight in, a held rename keeps the old name until approved

### 6. Family across households
- [x] `relationships` editing (parent / spouse), suggested parents and spouse when adding children and heads
- [x] **Start their own household**: move the person (and picked spouse and children), keep links, `managed_by` until a linked head exists
- [x] **Family** section on person pages (parents, spouse, siblings, children, grandparents, grandchildren) and **Related households** on household pages
- [x] Tests: grandchildren found from grandparents and back after a move, contacts and archived people hidden from those who couldn't see them, `managed_by` rights end when a linked head is made

### 7. Groups
- [x] `/admin/groups`: add, edit, archive, members with labels and order, `members` / `staff` visibility
- [x] `/groups`, `/groups/{id}`, group filter on `/`
- [x] Tests: staff groups hidden from members, contacts can't be added

### 8. Notes
- [x] Notes on a person: kinds, `household` / `private` / `directory`, **Share with sign-ups**
- [x] Who can write and who can see, as listed above
- [x] Tests: a table test of viewer × visibility (done); "notes never in print output" waits for step 10 (print) - see the comment in `internal/access/notes.go`

### 9. Photos
- [x] `photo` controller (canvas resize to JPEG, preview), upload form that works without JS
- [x] Server: size cap, `DecodeConfig` limits, area-average resize, 1200 px + 300 px JPEG, random names, old file removed on replace
- [x] `/photos/{name}` behind sign-in with the visibility check; CSP `img-src 'self' blob:`
- [x] Tests: EXIF/GPS gone after upload (a fixture with GPS), oversized refused, hidden photo 404s

### 10. Printing
- [x] `/print` with the options above; print CSS: cards not split, `@page` sizes, gutter, footer and page numbers
- [x] Cover, households, groups, birthdays and anniversaries by month, person index
- [x] **Who may print** setting
- [ ] Check by printing to PDF in Chrome and Safari, Letter and half-letter

### 11. Single sign-on
- [x] Copy `internal/sso` from the calendar; `RoleFromSSO` maps `member` / `editor` / `admin`; the **Single sign-on** connection section with **Test sign-in**; `local-login on|off` break-glass
- [x] **Link to user** lists User Management's users with `directory` access; links by `sso_subject`
- [x] Under-13 found later: suspend `directory` access with `PATCH` and the acting staff member's grant
- [ ] Register in User Management (`../user_management/DIRECTORY_PLAN.md`)

### 12. Deploy
- [x] `deploy/ansible` copied from the calendar: service `church-directory`, user `directory`, `/var/lib/church-directory`, Caddy site file, `directory.<dns_zone>`
- [x] Main Caddyfile text identical to the other repos' `deploy.yml`
- [x] Backups of `/var/lib/church-directory`, including `photos/` and `secrets.key`

### 13. Bring in the existing directory (optional)
- [x] CSV import for staff (`encoding/csv`): preview, match by name (the same name-match function, so suffixes count), make households by address, nothing saved until **Import**

### 14. Sign-ups API (designed: `../.claude/plans/event-signups.md`, used by `../event_planning/SIGNUPS_PLAN.md` step 6)
- [x] App tokens (kind `app`) on `/admin/tokens`
- [x] `GET /api/v1/people/by-user/{sub}` (household to tick, roles, age) and `GET /api/v1/people?ids=…&notes=…` (team pages), both through `Visible()` plus the **Share with sign-ups** rule for notes
- [x] **Share with sign-ups** tick wording as in Notes
- [x] Tests: unshared notes never returned, contacts and archived left out, a non-head gets only themselves, age from a year-only birthday, no birthday in the output
