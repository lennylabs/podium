# Bundled typefaces

The design pass fixes Space Grotesk for prose and UI, JetBrains Mono for
identifiers, and Anton for the wordmark, and it records self-hosting as the
remedy for a registry that reaches no font host. Each file here is the latin
subset of the upstream release, in WOFF2, referenced by `../index.css` and
emitted into the committed bundle, so the served UI resolves every family from
the `/ui/` mount and loads nothing from another origin.

| File | Family | Upstream | Licence |
|:--|:--|:--|:--|
| `space-grotesk.woff2` | Space Grotesk, variable 300–700 | https://fonts.google.com/specimen/Space+Grotesk | SIL Open Font License 1.1 |
| `jetbrains-mono.woff2` | JetBrains Mono, variable 100–800 | https://fonts.google.com/specimen/JetBrains+Mono | SIL Open Font License 1.1 |
| `anton.woff2` | Anton, 400 | https://fonts.google.com/specimen/Anton | SIL Open Font License 1.1 |

Replacing a file means re-running `npm run build` in `web/ui`, because the
bundle under `web/bundle` is committed.
