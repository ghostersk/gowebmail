# GoWebMail — Outlook-style UI restyle (design)

Date: 2026-11-06
Status: proposed (awaiting owner approval)
Project: `/mnt/hdd/nxt/Projects/GO/gowebmail`

## Intent and why

GoWebMail is a self-hosted, multi-account Go webmail client (Outlook/Gmail OAuth2 + IMAP/SMTP). The backend is complete and proven — the owner's Outlook.com account is already connected via this app's OAuth (the "New app(s) connected to your Microsoft account" mail in the account's inbox is GoWebMail's registration). The owner finds the current dark 3-pane UI functional but plain, and wants the **Outlook Web (dark theme) look and feel** — the layout shown in `docs/outlook-reference/user-target.png` — with the same Outlook interactions around it. This is a frontend restyle; the Go backend and API stay untouched.

Reference material (captured from a logged-in Outlook Web session on 2026-11-06):
- `docs/outlook-reference/user-target.png` — owner's target layout
- `docs/outlook-reference/inbox.png` — live capture, folder tree + list
- `docs/outlook-reference/message-open.png` — live capture, reading pane with message
- `docs/outlook-reference/tokens.md` — measured design tokens

## Goals

1. Outlook-Web dark layout: app rail, single top command row, Home ribbon, Favourites + per-account folder panel, message list, reading pane — styled per the measured tokens.
2. **No blue top bar.** Search, notifications, settings and profile move into the `File | Home | View | Help` row (single 48 px top row).
3. Ribbon (Home tab) contains **only actions the backend supports**: New, Delete, Archive, Reply, Reply all, Move, Mark all as read, Flag, Snooze, Print, Rules. **No** Report junk / Report phishing / Ignore / Block / Sweep / Categorise / Discover groups.
4. **No ads, no "Buy Microsoft 365", no Office/Teams/Groups chrome** anywhere.
5. Right-click **context menu on messages works** in the new layout (the app already has `showMessageMenu` + long-press on touch in `web/static/js/app.js` — it must be preserved, re-targeted, and verified). Folder/account context menus too.
6. Everything the current UI does keeps working: unified inbox, Starred/Snoozed/Scheduled, per-account folders, labels, rules, contacts, calendar, compose (incl. reply/forward/scheduled send), search, settings, admin, MFA login, view prefs (pane position, density, sidebar mode), keyboard shortcuts, mobile layout.

## Non-goals

- No backend or API changes. If a ribbon action turns out to have no endpoint, the button is dropped, not stubbed. Known-good endpoints (verified against `cmd/server/main.go`): `DELETE /api/messages/{id}`, `PUT /api/messages/{id}/move`, `PUT /api/messages/{id}/star`, `PUT /api/messages/{id}/snooze`, `PUT /api/messages/{id}/read`, `POST /api/folders/{id}/mark-all-read`, `GET /api/search`, `GET /api/poll`, `POST /api/reply`, `POST /api/forward`, `GET /api/folders`, `GET /api/accounts`.
- No light theme. No pixel-perfect clone (hashed Fluent class names, React runtime, Copilot, ad slots are not replicated).
- `File | View | Help` are lightweight menus (Settings, view prefs, about/links) — not the full Office File/View menus.
- No new dependencies, no build step. Vanilla JS + html/template + go:embed as today.

## Anti-goals (a failure even if it "works")

- Context menu, mobile layout, unified inbox, star/snooze/move, login/MFA, or admin page broken.
- Any visible dead button (a ribbon item whose action doesn't exist in the backend).

## Constraints

- Frontend lives in `web/`: `templates/*.html` (server-side base + app.html ~980 lines), `static/js/app.js` (~3600 lines, the main frontend logic), `static/js/gowebmail.js` (helpers, long-press context-menu synth), `static/js/contacts_calendar.js`, `static/css/gowebmail.css`. All embedded via `webfs.go` (`go:embed`) — new assets must stay inside `web/`.
- Keep the existing CSS custom-property theming system in `gowebmail.css`; Outlook tokens are layered on top as new/updated variables.
- Keep function names in `app.js` stable where practical (templates and inline `onclick`/`oncontextmenu` handlers reference them).

## Approaches (considered)

- **A) In-place restyle by the builder** (chosen by owner). Restyle `app.html`/`gowebmail.css`/`app.js` against the captured references, keeping all existing behavior and the Go API. Full control, verifiable step by step.
- B) Craft Anything `rebuild` against a local mirror of the captures, editing the project in place. Rejected: the rebuild agent has no conversation context, high risk of breaking existing 3-pane/mobile behavior, extra fixing pass needed for the same result.
- C) Copy Outlook's DOM/CSS directly. Rejected: it's a React SPA with hashed class names and a live backend — copying yields a dead shell.

