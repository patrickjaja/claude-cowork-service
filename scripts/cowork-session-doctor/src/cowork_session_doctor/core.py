"""Core session analysis and repair logic for Claude Desktop Cowork sessions.

This module handles the three file types that constitute a Cowork session:
- audit.jsonl: Cryptographic ledger of all conversation events (HMAC-signed)
- queue JSONL: UI renderer source file (in .claude/projects/.../<session-id>.jsonl)
- manifest JSON: Session metadata (local_<session-id>.json)
"""

from __future__ import annotations

import json
import os
import re
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any


@dataclass
class SessionPaths:
    """Resolved paths for all components of a Cowork session."""

    session_dir: Path
    manifest_json: Path
    audit_jsonl: Path
    queue_jsonl: Path | None = None
    session_id: str = ""

    @classmethod
    def from_session_dir(cls, session_dir: str | Path) -> "SessionPaths":
        """Discover all session files from a session directory path.

        Accepts either:
        - The session directory itself (local_<id>/)
        - The parent directory containing it
        """
        session_dir = Path(session_dir)

        # If given the parent dir, find the session dir
        if not session_dir.name.startswith("local_"):
            candidates = sorted(session_dir.glob("local_*/"), key=lambda p: p.stat().st_mtime, reverse=True)
            if not candidates:
                msg = f"No local_* session directories found in {session_dir}"
                raise FileNotFoundError(msg)
            session_dir = candidates[0]

        # Extract session ID from directory name
        session_id = session_dir.name.removeprefix("local_")

        # Manifest JSON is a sibling of the session directory
        manifest = session_dir.parent / f"{session_dir.name}.json"

        # Audit JSONL is inside the session directory
        audit = session_dir / "audit.jsonl"

        # Queue JSONL is in .claude/projects/<encoded-path>/<session-id>.jsonl
        queue = None
        claude_projects = session_dir / ".claude" / "projects"
        if claude_projects.exists():
            for project_dir in claude_projects.iterdir():
                if project_dir.is_dir():
                    candidate = project_dir / f"{session_id}.jsonl"
                    if candidate.exists():
                        queue = candidate
                        break

        return cls(
            session_dir=session_dir,
            manifest_json=manifest,
            audit_jsonl=audit,
            queue_jsonl=queue,
            session_id=session_id,
        )


@dataclass
class DiagnosticResult:
    """Results of a session health diagnosis."""

    session_id: str = ""
    audit_lines: int = 0
    queue_lines: int = 0
    errors: list[str] = field(default_factory=list)
    warnings: list[str] = field(default_factory=list)
    info: list[str] = field(default_factory=list)
    is_healthy: bool = True

    def add_error(self, msg: str) -> None:
        self.errors.append(msg)
        self.is_healthy = False

    def add_warning(self, msg: str) -> None:
        self.warnings.append(msg)

    def add_info(self, msg: str) -> None:
        self.info.append(msg)

    def format_report(self) -> str:
        lines = [f"# Session Diagnosis: {self.session_id}", ""]
        lines.append(f"**Status**: {'✅ HEALTHY' if self.is_healthy else '❌ UNHEALTHY'}")
        lines.append(f"**Audit lines**: {self.audit_lines}")
        lines.append(f"**Queue lines**: {self.queue_lines}")
        lines.append("")

        if self.errors:
            lines.append("## Errors")
            for e in self.errors:
                lines.append(f"- ❌ {e}")
            lines.append("")

        if self.warnings:
            lines.append("## Warnings")
            for w in self.warnings:
                lines.append(f"- ⚠️ {w}")
            lines.append("")

        if self.info:
            lines.append("## Info")
            for i in self.info:
                lines.append(f"- ℹ️ {i}")
            lines.append("")

        return "\n".join(lines)


def _parse_jsonl(path: Path) -> list[dict[str, Any]]:
    """Parse a JSONL file, returning a list of dicts. Skips malformed lines."""
    entries: list[dict[str, Any]] = []
    with open(path) as f:
        for i, line in enumerate(f, 1):
            line = line.strip()
            if not line:
                continue
            try:
                entries.append(json.loads(line))
            except json.JSONDecodeError as e:
                entries.append({"_parse_error": str(e), "_line_number": i, "_raw": line[:200]})
    return entries


