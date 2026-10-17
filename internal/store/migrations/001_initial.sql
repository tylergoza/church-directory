-- Member Directory: first migration. The design is in PLAN.md ("Data
-- model"); this is it made concrete. It stays changeable until the first
-- deploy.
--
-- Conventions, as in the other church apps: dates and times are TEXT
-- (YYYY-MM-DD, or SQLite's UTC "YYYY-MM-DD HH:MM:SS" for created_at and
-- friends); true/false are INTEGER 0/1; "no text" is '' rather than NULL
-- unless NULL means something ("use the default", "never").
--
-- When a person or household is deleted, the rows that only make sense with
-- them go too (ON DELETE CASCADE): notes, visibility settings, memberships,
-- relationships, groups' memberships, claims, join requests and name-match
-- decisions. The audit trail (changes) keeps its rows, with the person or
-- household blanked (SET NULL): it never holds values, only what was done.

-- Logins ------------------------------------------------------------------

-- With single sign-on this is a local cache of User Management's users
-- (keyed by sso_subject), so every users(id) reference keeps working. When
-- the app runs alone these are the local users. A user is a login, not a
-- person: see people.user_id.
--   member  sees the directory (only what each person shows), edits their
--           own profile and, as a head of household, their household
--   editor  staff: also adds, edits, archives and deletes any person or
--           household, sets visibility, links profiles to users, approves
--           pending profiles, manages groups
--   admin   also default visibility, settings, connections, API tokens, users
CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
    display_name  TEXT NOT NULL DEFAULT '',
    password_hash TEXT,                        -- NULL: can't sign in locally
    role          TEXT NOT NULL DEFAULT 'member'
                  CHECK (role IN ('member', 'editor', 'admin')),
    sso_subject   TEXT UNIQUE,                 -- User Management's user ID
    active        INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),  -- 0: access removed, row kept for history
    created_at    TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE sessions (
    token            TEXT PRIMARY KEY,
    user_id          INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf_token       TEXT NOT NULL,
    expires_at       TEXT NOT NULL,
    sso_grant        TEXT,   -- the grant behind an SSO session; NULL for local login
    grant_checked_at TEXT,
    created_at       TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX sessions_user ON sessions(user_id);

-- Site settings (site_name, self_signup, print_for, church_name, logo, ...)
-- and connections to other apps. Secret values are sealed ("enc:v1:...")
-- with the key in secrets.key next to the database.
CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- API tokens for the other church apps (kind 'app', e.g. the event planner
-- reading sign-up details). Only a hash is kept: the token is shown once,
-- and its first few characters (prefix) tell tokens apart afterwards.
CREATE TABLE api_tokens (
    id          INTEGER PRIMARY KEY,
    name        TEXT NOT NULL,             -- "Event planner"
    kind        TEXT NOT NULL CHECK (kind IN ('app')),
    hash        TEXT NOT NULL UNIQUE,      -- sha256
    prefix      TEXT NOT NULL,
    created_by  INTEGER REFERENCES users(id) ON DELETE SET NULL,
    last_used   TEXT,
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

-- People -------------------------------------------------------------------

-- A person (member profile). Not the same thing as a user: most people
-- never log in. user_id is the optional link to a login.
CREATE TABLE people (
    id            INTEGER PRIMARY KEY,
    first_name    TEXT NOT NULL CHECK (first_name <> ''),
    last_name     TEXT NOT NULL CHECK (last_name <> ''),
    goes_by       TEXT NOT NULL DEFAULT '',   -- "Ty"
    suffix        TEXT NOT NULL DEFAULT '',   -- "Jr.", "III": tells a father and son apart

    -- Partial dates: each part can be left empty; a day needs a month. The
    -- day is checked against the month in Go (Feb 29 is fine with no year).
    birth_month   INTEGER CHECK (birth_month BETWEEN 1 AND 12),
    birth_day     INTEGER CHECK (birth_day BETWEEN 1 AND 31),
    birth_year    INTEGER CHECK (birth_year BETWEEN 1 AND 9999),
    anniv_month   INTEGER CHECK (anniv_month BETWEEN 1 AND 12),
    anniv_day     INTEGER CHECK (anniv_day BETWEEN 1 AND 31),
    anniv_year    INTEGER CHECK (anniv_year BETWEEN 1 AND 9999),

    email         TEXT NOT NULL DEFAULT '',
    phone         TEXT NOT NULL DEFAULT '',
    photo         TEXT NOT NULL DEFAULT '',   -- random file name in photos/, or ''

    -- member: listed in the directory. contact: never listed or printed (a
    -- child's other parent who doesn't attend); only staff and the heads of
    -- that child's other households see them.
    kind          TEXT NOT NULL DEFAULT 'member' CHECK (kind IN ('member', 'contact')),

    -- A child who lives in more than one household (parents divorced or
    -- separated, or other). Only then can they be in a second household.
    multi_household INTEGER NOT NULL DEFAULT 0 CHECK (multi_household IN (0, 1)),
    multi_reason  TEXT NOT NULL DEFAULT '' CHECK (multi_reason IN ('', 'divorced', 'other')),

    -- The link to a login. Deleting the user keeps the profile.
    user_id       INTEGER UNIQUE REFERENCES users(id) ON DELETE SET NULL,
    login_allowed INTEGER NOT NULL DEFAULT 0 CHECK (login_allowed IN (0, 1)),  -- "Can have their own login"

    -- A linked adult's household heads may edit the profile: the person's
    -- opt-in, or set by staff. A staff lock (on or off) stops the person
    -- changing it; the reason is written by staff.
    heads_may_edit            INTEGER NOT NULL DEFAULT 0 CHECK (heads_may_edit IN (0, 1)),
    heads_may_edit_locked_by  INTEGER REFERENCES users(id) ON DELETE SET NULL,
    heads_may_edit_locked     INTEGER NOT NULL DEFAULT 0 CHECK (heads_may_edit_locked IN (0, 1)),
    heads_may_edit_reason     TEXT NOT NULL DEFAULT '',

    -- Unknown age (no birth year): who ticked "This person is 13 or older".
    age_attested_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    age_attested_at TEXT,

    -- Waiting for staff. Only the person who made it and staff see it.
    --   self_signup  a profile someone made for themselves
    --   name_match   added by a non-staff user with the same name as someone here
    pending       INTEGER NOT NULL DEFAULT 0 CHECK (pending IN (0, 1)),
    pending_reason TEXT NOT NULL DEFAULT '' CHECK (pending_reason IN ('', 'self_signup', 'name_match')),
    -- A held rename (JSON: first, last, goes_by, suffix). The person stays
    -- listed (pending = 0) under the old name until staff approve.
    pending_name  TEXT NOT NULL DEFAULT '',

    archived_at   TEXT,                       -- moved away, passed away: hidden except in staff's archive list
    created_by    INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at    TEXT NOT NULL DEFAULT (datetime('now')),

    -- Table checks (after the columns, as SQLite wants).
    CHECK (birth_day IS NULL OR birth_month IS NOT NULL),     -- a day needs a month
    CHECK (anniv_day IS NULL OR anniv_month IS NOT NULL),
    CHECK (multi_household = 1 OR multi_reason = ''),         -- a reason needs the tick
    CHECK ((pending = 1) = (pending_reason <> ''))            -- pending always says why
);
CREATE INDEX people_name ON people(last_name COLLATE NOCASE, first_name COLLATE NOCASE);
CREATE INDEX people_held ON people(pending, pending_name) WHERE pending = 1 OR pending_name <> '';
CREATE INDEX people_archived ON people(archived_at) WHERE archived_at IS NOT NULL;

-- Staff said "different people" about two entries with matching names, so
-- the pair is never flagged again. Stored low id, high id.
CREATE TABLE not_same_people (
    person_a   INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    person_b   INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    decided_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    decided_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (person_a, person_b),
    CHECK (person_a < person_b)
);
CREATE INDEX not_same_people_b ON not_same_people(person_b);

-- Households and addresses ---------------------------------------------------

CREATE TABLE households (
    id          INTEGER PRIMARY KEY,
    name        TEXT NOT NULL CHECK (name <> ''),   -- "The Goza Family"
    photo       TEXT NOT NULL DEFAULT '',
    -- contact: never listed or printed (the other parent of a child in two
    -- households); edited by the head who made it and by staff.
    kind        TEXT NOT NULL DEFAULT 'member' CHECK (kind IN ('member', 'contact')),
    -- Who looks after a contact household, or a new one from "Start their
    -- own household", until it has a linked head.
    managed_by  INTEGER REFERENCES people(id) ON DELETE SET NULL,
    -- Made by a user through self sign-up: only they and staff see it until
    -- staff approve it (Needs action).
    pending     INTEGER NOT NULL DEFAULT 0 CHECK (pending IN (0, 1)),
    created_by  INTEGER REFERENCES users(id) ON DELETE SET NULL,
    archived_at TEXT,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX households_name ON households(name COLLATE NOCASE);
CREATE INDEX households_managed_by ON households(managed_by);

-- Addresses belong to the household, several each. Each member is assigned
-- one (household_members.address_id).
CREATE TABLE household_addresses (
    id           INTEGER PRIMARY KEY,
    household_id INTEGER NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    label        TEXT NOT NULL DEFAULT '',   -- "Main", "Mom and Dad's"
    address1     TEXT NOT NULL DEFAULT '',
    address2     TEXT NOT NULL DEFAULT '',
    city         TEXT NOT NULL DEFAULT '',
    state        TEXT NOT NULL DEFAULT '',
    postal       TEXT NOT NULL DEFAULT '',
    shown        INTEGER CHECK (shown IN (0, 1)),  -- the heads' choice; NULL = use the default
    force_hidden INTEGER NOT NULL DEFAULT 0 CHECK (force_hidden IN (0, 1)),  -- staff
    position     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX household_addresses_household ON household_addresses(household_id, position);

CREATE TABLE household_members (
    household_id INTEGER NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    person_id    INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    role         TEXT NOT NULL CHECK (role IN ('head', 'adult', 'child')),
    address_id   INTEGER REFERENCES household_addresses(id) ON DELETE SET NULL,  -- same household; NULL = none
    is_primary   INTEGER NOT NULL DEFAULT 0 CHECK (is_primary IN (0, 1)),  -- a child's primary household
    position     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (household_id, person_id)
);
CREATE INDEX household_members_person ON household_members(person_id);
-- One household per person, except a child who lives in more than one.
CREATE UNIQUE INDEX household_members_one_adult_home ON household_members(person_id) WHERE role IN ('head', 'adult');
CREATE UNIQUE INDEX household_members_one_primary ON household_members(person_id) WHERE is_primary = 1;

-- A member's address must be one of their own household's.
CREATE TRIGGER household_members_address_insert BEFORE INSERT ON household_members
WHEN NEW.address_id IS NOT NULL AND
     (SELECT household_id FROM household_addresses WHERE id = NEW.address_id) IS NOT NEW.household_id
BEGIN
    SELECT RAISE(ABORT, 'address belongs to another household');
END;
CREATE TRIGGER household_members_address_update BEFORE UPDATE OF address_id, household_id ON household_members
WHEN NEW.address_id IS NOT NULL AND
     (SELECT household_id FROM household_addresses WHERE id = NEW.address_id) IS NOT NEW.household_id
BEGIN
    SELECT RAISE(ABORT, 'address belongs to another household');
END;

-- Family links between people, kept when people change households.
-- Only parent and spouse are stored; children, siblings, grandparents and
-- the rest are worked out from them.
CREATE TABLE relationships (
    person_id  INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    related_id INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('parent', 'spouse')),  -- parent: person_id is a parent of related_id
    PRIMARY KEY (person_id, related_id, kind),
    CHECK (person_id <> related_id),
    CHECK (kind = 'parent' OR person_id < related_id)  -- a couple is one row, low id first
);
CREATE INDEX relationships_related ON relationships(related_id, kind);

-- A head asks to add a child to a household someone else heads; a head of
-- that household or staff answers. accepted is NULL until answered.
CREATE TABLE household_requests (
    id           INTEGER PRIMARY KEY,
    person_id    INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    household_id INTEGER NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    asked_by     INTEGER REFERENCES users(id) ON DELETE SET NULL,
    asked_at     TEXT NOT NULL DEFAULT (datetime('now')),
    answered_by  INTEGER REFERENCES users(id) ON DELETE SET NULL,
    answered_at  TEXT,
    accepted     INTEGER CHECK (accepted IN (0, 1)),
    CHECK ((answered_at IS NULL) = (accepted IS NULL))
);
CREATE UNIQUE INDEX household_requests_open ON household_requests(person_id, household_id) WHERE answered_at IS NULL;
CREATE INDEX household_requests_household ON household_requests(household_id);

-- Visibility ---------------------------------------------------------------

-- Per-profile visibility of one field. shown NULL = use the default;
-- force_hidden is staff's override (see Visible() in PLAN.md).
CREATE TABLE field_settings (
    person_id    INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    field        TEXT NOT NULL CHECK (field IN (
        'email', 'phone', 'photo',
        'birthday.month', 'birthday.day', 'birthday.year',
        'anniversary.month', 'anniversary.day', 'anniversary.year')),
    shown        INTEGER CHECK (shown IN (0, 1)),
    force_hidden INTEGER NOT NULL DEFAULT 0 CHECK (force_hidden IN (0, 1)),
    set_by       INTEGER REFERENCES users(id) ON DELETE SET NULL,
    set_at       TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (person_id, field)
);

-- The same for a household's photo. Each address carries its own setting.
CREATE TABLE household_settings (
    household_id INTEGER NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    field        TEXT NOT NULL CHECK (field IN ('household.photo')),
    shown        INTEGER CHECK (shown IN (0, 1)),
    force_hidden INTEGER NOT NULL DEFAULT 0 CHECK (force_hidden IN (0, 1)),
    set_by       INTEGER REFERENCES users(id) ON DELETE SET NULL,
    set_at       TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (household_id, field)
);

-- What shows when nobody chose: one set for adults, one for children (role
-- 'child' in any household). Admins change these on /admin/defaults.
-- Children's anniversary and address rows are "not applicable" in the plan;
-- they are seeded hidden so a lookup always finds a row.
CREATE TABLE defaults (
    audience TEXT NOT NULL CHECK (audience IN ('adult', 'child')),
    field    TEXT NOT NULL CHECK (field IN (
        'email', 'phone', 'photo',
        'birthday.month', 'birthday.day', 'birthday.year',
        'anniversary.month', 'anniversary.day', 'anniversary.year',
        'address', 'household.photo')),
    shown    INTEGER NOT NULL CHECK (shown IN (0, 1)),
    PRIMARY KEY (audience, field)
);
INSERT INTO defaults (audience, field, shown) VALUES
    ('adult', 'email',             1),
    ('adult', 'phone',             1),
    ('adult', 'photo',             1),
    ('adult', 'birthday.month',    1),
    ('adult', 'birthday.day',      1),
    ('adult', 'birthday.year',     0),
    ('adult', 'anniversary.month', 1),
    ('adult', 'anniversary.day',   1),
    ('adult', 'anniversary.year',  1),
    ('adult', 'address',           1),
    ('adult', 'household.photo',   1),
    ('child', 'email',             0),
    ('child', 'phone',             0),
    ('child', 'photo',             0),
    ('child', 'birthday.month',    0),
    ('child', 'birthday.day',      0),
    ('child', 'birthday.year',     0),
    ('child', 'anniversary.month', 0),
    ('child', 'anniversary.day',   0),
    ('child', 'anniversary.year',  0),
    ('child', 'address',           0),
    ('child', 'household.photo',   0);

-- Groups -------------------------------------------------------------------

-- Labels, not permissions: being in "Deacons" gives no rights anywhere.
CREATE TABLE groups (
    id          INTEGER PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE COLLATE NOCASE CHECK (name <> ''),
    description TEXT NOT NULL DEFAULT '',
    visible_to  TEXT NOT NULL DEFAULT 'members' CHECK (visible_to IN ('members', 'staff')),
    position    INTEGER NOT NULL DEFAULT 0,
    archived_at TEXT
);

CREATE TABLE group_members (
    group_id  INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    person_id INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    label     TEXT NOT NULL DEFAULT '',   -- "Chair", "Leader"
    position  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (group_id, person_id)
);
CREATE INDEX group_members_person ON group_members(person_id);

-- Notes --------------------------------------------------------------------

-- Allergies, medical and dietary needs, accessibility, anything else the
-- church may need. Never printed, exported or logged; only the ones with
-- share_with_signups ticked ever leave through the API.
CREATE TABLE notes (
    id         INTEGER PRIMARY KEY,
    person_id  INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL DEFAULT 'general'
               CHECK (kind IN ('allergy', 'medical', 'dietary', 'accessibility', 'general')),
    body       TEXT NOT NULL CHECK (body <> ''),
    -- household: the person, their household heads, staff. private: the
    -- person and staff. directory: every member, on the person's page.
    visible_to TEXT NOT NULL DEFAULT 'household' CHECK (visible_to IN ('household', 'private', 'directory')),
    share_with_signups INTEGER NOT NULL DEFAULT 0 CHECK (share_with_signups IN (0, 1)),
    created_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX notes_person ON notes(person_id);

-- Linking, holding and the audit trail -------------------------------------

-- One-time links a head sends so a person can claim their profile. Only a
-- hash of the code is kept. Valid for 14 days.
CREATE TABLE claims (
    code_hash  TEXT PRIMARY KEY,   -- sha256
    person_id  INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    made_by    INTEGER REFERENCES users(id) ON DELETE SET NULL,
    expires_at TEXT NOT NULL,
    used_at    TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX claims_person ON claims(person_id);

-- A found-later under-13 whose login was an SSO user's: the link is gone, but
-- their User Management access still has to be suspended. Staff do it (step
-- 11 turns this into a PATCH to User Management with the staff member's
-- grant) and then resolve the item. Local users are deactivated at once and
-- need no item. person_id is blanked if the person is deleted.
CREATE TABLE under13_items (
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    person_id   INTEGER REFERENCES people(id) ON DELETE SET NULL,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    resolved_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    resolved_at TEXT
);
CREATE INDEX under13_open ON under13_items(user_id) WHERE resolved_at IS NULL;

-- Who changed what, never the values (names, notes and so on stay out of it).
-- Kept when the person or household is deleted.
CREATE TABLE changes (
    id           INTEGER PRIMARY KEY,
    person_id    INTEGER REFERENCES people(id) ON DELETE SET NULL,
    household_id INTEGER REFERENCES households(id) ON DELETE SET NULL,
    by_user      INTEGER REFERENCES users(id) ON DELETE SET NULL,
    what         TEXT NOT NULL,   -- "edited profile", "changed email visibility", ...
    at           TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX changes_person ON changes(person_id, at);
CREATE INDEX changes_household ON changes(household_id, at);

-- A note for a user on their /me page, such as "turned down": a held entry
-- they made was not accepted. seen_at is set once they've read it.
CREATE TABLE held_notices (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    text       TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    seen_at    TEXT
);
CREATE INDEX held_notices_unseen ON held_notices(user_id) WHERE seen_at IS NULL;

-- Replays for the website members API (MEMBERS_PLAN.md): a write sent again
-- with the same Idempotency-Key gets the saved, already-filtered response.
-- Per user; rows older than 24 hours are deleted.
CREATE TABLE idempotency (
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key        TEXT NOT NULL,
    method     TEXT NOT NULL,
    path       TEXT NOT NULL,
    status     INTEGER NOT NULL,
    body_hash  TEXT NOT NULL,   -- sha256 of the request body
    response   TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (user_id, key)
);
CREATE INDEX idempotency_created ON idempotency(created_at);