## Design

### Layout (desktop, 1280 px+ reference)

```
┌────────────────────────────────────────────────────────────────────────┐
│ [☰] File  Home  View  Help                        [ Search ]  🔔 ⚙️ (GH) │  48px, #141414
├────────────────────────────────────────────────────────────────────────┤
│  New │ Delete Archive │ Reply Reply all │ Move  Mark all as read │ Flag Snooze Print │ Rules │  ~92px, #292929
├──────┬───────────────┬──────────────────┬─────────────────────────────┤
│ rail │ Favourites    │ Inbox ★  ⤓ ≡ ↕  │  Super message               │
│ 48px │ #141414       │ #292929          │  #141414                     │
│ #0a0a│ ▸ Inbox   1   │ ┌row──────────┐ │  ┌message card───────────┐  │
│ 0a   │ ▸ Drafts      │ │ M  michal    │ │  │ M  michal<…@…>    …  │  │
│  ✉   │ ▸ Archive     │ │    Super msg │ │  │ To: …          date │  │
│  📅  │ gho…@outlook  │ │    Thu 19:27 │ │  │ body…               │  │
│  👤  │   Inbox  1    │ │ Older        │ │  │ [Reply][Reply all] │  │
│      │   Junk [1]    │ └row──────────┘ │  │ [Forward]           │  │
│      │   Drafts      │                  │  └─────────────────────┘  │
│      │   Sent Items  │                  │                             │
│      │   Deleted…    │                  │                             │
└──────┴───────────────┴──────────────────┴─────────────────────────────┘
```

- **Top row** (48 px, `#141414`): left = ☰ (sidebar toggle) + `File` (Settings/account/sign out menu) + `Home` + `View` (existing view dropdown: reading-pane position, density, sidebar mode) + `Help` (about/links). Right = search box (≈350 px, `rgba(33,32,33,0.7)`, wired to `GET /search`), notification bell (badge from existing `/poll` unread), settings gear (opens existing settings), profile (initials avatar → menu: account, Server Administration if admin, sign out). No other items.
- **App rail** (48 px, `#0a0a0a`): three blue Fluent icons stacked — envelope (Mail), calendar, people — matching Outlook (see `docs/outlook-reference/calendar.png`, `docs/outlook-reference/people.png`). Active app: `#479ef5` icon + 3 px left indicator; inactive: `#d6d6d6`. They navigate to the existing mail / calendar / contacts views. Those views keep their current layouts but get the same panel treatment (left tree + content pane, same tokens); no new functionality.
- **Ribbon** (`#292929`, ≈92 px tall): groups separated by 1 px vertical dividers, buttons = icon on top + 12 px label below (Outlook style). Operate on the selected message(s); disabled when nothing selected. Mapping:
  | Button | Action |
  |---|---|
  | New | `openCompose()` (existing) |
  | Delete | `DELETE /api/messages/{id}` (existing delete flow) |
  | Archive | `PUT /api/messages/{id}/move` → Archive folder |
  | Reply / Reply all | existing reply flow (compose prefilled) |
  | Move | existing move-to-folder flow |
  | Mark all as read | `POST /api/folders/{id}/mark-all-read` for current folder |
  | Flag | `PUT /api/messages/{id}/star` |
  | Snooze | existing snooze dialog |
  | Print | `window.print()` (print CSS) |
  | Rules | existing rules UI |
