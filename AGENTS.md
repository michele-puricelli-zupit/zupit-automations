# Project instructions

Use the TypeSafe skill (`typesafe-ai`) when working on this project, as requested by the user.

- Read `.agents/skills/typesafe-ai/SKILL.md` when installed. Otherwise consult the upstream skill: https://raw.githubusercontent.com/typesafe-ai/skills/main/skills/typesafe-ai/SKILL.md.
- Read the current TypeSafe documentation, starting at https://docs.typesafe.ai/llms.txt. Before an integration, read the relevant API, primitive and cookbook pages.
- Keep known rules, exact extraction, validation and execution in Go. Preserve the current deterministic application scope; introduce TypeSafe inference for semantic features requested by the user.
- Install for Codex using one method: `npx skills add typesafe-ai/skills --skill typesafe-ai --agent codex --yes`.
