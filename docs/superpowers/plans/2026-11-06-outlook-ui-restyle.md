# Outlook-Style UI Restyle — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restyle GoWebMail's frontend to the Outlook Web (dark) layout and measured tokens, keeping every existing behavior and the Go backend untouched.

**Architecture:** In-place restructure of `web/templates/app.html` (full-width top command row; flex row of app rail + favourites panel + main area; Home ribbon), retheme of `web/static/css/gowebmail.css` to the measured Outlook tokens (spec §Design tokens), and DOM re-targeting in `web/static/js/app.js` / `web/static/js/contacts_calendar.js` without changing the JS function surface. All ribbon/menu actions reuse existing functions and verified endpoints — no new API calls except none.

**Tech Stack:** Go (html/template + go:embed in `webfs.go`) + vanilla JS (no framework, no build step) + plain CSS custom properties.

**Spec:** `docs/superpowers/specs/2026-11-06-outlook-ui-design.md` (tokens + captures: `docs/outlook-reference/`)

## Global Constraints

- **No backend or API changes.** Ribbon/menu actions only use existing JS functions and verified endpoints (spec §Non-goals).
- **No new dependencies, no build step.** All assets stay inside `web/` (go:embed). No JS test framework — verification is build + run + browser check (spec §Verification).
- **Assets are embedded in the binary:** after any `web/` change, rebuild and restart before verifying: `CGO_ENABLED=1 go build -ldflags="-s -w" -o gowebmail ./cmd/server` (README: runs on `:8080`).
- **Keep the JS function surface referenced by templates/inline handlers:** `selectFolder`, `openCompose`, `toggleSidebarCollapse`, `toggleViewDropdown`, `setViewPref`, `showMessageMenu`, `showFolderMenu`, `showAccountMenu`, `toggleComposeDropdown`, `mobShowNav`, `mobBack`, `mobCloseNav`, `openSettings`, `doLogout`, `handleSearch`, `applySearchFilters`, `_setView`, `showMail`, `showContacts`, `showCalendar`, `openReplyTo`, `openForward`, `markFolderAllRead`, `moveMessage`, `deleteMessage`, `snoozeMessage`, `toggleStar`, `showSettingsTab`, `renderFolders`, `loadFolders`, `loadMessages`, `renderMessageList`, `openMessage`, `resetDetail`.
- **Tokens (spec §Design tokens, verbatim):** font `"Segoe UI","Segoe UI Web (West European)",-apple-system,"Helvetica Neue",sans-serif`; UI 14px / labels 12px / pane title 18px; top row + favourites + reading pane bg `#141414`; app rail bg `#0a0a0a`; list + ribbon + cards bg `#292929`; selected row bg `#3d3d3d` + 3px left accent `#77b7f7`; text `#ffffff` / `#d6d6d6`; accent/links/unread subject `#479ef5`; search bg `rgba(33,32,33,.7)`; dividers `rgba(255,255,255,.08)`; hover `rgba(255,255,255,.05)`.
- **No ads / Office chrome / "Buy Microsoft 365".** No dead buttons: a visible button whose action has no existing function/endpoint must not ship.
- Mobile breakpoint stays `max-width:700px` (CSS line 656); desktop ≥701px. Top row, ribbon, and rail must not appear below 700px; the existing mobile flow (`#mob-topbar`) keeps working.
- Work on branch `outlook-ui` from `main`; commit after every task.
- CSS variable name mapping (keep the existing names, change values): `--bg:#141414`, `--surface:#292929`, `--surface2:#292929`, `--surface3:#3d3d3d`, `--border:rgba(255,255,255,.08)`, `--border2:rgba(255,255,255,.14)`, `--text:#ffffff`, `--text2:#d6d6d6`, `--muted:#8d8d8d`, `--accent:#479ef5`, `--accent-dim:rgba(71,158,245,.12)`, `--accent-glow:rgba(71,158,245,.25)`, keep `--danger/--success/--warn/--star`, `--panel-w:350px`; add `--rail-w:48px`, `--topbar-h:48px`, `--ribbon-h:92px`, `--row-accent:#77b7f7`, `--hover:rgba(255,255,255,.05)`.