def diagnose(paths: SessionPaths) -> DiagnosticResult:
    """Run full diagnostic on a Cowork session."""
    result = DiagnosticResult(session_id=paths.session_id)

    # Check file existence
    if not paths.manifest_json.exists():
        result.add_error(f"Manifest JSON not found: {paths.manifest_json}")
    if not paths.audit_jsonl.exists():
        result.add_error(f"Audit JSONL not found: {paths.audit_jsonl}")
        return result
    if paths.queue_jsonl is None or not paths.queue_jsonl.exists():
        result.add_error("Queue JSONL not found in .claude/projects/")
        # Can still diagnose audit
        paths.queue_jsonl = None

    # Parse audit
    audit_entries = _parse_jsonl(paths.audit_jsonl)
    result.audit_lines = len(audit_entries)

    # Check for parse errors in audit
    parse_errors = [e for e in audit_entries if "_parse_error" in e]
    if parse_errors:
        for pe in parse_errors:
            result.add_error(f"Malformed JSON at audit line {pe['_line_number']}: {pe['_parse_error']}")

    # Analyze audit message types
    audit_types = {}
    for entry in audit_entries:
        t = entry.get("type", "unknown")
        audit_types[t] = audit_types.get(t, 0) + 1
    result.add_info(f"Audit message types: {audit_types}")

    # Check for dangling assistant message (no stop_reason on last assistant)
    last_assistant = None
    for entry in reversed(audit_entries):
        if entry.get("type") == "assistant":
            last_assistant = entry
            break
    if last_assistant:
        sr = last_assistant.get("message", {}).get("stop_reason")
        if sr is None:
            result.add_error(f"Last assistant message has no stop_reason (stuck generation)")
        else:
            result.add_info(f"Last assistant stop_reason: {sr}")

    # Check for orphaned tool_use (no matching tool_result)
    pending_tool_ids: set[str] = set()
    for entry in audit_entries:
        content = entry.get("message", {}).get("content", [])
        if isinstance(content, list):
            for block in content:
                if isinstance(block, dict):
                    if block.get("type") == "tool_use":
                        pending_tool_ids.add(block.get("id", ""))
                    elif block.get("type") == "tool_result":
                        pending_tool_ids.discard(block.get("tool_use_id", ""))
    if pending_tool_ids:
        result.add_warning(f"Orphaned tool_use IDs (no tool_result): {pending_tool_ids}")

    # Check for string-typed content in tool_result (should be array)
    for i, entry in enumerate(audit_entries):
        content = entry.get("message", {}).get("content", [])
        if isinstance(content, list):
            for block in content:
                if isinstance(block, dict) and block.get("type") == "tool_result":
                    tc = block.get("content")
                    if isinstance(tc, str):
                        result.add_warning(
                            f"Audit line {i}: tool_result content is string, should be array"
                        )

    # Check for synthetic/dummy message IDs (Gemini artifacts)
    for i, entry in enumerate(audit_entries):
        msg_id = entry.get("message", {}).get("id", "")
        if "dummy" in msg_id:
            result.add_warning(f"Audit line {i}: synthetic message ID detected: {msg_id}")

    # Parse and analyze queue file
    if paths.queue_jsonl and paths.queue_jsonl.exists():
        queue_entries = _parse_jsonl(paths.queue_jsonl)
        result.queue_lines = len(queue_entries)

        # Check for parse errors
        queue_parse_errors = [e for e in queue_entries if "_parse_error" in e]
        if queue_parse_errors:
            for pe in queue_parse_errors:
                result.add_error(
                    f"Malformed JSON at queue line {pe['_line_number']}: {pe['_parse_error']}"
                )

        # Check parentUuid chain
        uuids_seen: set[str] = set()
        for i, entry in enumerate(queue_entries):
            uuid = entry.get("uuid", "")
            parent = entry.get("parentUuid", "")
            if uuid:
                uuids_seen.add(uuid)
            if parent and parent not in uuids_seen and i > 0:
                result.add_error(
                    f"Queue line {i}: parentUuid {parent[:16]}... not found in prior entries"
                )

        # Check queue types
        queue_types = {}
        for entry in queue_entries:
            t = entry.get("type", "unknown")
            queue_types[t] = queue_types.get(t, 0) + 1
        result.add_info(f"Queue message types: {queue_types}")

        # Check for string-typed content in queue
        for i, entry in enumerate(queue_entries):
            content = entry.get("message", {}).get("content", [])
            if isinstance(content, list):
                for block in content:
                    if isinstance(block, dict) and block.get("type") == "tool_result":
                        tc = block.get("content")
                        if isinstance(tc, str):
                            result.add_warning(
                                f"Queue line {i}: tool_result content is string, should be array"
                            )

        # Check session ID consistency
        for i, entry in enumerate(queue_entries):
            sid = entry.get("sessionId", "")
            if sid and sid != paths.session_id:
                result.add_error(
                    f"Queue line {i}: sessionId mismatch: {sid} vs expected {paths.session_id}"
                )

    # Check manifest
    if paths.manifest_json.exists():
        try:
            with open(paths.manifest_json) as f:
                manifest = json.load(f)
            title = manifest.get("title", "")
            if "[FIXED" in title:
                result.add_warning(f"Manifest title contains repair marker: {title}")
            result.add_info(f"Session title: {title}")
            result.add_info(f"Model: {manifest.get('model', 'unknown')}")
        except json.JSONDecodeError as e:
            result.add_error(f"Manifest JSON parse error: {e}")

    return result


