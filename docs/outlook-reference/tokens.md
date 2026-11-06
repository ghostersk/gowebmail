# Outlook Web (dark theme) — measured design tokens

Captured 2026-11-06 from a logged-in outlook.live.com session (in-app Chromium, 1134×805 viewport)
via getComputedStyle on live DOM. Reference screenshots in this folder.

## Colors

| Element | Color | Note |
|---|---|---|
| Top app bar (blue) | `rgb(71,158,245)` `#479ef5` | 48 px tall, full width — **removed in GoWebMail per owner** |
| App rail bg | `rgb(10,10,10)` `#0a0a0a` | 49 px wide, under top bar |
| Menu row bg (File/Home/View/Help) | `rgb(20,20,20)` `#141414` | ~36 px tall in real Outlook |
| Ribbon bg | `rgb(41,41,41)` `#292929` | y≈85..185 (~100 px incl. padding) |
| Favourites panel bg | `rgb(20,20,20)` `#141414` | ~198 px wide |
| Message list row bg | `rgb(41,41,41)` `#292929` | rows ~81 px (comfortable) |
| Reading pane bg | `rgb(20,20,20)` `#141414` | |
| Selected row bg | `rgb(61,61,61)` `#3d3d3d` | + accent `rgb(119,183,247)` `#77b7f7` (left border) |
| Text primary | `#ffffff` | names, subjects (unread) |
| Text secondary | `rgb(214,214,214)` `#d6d6d6` | most labels, read rows |
| Link / accent / unread subject | `rgb(71,158,245)` `#479ef5` | |
| Search input bg | `rgba(33,32,33,0.7)` | 350 px wide, 32 px tall, rounded |

## Type

- Family: `"Segoe UI", "Segoe UI Web (West European)", -apple-system, BlinkMacSystemFont, ...`
- UI base: 14 px; menu-row labels 14 px; ribbon button labels 12 px; body 16 px (body default, UI uses 14)

## Layout (1134 px viewport)

- Top bar: 0..48 px
- App rail: x 0..49, y 48..bottom
- Menu row: y 48..~84
- Ribbon: y 85..~185, x 57..1126
- Favourites: x 50..248
- Message list: x 247..597 (350 px)
- Reading pane: x 597..1134
- Message row height: ~81 px comfortable
- Avatar in list: initials circle, 28 px, blue bg `#479ef5`-ish for account colors
- Ribbon structure: icon (24 px) above 12 px label, groups split by 1 px vertical dividers; large "New" button on the left

## Structure notes

- Message row: hover checkbox · 28 px initials avatar · name (bold when unread) · subject line (blue when unread, gray read) + one-line snippet · right: time + star + "⋯" (Customise Actions)
- Unread count: bold white number at row's left edge (in favourites: right-aligned in the item)
- Favourites item: 20 px FluentSystemIcon + label + optional right-aligned count
- Reading pane: pane title (subject) at top · message card (rounded, bordered) with header row (avatar, `sender <link>`, "To: …", right date, ⋯) · HTML body · footer buttons Reply / Reply all / Forward (outlined)
- Font icons: FluentSystemIcons (we substitute inline SVGs)