## Review Focus

1. **Embedded-asset staleness** — edits under `web/` are invisible until rebuild + restart; a "passing" browser check against an old binary proves nothing. → Every task's verify step rebuilds first (Task 1 adds the exact build command; tasks 2–7 repeat it).
2. **Multi-select + ribbon** — with 2+ rows checked, ribbon Delete/Archive/Flag must act on the whole selection (via existing `bulkDelete()`/loops); Mark-all-read stays folder-level and never touches other accounts. → Task 4 verify.
3. **Conversation view off** — toggle off ⇒ no thread count badges on rows, no "Thread (N)" dropdown in the reading pane, flat rows; toggle on restores (default on). → Task 5 verify.
4. **Mark-all-read in virtual views** — Unified Inbox / Starred / Snoozed / label views: button disabled (no single folder). → Task 4 verify.
5. **Context menus on restyled rows** — right-click (and touch long-press via `gowebmail.js` synth) on a message row opens `showMessageMenu` with actions that actually fire; `showFolderMenu`/`showAccountMenu` unchanged. → Task 5 spot-check + Task 8 full pass.
6. **Keyboard shortcuts survive the move** — `Ctrl+N`, `Ctrl+K` (must focus the search input that now lives in the top row — `#search-input` id must survive), `j`/`k`, `r`, `f`, `#`/`Delete`, `Escape` (app.js:163-188). → Task 8 full pass.

---

### Task 1: Tokens, fonts, layout restructure, top command row

**Files:**
- Modify: `web/static/css/gowebmail.css` — `:root` vars (lines 3-10), font family (line 11 + every `'DM Sans'`/`'DM Serif Display'` occurrence → Segoe UI stack), add `.topbar`/`.app-body` styles
- Modify: `web/templates/base.html` — drop the Google Fonts `<link>` (line 7), `?v=79` → `?v=80` (lines 8, 16)
- Modify: `web/templates/app.html` — wrap existing children in `.app-body`; insert `.topbar`
- Modify: `web/static/js/app.js` — top-row menus, `#bell-badge`, `document.title`

**Interfaces:**
- Consumes: existing `toggleSidebarCollapse()`, `openSettings()`, `doLogout()`, `selectFolder()`, `toggleViewDropdown()`/`setViewPref()` (app.js:99/111), `updateUnreadBadgeFromPoll()` (app.js:~3410), `S.me` (app.js:4)
- Produces: `#topbar` containing `#tb-menu-btn` (☰), `#file-btn`, `#home-btn`, `#view-btn` (hosts the moved `#view-dropdown`), `#help-btn`, moved `#search-input` + `#search-filters-btn`, `#bell-btn` + `#bell-badge`, `#gear-btn`, `#profile-btn` + `#profile-initials` + profile menu (contains moved `#user-display` and `#admin-link`); `#app-body` flex row wrapping rail/side/main; JS `topMenuOpen(kind)` toggling `.top-menu` dropdowns (File/Help/profile reuse the existing `.filter-dropdown-menu` pattern)

- [ ] **Step 1: Branch + commit docs**

```bash
git checkout -b outlook-ui
git add docs && git commit -m "docs: Outlook UI design spec and reference captures"
```

- [ ] **Step 2: CSS tokens + font**

Replace `:root` values per Global Constraints mapping; add the new vars; set `html,body` font-family to the Segoe UI stack; replace all `'DM Sans'` and `'DM Serif Display'` font references in the file with the stack (keep weights/sizes). Bump nothing else yet.

- [ ] **Step 3: base.html**

Delete the Google Fonts `<link>`; bump both `?v=79` to `?v=80`.

- [ ] **Step 4: app.html layout restructure + topbar**

