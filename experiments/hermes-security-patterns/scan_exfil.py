#!/usr/bin/env python3
"""Pre-agent scan for credential exfiltration patterns in AI config files.

Scans CLAUDE.md, AGENTS.md, .cursorrules, and similar AI config files for
commands that would exfiltrate credentials if an agent followed them.
Adapted from Hermes Agent's prompt_builder.py and skills_guard.py patterns.

This fills a gap in Tirith's configfile scanner, which detects prompt injection
but not data exfiltration commands embedded in code blocks.

NOTE: This script may be superseded by a `fullsend scan` CLI command that
integrates exfiltration scanning alongside other pre-agent security checks.

Usage:
    # Scan current directory
    python3 scan_exfil.py .

    # Scan specific files
    python3 scan_exfil.py CLAUDE.md AGENTS.md .cursorrules

    # JSON output for CI integration
    python3 scan_exfil.py --json .

Exit codes: 0 = clean, 1 = findings, 2 = error
"""

import json
import re
import sys
from pathlib import Path

# AI config file patterns (same set as Tirith's configfile.rs)
CONFIG_FILE_NAMES: set[str] = {
    "CLAUDE.md",
    "AGENTS.md",
    ".cursorrules",
    ".windsurfrules",
    ".clinerules",
    ".roorules",
    "SOUL.md",
    "HERMES.md",
    "copilot-instructions.md",
}

CONFIG_FILE_GLOBS: list[str] = [
    ".claude/**/*.md",
    ".cursor/rules/*.md",
    ".github/copilot-instructions.md",
    ".github/agents/*.md",
    ".codex/agents/*.md",
    ".opencode/agents/*.md",
    ".amazonq/rules/*.md",
]

# Exfiltration patterns adapted from Hermes agent/prompt_builder.py + skills_guard.py
EXFIL_PATTERNS: list[tuple[str, str, re.Pattern]] = [
    # curl/wget with env var tokens in URL or data
    (
        "exfil_curl_env",
        "critical",
        re.compile(
            r"(?:curl|wget|httpie|xh)\s+[^\n]*"
            r"\$\{?\w*(?:KEY|TOKEN|SECRET|PASSWORD|CREDENTIAL|AUTH)\w*\}?",
            re.IGNORECASE,
        ),
    ),
    # curl data upload flags with stdin/file
    (
        "exfil_curl_upload",
        "high",
        re.compile(
            r"(?:curl|wget)\s+[^\n]*(?:-d\s+@|-F\s+|--data|--post-file|--upload-file|-T\s+)",
            re.IGNORECASE,
        ),
    ),
    # Reading sensitive credential files
    (
        "read_secrets",
        "critical",
        re.compile(
            r"(?:cat|less|more|head|tail|bat|vim?|nano)\s+[^\n]*"
            r"(?:\.ssh/|\.aws/|\.gnupg/|\.kube/config|\.docker/config"
            r"|\.env|credentials|\.netrc|\.pgpass|id_rsa|id_ed25519)",
            re.IGNORECASE,
        ),
    ),
    # printenv / env dump
    (
        "env_dump",
        "high",
        re.compile(
            r"(?:printenv|env\b|set\b)\s*(?:\||>|$)|"
            r"(?:printenv|env)\s+[^\n]*(?:grep|sort|tee)",
            re.IGNORECASE | re.MULTILINE,
        ),
    ),
    # base64 encode + exfil pipeline
    (
        "base64_exfil",
        "critical",
        re.compile(
            r"base64\s+[^\n]*\$\{?\w*(?:KEY|TOKEN|SECRET|PASSWORD)\w*\}?|"
            r"base64\s+[^\n]*\|\s*(?:curl|wget|nc|ncat)",
            re.IGNORECASE,
        ),
    ),
    # DNS exfiltration via dig/nslookup
    (
        "dns_exfil",
        "critical",
        re.compile(
            r"(?:dig|nslookup|host)\s+[^\n]*\$\{?\w*(?:KEY|TOKEN|SECRET)\w*\}?",
            re.IGNORECASE,
        ),
    ),
    # netcat / socat data send
    (
        "netcat_exfil",
        "high",
        re.compile(
            r"(?:nc|ncat|socat)\s+[^\n]*(?:\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}|[\w.-]+\.\w{2,})\s+\d+",
            re.IGNORECASE,
        ),
    ),
    # Markdown image exfil (renders as request to attacker server)
    (
        "markdown_image_exfil",
        "high",
        re.compile(
            r"!\[.*?\]\(https?://[^\n]*\$\{?\w*(?:KEY|TOKEN|SECRET)\w*\}?",
            re.IGNORECASE,
        ),
    ),
]


