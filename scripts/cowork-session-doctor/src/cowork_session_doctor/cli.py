"""CLI entry point for cowork-session-doctor."""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

from cowork_session_doctor.core import (
    SessionPaths,
    backup_session,
    diagnose,
    extract_artifacts_list,
    extract_transcript,
    repair_queue,
    validate_jsonl,
)


def cmd_diagnose(args: argparse.Namespace) -> int:
    """Run full diagnostic on a session."""
    try:
        paths = SessionPaths.from_session_dir(args.session_dir)
    except FileNotFoundError as e:
        print(f"Error: {e}", file=sys.stderr)
        return 1

    result = diagnose(paths)
    print(result.format_report())
    return 0 if result.is_healthy else 1


def cmd_repair(args: argparse.Namespace) -> int:
    """Repair queue file issues."""
    try:
        paths = SessionPaths.from_session_dir(args.session_dir)
    except FileNotFoundError as e:
        print(f"Error: {e}", file=sys.stderr)
        return 1

    if not args.no_backup:
        backup_dir = backup_session(paths)
        print(f"Backup created: {backup_dir}")

    actions = repair_queue(paths, dry_run=args.dry_run)
    for action in actions:
        prefix = "[DRY RUN] " if args.dry_run else ""
        print(f"{prefix}{action}")

    return 0


def cmd_extract(args: argparse.Namespace) -> int:
    """Extract transcript and artifact list."""
    try:
        paths = SessionPaths.from_session_dir(args.session_dir)
    except FileNotFoundError as e:
        print(f"Error: {e}", file=sys.stderr)
        return 1

    output = Path(args.output) if args.output else None

    if args.artifacts_only:
        artifacts = extract_artifacts_list(paths)
        if not artifacts:
            print("No artifacts found in session.")
            return 0
        print(f"{'Path':<80} {'Op':<6} {'Exists':<8} {'Size'}")
        print("-" * 110)
        for a in artifacts:
            print(f"{a['path']:<80} {a['operation']:<6} {a['exists']:<8} {a['size_bytes']}")
        return 0

    transcript = extract_transcript(paths, output_path=output)

    if output:
        print(f"Transcript written to: {output}")
    else:
        print(transcript)

    return 0


def cmd_backup(args: argparse.Namespace) -> int:
    """Create a timestamped backup."""
    try:
        paths = SessionPaths.from_session_dir(args.session_dir)
    except FileNotFoundError as e:
        print(f"Error: {e}", file=sys.stderr)
        return 1

    backup_dir = Path(args.dest) if args.dest else None
    result = backup_session(paths, backup_dir=backup_dir)
    print(f"Backup created: {result}")
    return 0


def cmd_validate(args: argparse.Namespace) -> int:
    """Validate a JSONL file."""
    path = Path(args.file)
    if not path.exists():
        print(f"Error: File not found: {path}", file=sys.stderr)
        return 1

    issues = validate_jsonl(path)
    if not issues:
        print(f"✅ {path.name}: Valid ({sum(1 for _ in open(path))} lines)")
        return 0

    print(f"❌ {path.name}: {len(issues)} issue(s)")
    for issue in issues:
        print(f"  - {issue}")
    return 1


def main() -> None:
    """Main CLI entry point."""
    parser = argparse.ArgumentParser(
        prog="cowork-session-doctor",
        description="Diagnostic, repair, and recovery tool for Claude Desktop Cowork sessions",
    )
    subparsers = parser.add_subparsers(dest="command", required=True)

    # diagnose
    p_diag = subparsers.add_parser("diagnose", help="Analyze session health")
    p_diag.add_argument("session_dir", help="Path to session directory (local_<id>/ or parent)")
    p_diag.set_defaults(func=cmd_diagnose)

    # repair
    p_repair = subparsers.add_parser("repair", help="Repair queue file issues")
    p_repair.add_argument("session_dir", help="Path to session directory")
    p_repair.add_argument("--dry-run", action="store_true", help="Show what would be done")
    p_repair.add_argument("--no-backup", action="store_true", help="Skip pre-repair backup")
    p_repair.set_defaults(func=cmd_repair)

    # extract
    p_extract = subparsers.add_parser("extract", help="Extract transcript and artifacts")
    p_extract.add_argument("session_dir", help="Path to session directory")
    p_extract.add_argument("--output", "-o", help="Output file path for transcript")
    p_extract.add_argument("--artifacts-only", action="store_true", help="Only list artifacts")
    p_extract.set_defaults(func=cmd_extract)

    # backup
    p_backup = subparsers.add_parser("backup", help="Create timestamped backup")
    p_backup.add_argument("session_dir", help="Path to session directory")
    p_backup.add_argument("--dest", help="Backup destination directory")
    p_backup.set_defaults(func=cmd_backup)

    # validate
    p_validate = subparsers.add_parser("validate", help="Validate JSONL file integrity")
    p_validate.add_argument("file", help="Path to JSONL file")
    p_validate.set_defaults(func=cmd_validate)

    args = parser.parse_args()
    sys.exit(args.func(args))


if __name__ == "__main__":
    main()