Inside `#app-root`: insert `.topbar` as first child; wrap everything currently in `#app-root` (mobile topbar, sidebar, backdrop, `#mail-view`, `#contacts-panel`, `#calendar-panel`) in `<div class="app-body">` (modals stay outside `#app-body`, after it). `.topbar` contents, in order:
- `#tb-menu-btn` (hamburger svg, `onclick="toggleSidebarCollapse()"`)
- `#file-btn` (label "File") — menu items: Settings (`openSettings()`), account email as inert label (`#user-display`, moved here from sidebar footer), Sign out (`doLogout()`)
- `#home-btn` (label "Home", class `on`) — no `onclick` yet (Task 4)
- `#view-btn` (label "View") — move the whole existing `#view-dropdown` block (app.html:101-119, all `vopt-*` ids) under it
- `#help-btn` (label "Help") — menu items: "About GoWebMail" (small modal: name + links `https://github.com/ghostersk/gowebmail`, `https://github.com/ghostersk/gowebmail/blob/main/README.md`)
- spacer; moved `#search-input` + `#search-filters-btn` (keep ids, handlers, aria)
- `#bell-btn` (bell svg + `#bell-badge` hidden span, `onclick="selectFolder('unified','Unified Inbox')"`)
- `#gear-btn` (gear svg, `onclick="openSettings()"`)
- `#profile-btn` (initials in `#profile-initials`, `#admin-link` moved here with its existing inline styles) — menu: account email, "Server Administration" (`#admin-link`), Sign out (`doLogout()`)

- [ ] **Step 5: app.js top-row wiring**

