# ADR-0004: Skills use the Agent Skills SKILL.md format, loaded by progressive disclosure

- **Date:** 2026-10-13
- **Status:** Accepted
- **Deciders:** Doni Wicaksono

## Context

A skill is a reusable procedure an agent follows for one kind of task. The open Agent
Skills format (a folder with `SKILL.md`: YAML frontmatter with `name` and `description`,
a Markdown body, optional `references/` and scripts) is already read by several coding
agents, and the sibling repository hi-donwi/Agent-Skills publishes a catalog in it.

Local models have small effective context windows (Ollama loads 4096 tokens by default,
see ADR-0002). Putting every skill body into the prompt does not fit, and a long system
prompt measurably degrades small models.

## Decision

lokal reads and writes skills only in the Agent Skills format. It does not define its own
skill format or extend the frontmatter beyond the spec's `metadata` map.

Skills are disclosed in three steps:

1. Only `name` and `description` of eligible skills enter the prompt, within a fixed
   token budget.
2. A router picks candidate skills for the task (embedding similarity over descriptions,
   plus keyword match); the selected skill's body is loaded.
3. Files under `references/` are loaded only when the model asks for one by path.

Skill sources, in precedence order: the project's `.agents/skills/`, the user's lokal
skill directory, then directories named in config (for example a checkout of
Agent-Skills). A skill name resolves to the first source that has it.

## Consequences

### Positive
- Skills written for other agents work in lokal unchanged, and the reverse.
- Learned skills (ADR-0005) can be proposed to Agent-Skills without conversion.
- Prompt size stays bounded regardless of how many skills are installed.

### Negative
- Routing quality now matters: a skill whose description does not match how users phrase
  the task is never loaded. The forge's evals measure trigger accuracy for this reason.
- The format has no machine-readable tool or permission declarations; an agent host's
  tool policy cannot be derived from the skill alone.

### Neutral
- Validation follows the spec's naming and length rules; lokal adds no stricter ones.

## Alternatives considered

| Option | Why rejected |
|---|---|
| lokal-specific skill format (YAML/JSON) | Splits the ecosystem; nothing written elsewhere would load |
| Inline all skills into the system prompt | Does not fit small local context windows |
| Router only by keywords | Misses paraphrases; embeddings are already available locally |

## Follow-up

- [ ] M4: parser and validator; `lokal skills ls|show|validate`
- [ ] M4: router with a measured token budget on the tiny profile
