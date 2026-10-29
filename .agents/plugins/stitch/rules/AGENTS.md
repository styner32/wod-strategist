# Stitch Design Integration Rules

These rules scope all Google Stitch MCP operations in `wod-strategist` to a single Stitch project and its existing `DESIGN.md`.

## Pinned Stitch Project
- **Project Title:** `WOD Strategist Mobile Redesign`
- **Project ID (`projectId`):** `11381319408891102008`
- **Resource Name (`name`):** `projects/11381319408891102008`
- **Device Type:** `MOBILE`

## Design System (`DESIGN.md` in Stitch)
`DESIGN.md` is already authored and stored inside Stitch project `11381319408891102008`. Always read design tokens and component guidelines from Stitch (`get_project` with `name: "projects/11381319408891102008"` or `list_design_systems` with `projectId: "11381319408891102008"`) before generating screens or updating app styles (`constants/theme.ts`, `app/`):
- **Active Project Theme (Light):** `assets/a8ede6d33785436a996b1efde5f57d65` (*Tactical Bento Metrics*)
- **Alternate Saved System (Dark):** `assets/572be58d47554fc6bfd1a791f66cae7e` (*Tactical Bento & Metrics*)

## Constraints
- **ONLY** read from or modify Stitch project `11381319408891102008` (`projects/11381319408891102008`).
- **NEVER** read, edit, or delete any other Stitch project in the account (such as `3342529371734966115`).
- **NEVER** call `create_project` or `delete_project` unless explicitly instructed by the user.
- **NEVER** commit `.agents/plugins/stitch/mcp_config.json` or hardcode `X-Goog-Api-Key` into tracked files.