Add `topMenuOpen(kind)` (opens/closes one of File/Help/profile `.top-menu` dropdowns, positioned like the existing `.filter-dropdown-menu`); set `#profile-initials` from `S.me` in `init()` (first letters of `me.username || me.email`, same visibility logic as the current admin-link at app.js:134-135 moved to apply to `#admin-link`'s new home); in `updateUnreadBadgeFromPoll()` also set `#bell-badge` (same count as `#unread-total`, hidden at 0); in `init()` set `document.title = 'Email - ' + (me.username || me.email) + ' - GoWebMail'`.

- [ ] **Step 6: CSS for `.topbar`/`.app-body`**

`#app-root{display:flex;flex-direction:column;height:100%}`; `.app-body{flex:1;display:flex;min-height:0}` (existing sidebar/mail-view children keep their flex behaviour inside it); `.topbar{height:48px;background:#141414;display:flex;align-items:center;gap:2px;padding:0 8px;border-bottom:1px solid var(--border)}`; menu buttons: 14px `#d6d6d6`, padding 8px 10px, radius 4px, hover `var(--hover)`, `.on{color:#fff;background:var(--surface)}`; search: width 350px, height 32px, bg `rgba(33,32,33,.7)`, border `1px solid var(--border2)`, radius 4px; `#bell-badge`: 16px circle, bg `#479ef5`, white 11px, absolute top-right of bell. `@media (max-width:700px){.topbar{display:none}}`.

- [ ] **Step 7: Build, run, verify**

```bash
CGO_ENABLED=1 go build -ldflags="-s -w" -o gowebmail ./cmd/server   # stop the running :8080 instance first
./gowebmail
```
Open `http://localhost:8080` in a browser tab (hard-refresh once). Check: top row renders with File/Home/View/Help + search/bell/gear/profile; Ctrl+K focuses the top-row search; File/Help/profile menus open and their actions work (Settings opens, links correct); View menu = the old view dropdown, all options tick correctly; title is "Email - … - GoWebMail"; app still usable (login, unified inbox, open a message).

- [ ] **Step 8: Commit**

```bash
git add -A && git commit -m "ui: Outlook tokens, Segoe UI, top command row + layout restructure"
```

---

### Task 2: App rail (Mail / Calendar / People)

**Files:**
- Modify: `web/templates/app.html` — insert `<nav class="app-rail">` first inside `.app-body`; remove `#nav-contacts` / `#nav-calendar` items from the sidebar
- Modify: `web/static/css/gowebmail.css` — `.app-rail` styles
- Modify: `web/static/js/contacts_calendar.js` — `_setView()` rail active state

**Interfaces:**
- Consumes: `showMail()`, `showContacts()`, `showCalendar()`, `_setView()` (contacts_calendar.js:9)
- Produces: `#rail-mail`, `#rail-cal`, `#rail-people` buttons; class `.on` marks the active app (default on `#rail-mail`)

- [ ] **Step 1: app.html rail markup**

```html
<nav class="app-rail" id="app-rail">
  <button id="rail-mail" class="on" onclick="showMail()" title="Mail" aria-label="Mail">(envelope svg — reuse the logo-icon path)</button>
  <button id="rail-cal" onclick="showCalendar()" title="Calendar" aria-label="Calendar">(calendar svg — reuse #nav-calendar's path)</button>
  <button id="rail-people" onclick="showContacts()" title="People" aria-label="People">(contacts svg — reuse #nav-contacts' path)</button>
</nav>
```
Delete the `#nav-contacts` and `#nav-calendar` `<div class="nav-item">` blocks from the sidebar (`_setView`/`showContacts` reference them via `?.` — safe).

- [ ] **Step 2: contacts_calendar.js rail active state**

In `_setView(view)`, after the nav-item loop, set `.on` on the matching rail button (`mail`→`#rail-mail`, `contacts`→`#rail-people`, `calendar`→`#rail-cal`), remove it from the others.

- [ ] **Step 3: CSS**

`.app-rail{width:48px;background:#0a0a0a;display:flex;flex-direction:column;align-items:center;padding-top:8px;gap:4px;flex-shrink:0}`; buttons: 40px square, radius 4px, color `#d6d6d6`, svg 20px, hover `var(--hover)`; `.on{color:#479ef5;position:relative}` `.on::before{content:'';position:absolute;left:-4px;top:6px;bottom:6px;width:3px;background:#479ef5;border-radius:2px}`; `@media (max-width:700px){.app-rail{display:none}}`.

- [ ] **Step 4: Build, run, verify**

Rebuild + restart (Global Constraints). Check: rail shows 3 icons with Mail active; clicking Calendar/People switches views (existing panels render) and the active indicator follows; clicking Mail returns to mail; sidebar no longer lists Contacts/Calendar.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "ui: app rail (Mail/Calendar/People) with active indicator"
```

---

### Task 3: Favourites panel + sidebar restructure

**Files:**
- Modify: `web/templates/app.html` — sidebar: drop `.sidebar-header` (logo + compose buttons) and `.sidebar-footer`; add "Favourites" section with `#favourites-list` above the virtual nav items; move `#nav-unified`/`#nav-starred`/`#nav-snoozed`/`#nav-scheduled` into Favourites
- Modify: `web/static/js/app.js` — `renderFavourites()` + call site; drop footer-specific code if any
- Modify: `web/static/css/gowebmail.css` — favourites panel + `.nav-item` restyle

**Interfaces:**
- Consumes: `renderFolders()` (app.js:683), `loadFolders()`, `S.folders`, `S.accounts`, `selectFolder(id, name)`
- Produces: `#favourites-list` container; `renderFavourites()` filling it with first account's Inbox/Drafts/Archive (looked up case-insensitively in `S.folders` by name) each `onclick="selectFolder(<id>,'<name>')"` + the inbox one showing its unread count; called from the same place `renderFolders()` is called (folder load path)

- [ ] **Step 1: app.html sidebar restructure**

Drop `.sidebar-header` (logo + compose — compose moves to the ribbon in Task 4) and `.sidebar-footer` (moved to the top row in Task 1). New sidebar body: `<div class="fav-section-title">Favourites</div><div id="favourites-list"></div>` followed by the four virtual items (`#nav-unified` with `#unread-total`, `#nav-starred`, `#nav-snoozed`, `#nav-scheduled` — keep ids/handlers), then `#folders-by-account`.

- [ ] **Step 2: app.js renderFavourites()**

New function: find the first account's folders in `S.folders` where name (lowercased) is `inbox`/`drafts`/`archive`; render 3 `.nav-item` rows (icon + name; inbox row gets a right-aligned unread count from the folder's existing unread field — same source `renderFolders` uses); `onclick="selectFolder(id,'Name')"`. Call it wherever `renderFolders()` is invoked (same load path, same data).

