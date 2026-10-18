# Member Directory: the members API for the website

The cross-app design is in `../.claude/plans/members-site.md`; this is the
directory's part. Everything still works with the directory alone: with
User Management not connected, the `/api/v1/me/…` API is off and nothing
else changes.

Status: planned 2026-10-16, not built.

The directory itself (`PLAN.md`) is built as of 2026-10-18, so the steps
this builds on are in place: it needs `Visible()` (PLAN step
3), `/me` household editing (step 5), notes (step 8), photos (step 9) and
single sign-on (step 11). Do those first. No migration is edited; this
plan adds `002_members_api.sql` after the first deploy, or folds into
`001` if nothing has deployed yet.

The website is a static members area. It signs in with User Management,
gets a 15-minute token for the `directory` app and calls this API straight
from the browser. The direction stays one way: the website depends on the
directory; the directory never depends on the website.

## The contract (User Management's, used exactly as written)

- Tokens start `umd_` and arrive as `Authorization: Bearer umd_…`.
- Check one with `GET /api/v1/token` on User Management, using this app's
  Basic auth (SSO client ID `directory` and its secret) and an `X-Token`
  header. A good answer is
  `{"user": {sub, username, name, email, role}, "via": "website"}`.
- Cache a good answer for **up to 60 s**, keyed by a hash of the token.
  A 401 means refuse (and don't cache it as good).
- The role in the answer is the person's role in the `directory` app
  (`member`, `editor`, `admin`, and later `guest`). Check it per call;
  never assume "signed in = member".

## What changes, in short

| | Now | After |
|--|-----|-------|
| Who can call the directory from outside | Signed-in browser pages (cookies) and app tokens `md_…` for the event planner | Also `/api/v1/me/…` with a `umd_…` Bearer token from the website. No cookies, no CSRF |
| Household editing | Heads of household, unchanged | Same rules on `/me` and through the API: heads edit the household and its unlinked and child members; other adults can view only |
| Photos | `/photos/{name}`, cookie sign-in | Also short-lived **signed photo URLs** in `/me` responses, because `<img>` can't send a Bearer header |
| Cross-origin | None | CORS on `/api/v1/me/…` for the website origin only, a setting on the User Management connection, never `*` |
| Settings | Single sign-on connection | Adds **Website origin**, and shows **Accepts tokens from** read-only (User Management owns it) |
| Without User Management | Local login | Unchanged. `/api/v1/me/…` answers 404 |
| `guest` (later) | Not a role | Planned: gets only their own household. Design for it now |

## Steps

### 1. Token check and the `/me` plumbing
- [ ] `internal/sso`: `CheckToken(token)` calls `GET /api/v1/token` with Basic auth and `X-Token`; a small in-memory cache (token hash, answer, expiry at most 60 s), cleared when the process restarts. A 401 or an error means refuse. Copy the HTTP client and timeouts from the existing SSO code
- [ ] Middleware `requireMeToken` for `/api/v1/me/…`: reads only `Authorization: Bearer umd_…`, **ignores cookies completely** (no session, no CSRF on these routes), refuses a token whose `via` is not `website`
- [ ] Find or create the local `users` row by `sso_subject`, through the same function SSO sign-in uses (`RoleFromSSO` for the role); never a second copy of that code. Link to the person's profile by `user_id` as sign-in does
- [ ] The viewer passed to `Visible()` is built from that user exactly as for a page request (staff or member, their profile, their households)
- [ ] Off unless set up: `/api/v1/me/…` is not registered, and answers 404, while the SSO connection is off or **Website origin** is empty
- [ ] **Website origin** (`settings`) on `/admin/connections` under Single sign-on, with **Test** reusing the token-check call; env overrides like the other connection values. **Accepts tokens from** is owned by User Management (`apps.accepts_from`, which refuses to delegate to an app that doesn't accept the website); show it read-only, or a link to User Management's Apps page. No local copy, same as `../calendar/MEMBERS_PLAN.md` and `../event_planning/MEMBERS_PLAN.md`
- [ ] Errors are JSON `{"error": "...", "code": "..."}`; 401 for a bad or missing token, 403 `no_role` when the user has no role here (`guest` handled in step 8)
- [ ] Rate limit per user (the existing limiter), on top of the 60 s cache

### 2. CORS, no cookies, idempotency
- [ ] CORS middleware on `/api/v1/me/…` only: allows the one **Website origin** from the connection (exact match, never `*`, no credentials mode), methods `GET, POST, PUT, PATCH, DELETE, OPTIONS`, headers `Authorization, Content-Type, Idempotency-Key`; `Vary: Origin`. A request from any other origin gets no CORS headers; the preflight answers 403
- [ ] No `Set-Cookie` on these responses and no cookie read; `Cache-Control: no-store` on every `/me` response (people's details are never cached by the browser, the service worker or a proxy)
- [ ] `Idempotency-Key` required on every write (400 without one): table `idempotency(user_id, key, method, path, status, body_hash, response, created_at)` in the migration; the same key and the same request replays the saved response, the same key with a different body is 422; rows older than 24 h are deleted by the existing background tick. The saved response is the already-filtered one (never raw data)
- [ ] The service worker is unchanged: static files only; add nothing that touches `/api/` and keep a test that it never caches an API response

### 3. Read: search, profile, households (all through `Visible()`)
- [ ] `GET /api/v1/me`: the viewer's role here, their profile, their household ids, and whether they can edit each household (the heads rule below)
- [ ] `GET /api/v1/me/people?q=&group=&limit=&cursor=`: the same search, A-Z and group filter as the `/` page, calling the page's store query, not a copy. Every field in the JSON comes from `Visible(viewer, person, field)`; a field that isn't visible is **omitted**, not blanked
- [ ] `GET /api/v1/me/households?q=` and `GET /api/v1/me/households/{id}`: household card with name, addresses (each through `Visible()`), members grouped by address, heads first, related households. Contacts, archived and pending people follow the page rules
- [ ] `GET /api/v1/me/people/{id}`: person page data, including the Family section
- [ ] `GET /api/v1/me/profile` (my profile, edit view): all my fields with my own visibility setting per field; my households; my pending join requests and notes I may see
- [ ] A person or household the viewer may not see is **404**, never 403 (no hint it exists)
- [ ] Responses are built by one `meView` function per resource that takes the viewer and cannot be handed a raw row; a test (step 9) fails if a handler returns a store type directly
- [ ] Never in these responses: notes of other people unless the note rules allow it (step 6), logins, `user_id`, `sso_subject`, `age_attested_*`, the name-match entry that held someone

### 4. Write: my profile and my household(s), heads of household (existing rules)
- [ ] One function `canHeadEditHousehold(viewer, household)` = the viewer's linked profile has role `head` in it, exactly the rule `/me` already uses (same function, not a copy). Staff keep their staff rights. Other adults, children with a login and `guest` logins return false and can only view. Used by `/me` pages **and** the API handlers
- [ ] `canEditPerson(viewer, person)` keeps the existing rules: the person themselves; for an **unlinked** person or a **linked child**, a head of one of their households; for a **linked adult**, heads only with **Let my household's heads edit my profile** (`heads_may_edit`) on (or set by staff), and never when staff locked it off
- [ ] `PATCH /api/v1/me/profile`: my fields and my per-field visibility (same validation as the page: partial dates, day needs month, email and phone checks, under-13 rules, `changes` row written)
- [ ] `PATCH /api/v1/me/households/{id}`: name, family photo, and per-address `shown`
- [ ] Addresses: `POST/PATCH/DELETE /api/v1/me/households/{id}/addresses[/{aid}]` (label, lines, `position`); deleting one with members needs `reassign_to`, as the page does. Several per household; each member assigned one with `PATCH …/members/{pid}` `{address_id}`
- [ ] Members: `POST /api/v1/me/households/{id}/members` adds a **new** person (children included; new people unlinked and kind `member`), by a head, `PATCH …/members/{pid}` changes their role (`head` / `adult` / `child`) and address, `DELETE …/members/{pid}` removes an unlinked person (linked adults can only leave themselves)
- [ ] Editing a person: `PATCH /api/v1/me/people/{id}` calls the same store function as the `/me` edit page after `canEditPerson`; the name-match function holds a matching add or rename for staff (`name_match`, `pending_name`) and the response says only "Waiting for the office to check this." and never the match
- [ ] The rules that stay: no login for under-13s (the API cannot turn on **Can have their own login** under 13, and the 13+ tick is recorded as on the page); lives in more than one household, join requests and contact households use the existing handlers (steps below only expose them): `POST …/people/{id}/multi-household`, `POST …/households/{id}/join-requests`
- [ ] `PUT /api/v1/me/people/{id}/heads-may-edit` (a linked adult, for themselves only): the opt-in `heads_may_edit`; refused (403) when staff locked it, with the reason
- [ ] Claim links: `POST /api/v1/me/people/{id}/claim` for a head of the household, same 14 day, one-time, hashed code; refused under 13
- [ ] Every write writes `changes` (who, what, no values), inside the same transaction as the write, and replays through `Idempotency-Key` (step 2)

### 5. Photos and signed photo URLs
- [ ] `signPhoto(name, viewerID, exp)`: HMAC-SHA256 over `name|viewer|exp` with a key derived from `secrets.key` (no new dependency: `crypto/hmac`, `crypto/sha256`); URL is `/photos/{name}?v=<viewer>&e=<unix>&s=<sig>`
- [ ] `exp` is 5 minutes (a setting constant, not user input). Tied to the **photo file name** (random, new on every replace) and the viewer, so a replaced or hidden photo's old URL stops at once
- [ ] `/photos/{name}` accepts either the cookie session (as now) or a valid signed query; for the signed form it **re-checks `Visible(viewer, owner, photo)`** at fetch time, so a photo that has since been hidden is 404 even with a good signature; a bad or expired signature is 403 with no detail; comparison with `hmac.Equal`
- [ ] Signed responses send `Cache-Control: private, max-age=300` (not the year-long immutable of the cookie form), no CORS needed (an `<img>` loads without it), and `Referrer-Policy: no-referrer`
- [ ] The `/me` responses carry `photo_url` (and `thumb_url`) **only when `Visible()` says the photo shows**; the JSON never holds a bare file name
- [ ] Photo upload for the API: `PUT /api/v1/me/people/{id}/photo` and `PUT /api/v1/me/households/{id}/photo` (multipart, JPEG from the website's `photo` controller) go through the same server-side decode, resize, EXIF-strip and random-name function as step 9 of `PLAN.md`; `DELETE` removes it. `canEditPerson` / `canEditHousehold` decide who may
- [ ] Update CSP note: the website's `img-src` lists `directory.<zone>`; nothing changes in the directory's own CSP

### 6. Notes (allergies and the rest) under the existing rules
- [ ] `GET /api/v1/me/people/{id}/notes`, `POST` and `PATCH/DELETE …/notes/{nid}`: call the notes store functions and the existing viewer x visibility table (`household` / `private` / `directory`) and the writer rules (the person, the heads for people they can edit, staff)
- [ ] The person (and, for children, their household heads) always sees every note about them
- [ ] **Share with sign-ups** tick is part of the note body with the same wording; a note not marked Share is never returned to the event planner and never leaves through any `/me` endpoint except to the people the rules above allow
- [ ] Notes are never in the search or household endpoints, never in logs, and not in the `idempotency` replay for other users (the replay is per user)
- [ ] Allergy list for a household: `GET /api/v1/me/households/{id}/notes?kind=allergy,dietary,medical` for the heads, built from the same query as the page, so the website can show "who has allergies" without a second rule set

### 7. Join requests, family and the rest of `/me`
- [ ] `GET /api/v1/me/requests` (join requests to answer and those I sent, held entries I made and their "waiting for the office" or "turned down" note); `POST …/requests/{id}/accept|decline` for a head of the target household
- [ ] **Start their own household** and `relationships` editing exposed for heads of the household with the existing handlers
- [ ] Anything not listed here stays staff-only on `/admin/…` and has **no** `/me` endpoint (approve, force hide, defaults, groups, link/unlink, delete)

### 8. The `guest` role (later) and roles in general
Not built now; the shape is fixed now so nothing assumes "signed in = member".
- [ ] Every `/me` handler gets the role from the token check and goes through one `meRole(viewer)` switch (`guest`, `member`, `editor`, `admin`); an unknown role gets 403 `no_role`
- [ ] `guest` (non-member logins, e.g. VBS parents): can see only their own household and nothing of anyone else's, **cannot search or view anyone else**: `/me/people`, `/me/households` search and other people's pages answer 403 for `guest`. Whether a `guest` may edit their own household is decided when built (they'd need to be a head of it). They never appear in the member directory, print or groups
- [ ] Open question for when this is built: how a `guest`'s profile is marked (a third `kind`, or a flag) so the directory, print and groups never list them. Settle it here first; until then `guest` gets 403 on all of `/me`
- [ ] `canHeadEditHousehold` already refuses anyone who is not a head of it, so a `guest` who isn't a head can't edit, and no code compares a role to `member` alone

### 9. Tests
All as table tests where it fits, with the API driven through `httptest` and a fake User Management that answers `/api/v1/token`.
- [ ] **`Visible()` on every `/me` response:** a test walks the router for every registered `/api/v1/me/…` route, calls it as a plain member, a child with a login and staff, and checks each field in the JSON against `Visible()` (hidden field omitted; hidden photo has no `photo_url`); a build-time check fails if a handler returns a raw store type
- [ ] **Only heads edit the household:** a non-head adult, a child with a login (`role child`) and a `guest` get 403 on every write route (`PATCH` household, addresses, members, notes of others, photo, claim); a head gets 200 on the same; staff still can
- [ ] **A linked adult's profile isn't editable by others without the opt-in:** a head gets 403 on `PATCH …/people/{id}` for a linked adult, 200 after the person ticks `heads_may_edit`, 403 again after untick or when staff lock it off; an unlinked adult and a linked child are editable by a head of the household
- [ ] **Notes not marked Share never leave:** a note without the tick is absent from the sign-ups API and every `/me` endpoint the note rules don't allow; with the tick it's returned only on the sign-ups call; notes never appear in search, household or print output
- [ ] **A signed photo URL expires and is tied to the photo:** good URL 200; after `exp` 403; changed file name or viewer 403; a replaced photo's old URL 404/403; a photo hidden since signing 404; a tampered `s` 403
- [ ] **A token for another audience is refused:** the fake User Management answers 401 for a token meant for the calendar; the directory returns 401 and caches nothing; a token whose `via` isn't `website` is refused; a good answer is cached for no more than 60 s (fake clock) and a revoked token is refused after the cache ends
- [ ] **CORS refuses other origins:** the website origin gets its headers, any other origin gets none and the preflight is 403, no `*`, no credentials header; with no website origin set, no CORS headers at all
- [ ] Off when User Management isn't connected: every `/api/v1/me/…` is 404 and the cookie pages are unchanged
- [ ] No cookies: a request with a valid session cookie and no Bearer is 401; no `Set-Cookie` on any `/me` response
- [ ] `Idempotency-Key`: missing is 400; the same key replays the same response without a second write; the same key with another body is 422; one user can't replay another's key
- [ ] Find-or-create by `sso_subject`: the first call creates the local user and links an existing profile; the second does not make a duplicate
- [ ] Under-13 and name-match rules over the API: a login can't be turned on for an under-13; a matching add is held and the response never shows the match
- [ ] The service worker test: `/api/` and anything with an `Authorization` header is never cached

### 10. Docs
- [ ] `PLAN.md`: a "Members API" section (pointer to this file), pages/roles tables
- [ ] README: the running-alone table row "Website members area: needs User Management and the website origin"
- [ ] `.claude/CLAUDE.md`: the `/me` API rules (Bearer only, no cookies, `Visible()` on every field, signed photo URLs, off without User Management)
