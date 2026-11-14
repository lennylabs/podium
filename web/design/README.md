# Handoff: Podium — website and registry web UI

Two deliverables that share one visual identity: the same tokens, the same type (Space Grotesk, JetBrains Mono, Anton for the wordmark only), the same orange accent, and the same Beat logo.

| Folder | What it is | Start with |
|:--|:--|:--|
| `site/` | Public website: landing page and documentation shell, light and dark, desktop and 390pt mobile. | `site/README.md`, then `site/podium-design-reference.html` |
| `app/` | Registry web UI (the SPA served by `podium serve --web-ui`): 37 screen mockups and a 57-component inventory with every state, in both themes. | `app/README.md`, then `app/Podium UI Inventory.dc.html` |
| `site/logo/` | Production SVGs for the mark, lockups, and tile. Both deliverables use these. | — |

Each README is self-contained and is the spec for its folder. The HTML files are design references, not production code: recreate them in the target codebase, and don't copy the inline styles.

## Shared rules

- **One token set, two themes.** The site and the app use the same hex values for `ink`, `link` (`#a8480a` light), `accent` (`#ffa424`, identical in both themes), `accent-deep` (`#f2861a`, logo disc only), borders, and the dark blue-black grounds. When the two READMEs name a token differently, the hex value decides which token it is.
- **The accent rule.** Orange is a surface carrying dark text, or text on the dark ground. It never carries small text on a light background.
- **Product vs. catalog.** "Registry" is the product; "catalog" is the set of artifacts a registry holds.
- **Fonts.** Load from Google Fonts. Self-host them for an air-gapped registry; the app must work offline.

## Opening the files

Open any `.html` file directly in a browser. No build step and no server. `app/support.js` is the runtime for the two app files and is not part of the design.