def is_config_file(path: Path) -> bool:
    """Check if a path is a known AI config file."""
    if path.name in CONFIG_FILE_NAMES:
        return True
    path_str = str(path)
    for glob in CONFIG_FILE_GLOBS:
        # Simple glob matching — check if path ends with the glob suffix
        parts = glob.split("**/")
        if len(parts) == 2 and path_str.endswith(parts[1]):
            return True
        if path_str.endswith(glob.lstrip(".")):
            return True
    return False


def find_config_files(root: Path) -> list[Path]:
    """Find AI config files in a directory tree."""
    found = []
    if root.is_file():
        if is_config_file(root):
            found.append(root)
        return found

    for path in root.rglob("*"):
        if path.is_file() and is_config_file(path):
            # Skip common non-project dirs
            parts = path.relative_to(root).parts
            if any(p.startswith(".git") and p != ".github" for p in parts):
                continue
            if any(p in ("node_modules", "vendor", ".venv", "venv") for p in parts):
                continue
            found.append(path)

    return sorted(found)


def scan_file(path: Path) -> list[dict]:
    """Scan a file for exfiltration patterns. Returns list of findings."""
    try:
        content = path.read_text(errors="replace")
    except OSError as e:
        return [{"pattern": "error", "severity": "error", "detail": str(e), "line": 0}]

    findings = []
    for name, severity, pattern in EXFIL_PATTERNS:
        for match in pattern.finditer(content):
            # Find line number
            line_num = content[: match.start()].count("\n") + 1
            findings.append(
                {
                    "pattern": name,
                    "severity": severity,
                    "detail": match.group(0).strip()[:120],
                    "line": line_num,
                }
            )

    return findings


def main():
    import argparse

    parser = argparse.ArgumentParser(
        description="Scan AI config files for credential exfiltration patterns"
    )
    parser.add_argument("paths", nargs="+", help="Files or directories to scan")
    parser.add_argument("--json", action="store_true", dest="json_output", help="JSON output")
    args = parser.parse_args()

    all_findings: dict[str, list[dict]] = {}

    for target in args.paths:
        target_path = Path(target)
        if target_path.is_dir():
            files = find_config_files(target_path)
        elif target_path.is_file():
            files = [target_path]
        else:
            print(f"WARNING: {target} not found", file=sys.stderr)
            continue

        for f in files:
            findings = scan_file(f)
            if findings:
                all_findings[str(f)] = findings

    if args.json_output:
        json.dump(
            {
                "scanner": "exfil_scanner",
                "files_scanned": sum(
                    len(find_config_files(Path(t)) if Path(t).is_dir() else [Path(t)])
                    for t in args.paths
                ),
                "findings": all_findings,
            },
            sys.stdout,
            indent=2,
        )
        print()
    else:
        if not all_findings:
            print("No exfiltration patterns found in config files.")
        else:
            for filepath, findings in all_findings.items():
                print(f"\n{filepath}:")
                for f in findings:
                    print(
                        f"  [{f['severity'].upper()}] line {f['line']}: "
                        f"{f['pattern']} — {f['detail']}"
                    )

            total = sum(len(f) for f in all_findings.values())
            critical = sum(
                1
                for findings in all_findings.values()
                for f in findings
                if f["severity"] == "critical"
            )
            print(f"\n{total} finding(s) in {len(all_findings)} file(s) ({critical} critical)")

    if all_findings:
        sys.exit(1)


if __name__ == "__main__":
    main()