def extract_transcript(paths: SessionPaths, output_path: Path | None = None) -> str:
    """Extract a human-readable Markdown transcript from audit.jsonl.

    Returns the transcript as a string and optionally writes to output_path.
    """
    if not paths.audit_jsonl.exists():
        msg = f"Audit JSONL not found: {paths.audit_jsonl}"
        raise FileNotFoundError(msg)

    audit_entries = _parse_jsonl(paths.audit_jsonl)

    lines: list[str] = []
    lines.append(f"# Session Transcript: {paths.session_id}")
    lines.append("")

    # Extract files created/modified
    files_touched: set[str] = set()
    for entry in audit_entries:
        content = entry.get("message", {}).get("content", [])
        if isinstance(content, list):
            for block in content:
                if isinstance(block, dict) and block.get("type") == "tool_use":
                    name = block.get("name", "")
                    inp = block.get("input", {})
                    if name in ("Write", "Edit") and isinstance(inp, dict):
                        fp = inp.get("file_path", "")
                        if fp:
                            files_touched.add(fp)

    if files_touched:
        lines.append("## Files Created/Modified")
        lines.append("")
        for fp in sorted(files_touched):
            lines.append(f"- `{fp}`")
        lines.append("")

    # Build transcript
    lines.append("## Conversation")
    lines.append("")

    for entry in audit_entries:
        if "_parse_error" in entry:
            continue

        entry_type = entry.get("type", "")
        message = entry.get("message", {})
        content = message.get("content", [])

        if entry_type == "user" and isinstance(content, list):
            for block in content:
                if isinstance(block, dict):
                    if block.get("type") == "text":
                        text = block.get("text", "")
                        lines.append("### 👤 User")
                        lines.append("")
                        lines.append(text[:2000])
                        if len(text) > 2000:
                            lines.append(f"\n*[... truncated, {len(text)} chars total]*")
                        lines.append("")
                    elif block.get("type") == "tool_result":
                        tool_id = block.get("tool_use_id", "")
                        lines.append(f"*Tool result for `{tool_id[:20]}...`*")
                        lines.append("")

        elif entry_type == "assistant" and isinstance(content, list):
            for block in content:
                if isinstance(block, dict):
                    if block.get("type") == "text":
                        text = block.get("text", "")
                        lines.append("### 🤖 Assistant")
                        lines.append("")
                        lines.append(text[:2000])
                        if len(text) > 2000:
                            lines.append(f"\n*[... truncated, {len(text)} chars total]*")
                        lines.append("")
                    elif block.get("type") == "tool_use":
                        name = block.get("name", "")
                        inp = block.get("input", {})
                        summary = ""
                        if isinstance(inp, dict):
                            if "file_path" in inp:
                                summary = f" → `{inp['file_path']}`"
                            elif "command" in inp:
                                summary = f" → `{str(inp['command'])[:80]}`"
                            elif "pattern" in inp:
                                summary = f" → `{inp['pattern'][:60]}`"
                        lines.append(f"**Tool: {name}**{summary}")
                        lines.append("")

    transcript = "\n".join(lines)

    if output_path:
        output_path.parent.mkdir(parents=True, exist_ok=True)
        output_path.write_text(transcript)

    return transcript


def extract_artifacts_list(paths: SessionPaths) -> list[dict[str, str]]:
    """Extract a list of all files created/modified during the session."""
    if not paths.audit_jsonl.exists():
        return []

    audit_entries = _parse_jsonl(paths.audit_jsonl)
    artifacts: list[dict[str, str]] = []
    seen: set[str] = set()

    for entry in audit_entries:
        content = entry.get("message", {}).get("content", [])
        if isinstance(content, list):
            for block in content:
                if isinstance(block, dict) and block.get("type") == "tool_use":
                    name = block.get("name", "")
                    inp = block.get("input", {})
                    if name in ("Write", "Edit") and isinstance(inp, dict):
                        fp = inp.get("file_path", "")
                        if fp and fp not in seen:
                            seen.add(fp)
                            exists = os.path.exists(fp)
                            size = os.path.getsize(fp) if exists else 0
                            artifacts.append({
                                "path": fp,
                                "operation": name,
                                "exists": str(exists),
                                "size_bytes": str(size),
                            })

    return artifacts