- **Favourites panel** (≈200 px, `#141414`): "Favourites" section (Inbox, Drafts, Archive + existing Unified Inbox / Starred / Snoozed entries as today), then per-account collapsible sections (header = account e-mail) with folders + unread counts. Keep existing data sources (`/api/folders`, `/api/accounts`, `/api/poll`).
- **Message list** (≈350 px, `#292929`): header row (folder name + star + existing sort/view buttons). Rows: 28 px initials avatar, sender (white when unread / `#d6d6d6` read), subject (`#479ef5` bold when unread), one-line snippet, right-aligned time, star, hover checkbox. Selected row: bg `#3d3d3d` + 3 px left accent `#77b7f7`. Keep existing density setting (compact/comfortable). **Conversation threading is kept and toggleable** (see Builder decisions): a thread row shows the existing `N` count badge and the `groupThreads()` collapse behaviour; the row also keeps its existing label dots, attachment indicator and account name.
- **Reading pane** (`#141414`): subject as pane title (≈18 px), message card (rounded, `#292929`-ish panel) with header (avatar, sender link, To, right-aligned date, ⋯ menu = existing per-message actions), the existing **"Thread (N)" dropdown** in the header, body (sanitized HTML as today) with the existing label chips + attachments bar, footer `Reply | Reply all | Forward` (outlined buttons). Empty state: "Select an item to read / Nothing is selected".
- **Mobile**: keep the existing `mob-topbar` flow (hamburger → sidebar, back button, compose), restyled.

### Design tokens (measured from live Outlook, dark theme)

| Token | Value |
|---|---|
| Font | `"Segoe UI", "Segoe UI Web (West European)", -apple-system, "Helvetica Neue", sans-serif` |
| Base size | 14 px (UI), 12 px labels, 18 px pane title |
| Top row / panel bg | `#141414` |
| App rail bg | `#0a0a0a` |
| List / ribbon / card bg | `#292929` |
| Selected row bg | `#3d3d3d`, left accent `#77b7f7` (3 px) |
| Text primary / secondary | `#ffffff` / `#d6d6d6` |
| Accent / links / unread subject | `#479ef5` |
| Search input bg | `rgba(33,32,33,0.7)` |
| Dividers | `rgba(255,255,255,0.08)` |
| Hover bg | `rgba(255,255,255,0.05)` |

### Builder decisions (from the builder check, recorded as calls)

These were raised by the builder check and settled against the doc's own goals (everything keeps working; Outlook-like; no dead buttons). Flag any you disagree with at approval.

- **Conversation threading stays, with an on/off toggle** (owner call): a new "Conversation view" option in the existing View settings dropdown (alongside reading-pane position, density, sidebar mode), persisted via `/api/ui-prefs`. Default **on** (current behaviour, `groupThreads()` collapse, count badge, "Thread (N)" reading-pane dropdown, Outlook-style placement). Off → flat message rows, no thread count badges, no thread dropdown.
- **Mark all as read applies only to the currently selected folder** (owner call): in a per-account folder view it calls `POST /api/folders/{id}/mark-all-read` for that folder only. In the Unified Inbox, Starred, Snoozed or label views there is no single folder, so the button is **disabled** there. Never across accounts.
- **Row / card spec is illustrative, not exhaustive.** Existing row content (label dots, attachment/size indicator, account name) and card content (label chips, attachments bar) carry over; the spec describes layout and style, not a content whitelist.
- **Reply all** is a pre-filled compose (CC param on `openCompose`), no new endpoint. **Print** adds fresh print CSS (none exists today). **Scheduled** appears in Favourites. Favourites Inbox/Drafts/Archive map to the first account's folders (existing behaviour). The ribbon is hidden in Calendar/Contacts views.
- **Font** moves from DM Sans to Segoe UI per the token table.

### What is left to the builder

- DOM restructure in `app.html` to the layout above (keep the existing `id`/`class` contract that `app.js` and inline handlers rely on; rename deliberately where cleaner).
- Rewrite/extend `gowebmail.css` with the tokens above; keep the variable system and mobile breakpoints.
- Re-target selectors in `app.js` without changing its function surface; preserve `showMessageMenu` / `showFolderMenu` / `showAccountMenu` and the long-press synth in `gowebmail.js`.
- Verification (must pass before "done"): `go build ./...` and `go vet ./...`; run the server; manually check — login + MFA, unified inbox, folder switching, open message, reply/forward/scheduled send, star/snooze/move/delete/archive, **right-click context menu on a message** (actions actually fire), search, settings, view prefs, admin page, mobile viewport (375 px).
