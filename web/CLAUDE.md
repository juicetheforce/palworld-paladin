# Frontend (web/)

- **Stack.** React 18, TypeScript, and Vite. Keep dependencies minimal; ask
  before adding any npm package.
- **Build output.** `npm run build` writes to `../internal/webserv/dist`,
  which Go embeds.
  - That directory **is committed**, so release builds and deploys need no
    Node. Never add a broad `dist/` ignore rule (the `.gitignore` explains
    this).
  - Vite names bundles with content hashes, so old bundles can linger.
    Before building, check whether `vite.config.ts` sets `emptyOutDir`. If it
    doesn't, clear the old assets in `dist` first.
  - Stage the directory with `git add -A internal/webserv/dist` so deletions
    are included.
  - Any change under `web/src` must ship with a rebuilt `dist` in the same
    commit.
- **Look.** An OLED "control room" style: true black, teal accent. Dark is
  the default and must stay first-class. Keep the style consistent with the
  existing pages rather than introducing a new visual language.
- **Mobile is supported.**
  - Check layouts at desktop width, phone width (~380px), and fold cover
    screens (~340px; there is a 420px breakpoint for these).
  - Ryan tests on a Pixel Fold and a Unihertz Titan 2 (square screen).
  - Known gap: the map has no touch pan/pinch yet.
- **Errors reach the user.** Show every API failure to the user. A swallowed
  UI error was a production bug.
- **Version display.** A hand-built binary shows `dev` in the footer, and a
  `scripts/deploy-test.sh` build shows `dev-<commit>[-dirty]`; both are
  correct. The "· update" indicator is suppressed on all dev builds.
