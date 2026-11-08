---
name: git-id
description: Cross-reference IDs managed by git id, in repositories with a .git-id.toml. Use when writing text that defines or references an ID, minting a new ID, retiring one, or fixing a `git id integrity` finding.
---

# git id

IDs give documents stable names to point at each other with.

## This repository

The repository's `.git-id.toml`, as `git id --skill-instructions` describes it. If the line below shows that command rather than its output, run it.

!`git id --skill-instructions`

Write every ID, placeholders included, in one of those forms. Every new ID comes from git id: `git id new <prefix>` or a placeholder. Where the format has check characters, a body typed by hand fails them: E4 as a definition, W4 as a reference. A reference that passes by chance still has no definition, so it fails as E1.

The examples below write `X` for the prefix and use the default notation (brackets define, bare refers); carry them over to the forms above.

## Getting new IDs

With the agent hook set up (a `git id hook run` command in the hook settings, such as `.claude/settings.json` or `.codex/hooks.json`; `git id hook install claude` or `git id hook install codex` adds it), write placeholders in edit tools, as listed above: as wide as an ID's body, first digit `1`.

- `[X-100000]`: a new definition; each occurrence becomes a different ID. Only in a definition's form.
- `[X-100001]` with `X-100001`, `[X-100002]` with `X-100002`, ...: a slot. Every occurrence of one number becomes the same ID for the rest of the session, so a definition and its references can be written in separate edits.

The hook writes the real IDs into the file and reports each replacement (`[X-100001] -> [X-7fKmQ2]`). From then on, use the real ID: in later edits, and when talking about it.

No report after writing a placeholder means the hook did not run; Codex, for one, runs hooks only once the person has trusted the project and the hook. Ask the person to enable it, then run `git id resolve --all` to resolve what was written. That run assigns slots afresh, so write each slot's every occurrence before running it.

Without the hook, or for writes outside edit tools (shell commands, generated files), run `git id new <prefix> [-n <count>]` first and write what it prints.

Placeholders starting with `0` belong to people, who resolve them with `git id resolve`; the hook rejects them.

## Looking an ID up

`git id check <id>...` (`--json` for structured output) shows where an ID is defined and referenced. Check that a definition exists before writing a reference to it.

`reserved` means it is written nowhere in the working tree but is still taken (HEAD, the index or the history has it), typically a definition deleted by the current change. References to it are stray until the definition is restored or retired as below.

## Retiring an ID

Keep a retired ID's definition, in a definition form, somewhere exactly once: that keeps references to it valid and keeps it from being minted again.

- In place, marked retired, keeping the content: `## ~~[X-7fKmQ2] Topic~~`, followed by why.
- Moved, dropping the content: to a list of retired IDs (`Retired: [X-7fKmQ2], [X-4hNw9R]`), or into the item that absorbed it (`Absorbs [X-7fKmQ2].`).

Check the marking still fits a definition form: in the default notation, a colon or parenthesis right after the closing bracket turns a definition into a link (`[X-7fKmQ2]: merged` is a reference), so write the reason after a space or a dash.

## Checking

Run `git id integrity` before each commit. It exits 1 on errors, the E codes; fix every one. W codes are warnings. Each finding starts with its place, `path:line:`.

| Code | Finding | Fix |
|---|---|---|
| E1 | Stray reference: no definition | Define it, or reference the right ID |
| E2 | Duplicate definition | One item written twice: keep one definition and refer to it. Two items sharing an ID (a copied document, IDs minted on two branches): give one of them a new ID |
| E3 | Unresolved placeholder | Resolve it, or mint with `git id new` and replace it |
| E4 | Invalid definition: fails the checksum | A typo of a minted ID (its references show the original, as E1): restore it. Otherwise replace it with a minted ID |
| W4 | Suspect reference | A real ID mistyped, or an ordinary word shaped like one: fix the former |
| W5 | Unreferenced definition | Often fine |
| E6 | Unmarked ID: fits no form | Rewrite it in a definition or reference form |

## Talking about IDs

With people, say what an ID refers to and add the ID as a pointer: `the export retry policy (X-7fKmQ2)`. An ID alone means nothing until it is looked up.

When writing about the notation itself (docs, examples), use a prefix that is not registered, as this skill does with `X`. A registered prefix would make the examples live IDs: minted, checked, and reported. Tests that need real IDs mint them while running, since an ID written out in a scanned file is checked like any other.
