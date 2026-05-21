# Themes & Localization

## Themes

8 themes are available:

| Theme         | Description                       |
|---------------|-----------------------------------|
| **Dark**      | Dark theme (default)              |
| **Light**     | Light theme                       |
| **Ocean**     | Ocean tones                       |
| **Forest**    | Forest, green shades              |
| **Nord**      | Cool northern tones               |
| **Dracula**   | Popular dark theme                |
| **Solarized** | Balanced warm palette             |
| **Spacedust** | Muted cosmic palette              |

Switch themes via the settings menu in the UI. The selection is saved in the browser's localStorage and applied instantly.

Themes are implemented via CSS variables (`--bg`, `--fg`, `--accent`, etc.), enabling smooth switching.

## Languages

10 languages are supported:

| Language   | Code |
|------------|------|
| Russian    | ru   |
| English    | en   |
| Chinese    | zh   |
| Spanish    | es   |
| French     | fr   |
| German     | de   |
| Portuguese | pt   |
| Japanese   | ja   |
| Korean     | ko   |
| Arabic     | ar   |

Language switching is available in the UI settings. Localization applies to all UI elements.

## Font Size

Font size is configurable in the UI (default 18px). The setting affects all text elements on the board.

## Fonts

Locally bundled fonts are used (no external CDNs):

- **JetBrains Mono** — for code and monospaced elements
- **Source Sans 3** — primary text font

## Responsiveness

The UI is optimized for mobile devices. The board displays correctly on screens of various sizes.

A [WAP/WML](api.md#wap) interface is also supported for compatibility with legacy mobile browsers.