- [ ] **Step 3: CSS**

`.fav-section-title`: 11px uppercase `var(--muted)` letter-spaced, padding; restyle `.nav-item`: icon 16px + label 14px `#d6d6d6`, hover `var(--hover)`, `.active{background:var(--surface);color:#fff}`, `.unread-badge` right-aligned accent circle. Favourites panel bg `#141414`, width `var(--sidebar-w)`.

- [ ] **Step 4: Build, run, verify**

Rebuild + restart. Check: Favourites shows Inbox/Drafts/Archive + Unified Inbox/Starred/Snoozed/Scheduled; clicking each loads the right view with correct counts; per-account folder sections below still render/collapse/context-menu; sidebar collapse (top-row ☰) and the three sidebar modes (pinned/collapsed/auto) still work via the View menu.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "ui: favourites panel (Inbox/Drafts/Archive + virtual views); drop sidebar header/footer"
```

---

### Task 4: Home ribbon (backend-backed actions only)

**Files:**
- Modify: `web/templates/app.html` — `#ribbon` after `.topbar`; give `#home-btn` `onclick="toggleRibbon()"`
- Modify: `web/static/js/app.js` — `ribbonAct(action)`, `toggleRibbon()`, `updateRibbonState()`, `openReplyAll(id)`; call `updateRibbonState()` from `openMessage`, `renderMessageList`, `selectFolder`, `updateBulkBar`
- Modify: `web/static/js/contacts_calendar.js` — `_setView()` hides `#ribbon` when view ≠ mail
- Modify: `web/static/css/gowebmail.css` — ribbon styles + `@media print` block

**Interfaces:**
- Consumes: `openCompose()`, `deleteMessage(id)`, `bulkDelete()`, `moveMessage(id, folderId)`, `openReplyTo(id)` (app.js:2101), `openForward()` (app.js:2121), `snoozeMessage(id)`, `toggleStar(id, event)`, `markFolderAllRead(folderId)` (app.js:890), `openSettings()` + `showSettingsTab('rules')`, `S`, `SEL` (selection set, see `updateBulkBar` app.js:1439), `toggleComposeDropdown()` (app.js:3571)
- Produces: `ribbonAct(action)` with `action` ∈ `{new, delete, archive, reply, replyall, move, markallread, flag, snooze, print, rules}`; `toggleRibbon()`; `updateRibbonState()`; `openReplyAll(id)`

- [ ] **Step 1: app.html ribbon markup**

`<div class="ribbon" id="ribbon">` with groups separated by `.ribbon-sep` (1px dividers), each button = icon-on-top + 12px label + `onclick="ribbonAct('…')"`, `id="rb-<action>"`: group 1: **New** (large; split caret reusing the compose-options dropdown — move its menu DOM here, keep `toggleComposeDropdown`); group 2: **Delete**, **Archive**; group 3: **Reply**, **Reply all**; group 4: **Move**, **Mark all as read**; group 5: **Flag**, **Snooze**, **Print**; group 6: **Rules**. `@media (max-width:700px)` hidden.

- [ ] **Step 2: app.js ribbonAct(action)**

