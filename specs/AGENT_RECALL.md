# Agent Recall Patterns

*How agents should use their memory tools — out of the box behavior*

## The Problem

Agents tend to:
1. Over-rely on current context window (assuming everything important is "here")
2. Default to web search for any knowledge question
3. Under-utilize transcript and memory tools

This leads to:
- Asking users to repeat themselves
- Re-researching topics already discussed
- Missing context from previous sessions
- Confabulating instead of checking

## The Rule

**Internal knowledge first, external knowledge second.**

When a user references past interactions, check `transcript` and `memory_search` BEFORE reaching for web search.

## Trigger Phrases

These phrases (and variants) should trigger internal recall:

| Phrase Pattern | Action |
|----------------|--------|
| "we discussed..." | transcript search |
| "remember when..." | transcript search |
| "a while ago we..." | transcript search |
| "didn't we already..." | transcript search |
| "you mentioned..." | transcript search |
| "what did we decide..." | memory_search → transcript |
| "I told you about..." | transcript search |
| "we looked at this..." | transcript search |
| "last week/month we..." | transcript search with date filter |

## Tool Selection

| Question Type | First Tool | Fallback |
|---------------|------------|----------|
| Past conversation reference | `transcript` | `memory_search` |
| Stored decision/preference | `memory_search` | `transcript` |
| General knowledge | `web_search` | — |
| Current events/news | `web_search` | — |
| "What time did X happen" | `transcript` | — |
| "What's my preference for X" | `memory_search` | `transcript` |

## Implementation Options

### 1. System Prompt Guidance (Current)
Add to default system prompt or AGENTS.md template:
```
When user references past discussions ("we discussed", "remember when", 
"a while ago", etc.), check transcript and memory_search FIRST before 
web search. Context is limited — these tools are your extended memory.
```

### 2. Pre-processing Layer (Future)
Could detect trigger phrases and auto-inject relevant transcript/memory context before the agent sees the message. Pros: automatic, no agent discipline needed. Cons: token cost, might inject irrelevant context.

### 3. Tool Hints in Schema (Future)
Extend tool descriptions with "when to use" hints that models respect:
```json
{
  "name": "transcript",
  "description": "Search conversation history. USE THIS when user says 'we discussed', 'remember when', 'a while ago', etc."
}
```

## Default AGENTS.md Snippet

Include in the out-of-box AGENTS.md template:

```markdown
## Memory Tools

You have two recall tools — use them!

- **transcript** — Raw conversation history. Use when user references past discussions.
- **memory_search** — Curated knowledge files. Use for decisions, preferences, saved context.

**Rule:** When user says "we discussed", "remember when", "didn't we", etc. — 
check these tools FIRST before web search. Your context window is limited; 
these tools are your extended memory.
```

## Success Criteria

An agent with good recall behavior:
1. Never asks "when did we discuss X?" — checks transcript instead
2. Doesn't re-research topics from previous sessions
3. Knows the difference between "I don't have this in context" and "this doesn't exist"
4. Uses web search for new knowledge, internal tools for past knowledge

---

*This should be default agent behavior, not a learned lesson.*