def backup_session(paths: SessionPaths, backup_dir: Path | None = None) -> Path:
    """Create a timestamped backup of the session files."""
    import shutil
    from datetime import datetime, timezone

    timestamp = datetime.now(tz=timezone.utc).strftime("%Y%m%d-%H%M%S")
    if backup_dir is None:
        backup_dir = paths.session_dir.parent / f"{paths.session_dir.name}.backup-{timestamp}"
    else:
        backup_dir = backup_dir / f"{paths.session_dir.name}.backup-{timestamp}"

    backup_dir.mkdir(parents=True, exist_ok=True)

    # Copy session directory
    shutil.copytree(paths.session_dir, backup_dir / paths.session_dir.name, dirs_exist_ok=True)

    # Copy manifest
    if paths.manifest_json.exists():
        shutil.copy2(paths.manifest_json, backup_dir / paths.manifest_json.name)

    return backup_dir


def repair_queue(paths: SessionPaths, *, dry_run: bool = False) -> list[str]:
    """Repair common issues in the queue JSONL file.

    Returns a list of actions taken (or that would be taken in dry_run mode).
    """
    actions: list[str] = []

    if paths.queue_jsonl is None or not paths.queue_jsonl.exists():
        actions.append("ERROR: No queue file found")
        return actions

    queue_entries = _parse_jsonl(paths.queue_jsonl)

    modified = False

    # Fix 1: Remove synthetic/dummy entries (injected by repair tools)
    clean_entries = []
    for i, entry in enumerate(queue_entries):
        msg_id = entry.get("message", {}).get("id", "")
        if "dummy" in msg_id:
            actions.append(f"REMOVE: Synthetic entry at line {i} (id={msg_id})")
            modified = True
        else:
            clean_entries.append(entry)
    queue_entries = clean_entries

    # Fix 2: Fix string-typed tool_result content → array
    for i, entry in enumerate(queue_entries):
        content = entry.get("message", {}).get("content", [])
        if isinstance(content, list):
            for j, block in enumerate(content):
                if isinstance(block, dict) and block.get("type") == "tool_result":
                    tc = block.get("content")
                    if isinstance(tc, str):
                        entry["message"]["content"][j]["content"] = [{"type": "text", "text": tc}]
                        actions.append(f"FIX: Queue line {i}, tool_result content string→array")
                        modified = True

    # Fix 3: Fix session ID mismatches
    for i, entry in enumerate(queue_entries):
        sid = entry.get("sessionId", "")
        if sid and sid != paths.session_id:
            entry["sessionId"] = paths.session_id
            actions.append(f"FIX: Queue line {i}, sessionId {sid} → {paths.session_id}")
            modified = True

    # Fix 4: Ensure last-prompt entry is consistent
    last_prompt_idx = None
    for i, entry in enumerate(queue_entries):
        if entry.get("type") == "last-prompt":
            last_prompt_idx = i
            lp_sid = entry.get("sessionId", "")
            if lp_sid != paths.session_id:
                entry["sessionId"] = paths.session_id
                actions.append(f"FIX: last-prompt sessionId {lp_sid} → {paths.session_id}")
                modified = True

    if modified and not dry_run:
        with open(paths.queue_jsonl, "w") as f:
            for entry in queue_entries:
                f.write(json.dumps(entry, separators=(",", ":")) + "\n")
        actions.append(f"WRITTEN: {len(queue_entries)} entries to {paths.queue_jsonl}")

    if not modified:
        actions.append("No repairs needed")

    return actions


def validate_jsonl(path: Path) -> list[str]:
    """Validate a JSONL file for structural integrity.

    Returns a list of issues found (empty list = valid).
    """
    issues: list[str] = []
    uuids: set[str] = set()

    with open(path) as f:
        for i, line in enumerate(f, 1):
            line = line.strip()
            if not line:
                continue
            try:
                obj = json.loads(line)
            except json.JSONDecodeError as e:
                issues.append(f"Line {i}: Invalid JSON: {e}")
                continue

            # Check UUID uniqueness
            uuid = obj.get("uuid", "")
            if uuid:
                if uuid in uuids:
                    issues.append(f"Line {i}: Duplicate UUID: {uuid}")
                uuids.add(uuid)

            # Check parentUuid references
            parent = obj.get("parentUuid", "")
            if parent and parent not in uuids and i > 1:
                issues.append(f"Line {i}: Orphaned parentUuid: {parent[:24]}...")

    return issues