- `new` → `openCompose()`
- `delete` → `SEL.ids.size ? bulkDelete() : deleteMessage(S.selectedMessageId)`
- `archive` → archive folder id = first `S.folders` entry with name (lowercased) `archive`; loop `moveMessage(id, archiveId)` over `SEL.ids` (or `S.selectedMessageId`)
- `reply` → `openReplyTo(S.selectedMessageId)`; `replyall` → `openReplyAll(S.selectedMessageId)`
- `move` → anchor the same "Move to" target list `showMessageMenu` (app.js:1886) builds, at the ribbon Move button (extract that list-building if it's inline)
- `markallread` → `markFolderAllRead(S.currentFolder)` — only when `S.currentFolder` is a numeric folder id
- `flag` → loop `toggleStar(id)` over `SEL.ids` (or `S.selectedMessageId`)
- `snooze` → `snoozeMessage(S.selectedMessageId)`
- `print` → `window.print()`
- `rules` → `openSettings(); showSettingsTab('rules')`

- [ ] **Step 3: app.js openReplyAll(id)**

Mirror `openReplyTo(id)` (app.js:2101) but CC = original's full recipient set (to + cc, excluding the signed-in account's own email), reading recipients the same way `openReplyTo` reads them.

- [ ] **Step 4: app.js state + visibility**

`toggleRibbon()`: toggle hidden on `#ribbon`, toggle `.on` on `#home-btn` (default: visible + `.on`). `updateRibbonState()`: disable `#rb-*` buttons — message-scoped ones (`delete/archive/reply/replyall/move/flag/snooze/print`) enabled iff `SEL.ids.size || S.selectedMessageId`, with `reply/replyall/print` additionally single-selection only; `markallread` enabled iff `S.currentFolder` numeric; `new`/`rules` always. In `_setView` (contacts_calendar.js): `#ribbon`.style.display = view === 'mail' ? '' : 'none'.

- [ ] **Step 5: CSS + print**

`.ribbon{height:92px;background:#292929;display:flex;align-items:center;padding:0 12px;gap:8px;border-bottom:1px solid var(--border)}`; `.ribbon-btn{display:flex;flex-direction:column;align-items:center;gap:4px;min-width:56px;padding:8px 6px;border:none;background:none;border-radius:4px;color:#d6d6d6;cursor:pointer}` svg 20px, label 12px; hover `var(--hover)`; `:disabled{opacity:.35;cursor:default}`; `.ribbon-sep{width:1px;height:64px;background:var(--border)}`; **New** is larger (icon 28px, label 13px). `@media print{.topbar,.app-rail,.ribbon,.sidebar,#message-list,.mob-topbar{display:none!important}.app-body,#app-root{display:block} #message-detail{position:static;overflow:visible}}`.

- [ ] **Step 6: Build, run, verify (the dead-button gate)**

Rebuild + restart. With a real inbox message selected, exercise **every** ribbon button: New (compose opens), Reply/Reply all (prefilled compose; reply-all CC list correct), Move (target list appears, move works), Snooze (dialog), Flag (star toggles), Print (print preview shows only the message), Rules (Settings opens on Rules tab), Delete (removes + undo toast), Archive (lands in Archive folder), Mark all as read (in a real folder marks it; in Unified Inbox the button is disabled). Check 2-row multi-select: Delete + Flag act on both. Switch to Calendar/Contacts: ribbon hidden; back to Mail: visible again.

- [ ] **Step 7: Commit**

```bash
git add -A && git commit -m "ui: Home ribbon with backend-backed actions; print CSS"
```

---

### Task 5: Message list + row restyle + conversation-view toggle

**Files:**
- Modify: `web/static/js/app.js` — `renderMessageList()` row markup + conversation gate; `openMessage()` thread section gate
- Modify: `web/static/js/gowebmail.js` — `initials(name)` helper
- Modify: `web/templates/app.html` — 2 new options in the (top-row) View dropdown
- Modify: `web/static/css/gowebmail.css` — `.message-item` restyle

