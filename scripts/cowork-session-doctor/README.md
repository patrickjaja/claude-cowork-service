# cowork-session-doctor

Diagnostic, repair, and recovery tool for Claude Desktop Cowork sessions.

## Usage

```bash
# Analyze session health
cowork-session-doctor diagnose <session-dir>

# Repair queue file issues (auto-backup first)
cowork-session-doctor repair <session-dir>
cowork-session-doctor repair <session-dir> --dry-run

# Extract human-readable transcript
cowork-session-doctor extract <session-dir>
cowork-session-doctor extract <session-dir> -o transcript.md

# List artifacts created during session
cowork-session-doctor extract <session-dir> --artifacts-only

# Create timestamped backup
cowork-session-doctor backup <session-dir>

# Validate JSONL file integrity
cowork-session-doctor validate <path-to-file.jsonl>
```

## What It Checks

- **JSONL validity**: Every line must be valid JSON
- **UUID chain**: `parentUuid` references must point to existing entries
- **Stop reason**: Last assistant message must have `stop_reason` set
- **Schema**: `tool_result` content must be array format, not string
- **Session ID**: All entries must reference the correct session
- **Synthetic entries**: Detects dummy/repair-injected messages
- **Orphaned tools**: `tool_use` without matching `tool_result`

## Session File Locations

| File | Path |
|------|------|
| Session dir | `~/.config/Claude/local-agent-mode-sessions/<workspace>/<space>/local_<id>/` |
| Manifest | `local_<id>.json` (sibling of session dir) |
| Audit log | `local_<id>/audit.jsonl` |
| Queue file | `local_<id>/.claude/projects/<encoded-path>/<id>.jsonl` |