**Interfaces:**
- Consumes: `uiPrefsGet`/`setViewPref` (app.js:23/99), `groupThreads()` (app.js:1319), `S.account_color` per-message fields as used at app.js:1341-1405
- Produces: setting key `convView` (bool, default `true`); row markup gains `.msg-avatar` (28px initials circle, bg = `m.account_color`); `initials(name) -> string` in `gowebmail.js` (first letters of the first two words, uppercased; `'?'` if empty)

- [ ] **Step 1: app.html View dropdown**

Add a "Conversation view" group to the moved `#view-dropdown` menu: `✓ On` / `○ Off` calling `setViewPref('convView', true/false)` (same pattern as the existing `vopt-*` rows; `applyViewPrefs()` app.js:35 renders the ticks — extend it for the new option).

- [ ] **Step 2: gowebmail.js initials()**

One-line helper per the Interfaces note.

- [ ] **Step 3: app.js renderMessageList()**

Gate threading: `const conv = uiPrefsGet('convView', true);` — only `groupThreads()` when `conv` (find where the current call happens on the render path and wrap it). Row markup: add `<span class="msg-avatar" style="background:${m.account_color}">${initials(fromName)}</span>` at the row start; drop the small `.msg-unread-dot`/`.msg-dot` pair (avatar carries state+color) but keep `.msg-account-name`, `.msg-label-dots`, `.msg-size`, `.msg-thread-count`, star, and every existing handler/attribute (`onclick=handleMsgClick`, `oncontextmenu=showMessageMenu`, drag, checkbox). Keep the unread/active/selected class logic.

- [ ] **Step 4: app.js openMessage()**

Hide the "Thread (N)" sibling dropdown (app.js:~1744) when `uiPrefsGet('convView', true)` is false.

- [ ] **Step 5: CSS rows**

`.message-item`: padding 8px 10px, flex, gap 8px, border-bottom `1px solid var(--border)`; `.msg-avatar` 28px circle, 12px white text, centered; name row 14px — `.unread .msg-from{color:#fff;font-weight:600}` else `var(--text2)`; `.msg-subject` `.unread` → `#479ef5; font-weight:600` else `var(--text2)`; snippet 13px `var(--muted)`; date 12px right; hover `var(--hover)`; `.active{background:#3d3d3d;box-shadow:inset 3px 0 0 #77b7f7}`; `.selected` checkbox state as today. Density: keep the existing mechanism, keep comfortable rows ≈ the current height.

- [ ] **Step 6: Build, run, verify**

Rebuild + restart. Check: rows match the Outlook look (avatar, bold white unread name, blue unread subject, right time); selected row shows `#3d3d3d` + left accent; hover + checkbox work; **right-click a row → context menu opens and Star/Move/Delete from it fire** (also long-press on a touch-emulated viewport); toggle Conversation view Off → rows flat, thread badges gone, reading-pane Thread dropdown gone; On → restored; density toggle still changes row height.

- [ ] **Step 7: Commit**

```bash
git add -A && git commit -m "ui: Outlook message rows + conversation view toggle"
```

---

### Task 6: Reading pane restyle

**Files:**
- Modify: `web/static/js/app.js` — `openMessage()` card structure + `resetDetail()` empty state
- Modify: `web/static/css/gowebmail.css` — `.message-detail` restyle

**Interfaces:**
- Consumes: Task 5's `initials()`; existing `openReplyTo`/`openReplyAll`/`openForward`
- Produces: `#message-detail` structure: `.pane-title` (subject, 18px) → `.msg-card` (header: `.msg-avatar` + sender link + To line + right-aligned date + existing ⋯ action menu + existing Thread dropdown) → body (sanitized HTML as today) → label chips + attachments bar (existing) → `.pane-footer` (outlined buttons `Reply` / `Reply all` / `Forward` acting on `S.currentMessage`); empty state copy: "Select an item to read" / "Nothing is selected"

- [ ] **Step 1: app.js openMessage()**

Wrap the existing message content in `.msg-card`; insert `.pane-title` with the subject above the card; add `.pane-footer` with the three buttons (reply → `openReplyTo(S.currentMessage.id)`, reply all → `openReplyAll(...)`, forward → `openForward()`). Keep every existing element (⋯ menu, labels, attachments, signature handling, external-link guard).

- [ ] **Step 2: app.js resetDetail()**

Empty state → Outlook copy ("Select an item to read" + muted "Nothing is selected") in the existing empty-state markup pattern.

- [ ] **Step 3: CSS**

`.message-detail{background:#141414}`; `.pane-title{font-size:18px;color:#fff;padding:14px 20px 6px}`; `.msg-card{background:var(--surface);border:1px solid var(--border);border-radius:8px;margin:0 16px 16px;padding:16px}`; card header: avatar + sender 14px `#fff` + To line 12px `var(--text2)` + date right 12px; body 14px `line-height:1.5`; `.pane-footer` buttons = restyled `.btn-secondary` (outlined, 13px).

- [ ] **Step 4: Build, run, verify**

Rebuild + restart. Check: open a message → title, card, header fields, body, labels/attachments, ⋯ menu actions fire, footer Reply/Reply all/Forward open correctly prefilled compose; Thread dropdown still present (conv on); press Escape → empty state shows the new copy; panel resize handle still drags.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "ui: Outlook reading pane (title, card, reply footer)"
```

---

### Task 7: Contacts/Calendar restyle + mobile

**Files:**
- Modify: `web/templates/app.html` — `#calendar-panel` header: `#cal-title` inline `font-family:'DM Serif Display'` → drop (inherit)
- Modify: `web/static/css/gowebmail.css` — contacts/calendar panel styles, `#mob-topbar` restyle

**Interfaces:**
- Consumes: tokens from Task 1 (panels use `var(--bg)`/`var(--surface)` inline — most restyle is automatic)
- Produces: nothing new (styling only)

- [ ] **Step 1: app.html** — remove the inline `font-family:'DM Serif Display',serif` from `#cal-title` (keep size).

- [ ] **Step 2: CSS** — restyle `#contacts-list` rows / contact search header, calendar toolbar + `#cal-grid` cells to the tokens (row hover `var(--hover)`, borders `var(--border)`, primary buttons already token-driven); restyle `#mob-topbar` (bg `#141414`, title centered 14px, `+ New` accent).

- [ ] **Step 3: Build, run, verify**

Rebuild + restart. Desktop: Contacts and Calendar views sit right of the rail, below the top row, look token-consistent (CRUD on a contact still works; a calendar event opens the modal). Mobile (browser devtools, 375 px): top row/ribbon/rail hidden; hamburger opens the sidebar drawer; back button, `+ New`, message open/close, contacts/calendar reachable, nothing overlaps or overflows horizontally.

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "ui: restyle contacts/calendar panels + mobile chrome"
```

---

### Task 8: Full verification, cache bump, finish

**Files:**
- Modify: `web/templates/base.html` — `?v=80` → `?v=81`

- [ ] **Step 1: Backend sanity**

```bash
go build ./... && go vet ./... && go test ./...
```
Expected: all pass (backend untouched).

- [ ] **Step 2: Spec verification pass (manual, per spec §Verification)**

Rebuild + restart. Walk the full checklist: login + MFA; unified inbox; folder switching; open message; reply/forward/scheduled send; star/snooze/move/delete/archive; **right-click context menu on a message — actions fire**; folder + account context menus; search (top row + filters); settings; view prefs (pane position, density, sidebar mode, **conversation toggle**); admin page; mobile viewport 375 px. Keyboard: Ctrl+N, Ctrl+K, j/k, r, f, #, Escape.

- [ ] **Step 3: Cache bump + commit**

Bump `?v=` to 81 in `base.html`; rebuild + restart + hard-refresh; confirm the new CSS is served (network tab) and the app still loads.

```bash
git add -A && git commit -m "ui: Outlook restyle complete (cache bump v81)"
```
