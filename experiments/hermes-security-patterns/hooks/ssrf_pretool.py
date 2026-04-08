#!/usr/bin/env python3
"""Claude Code PreToolUse hook for SSRF protection.

Intercepts Bash and WebFetch tool calls to validate URLs against RFC 1918
private networks, cloud metadata endpoints, and dangerous schemes before
the agent can make outbound requests.

Install in .claude/settings.json:
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash|WebFetch",
        "hooks": [
          {
            "type": "command",
            "command": "python3 experiments/hermes-security-patterns/hooks/ssrf_pretool.py"
          }
        ]
      }
    ]
  }
}

Protocol: reads JSON from stdin, writes JSON to stdout.
Exit codes: 0 = allow, 1 = block (with reason on stdout).
"""

import ipaddress
import json
import re
import sys
from urllib.parse import urlparse

# --- Blocklists ---

BLOCKED_HOSTNAMES: set[str] = {
    "metadata.google.internal",
    "metadata.goog",
    "169.254.169.254",
    "100.100.100.200",
    "fd00:ec2::254",
}

BLOCKED_NETWORKS: list[ipaddress.IPv4Network | ipaddress.IPv6Network] = [
    ipaddress.IPv4Network("10.0.0.0/8"),
    ipaddress.IPv4Network("172.16.0.0/12"),
    ipaddress.IPv4Network("192.168.0.0/16"),
    ipaddress.IPv4Network("127.0.0.0/8"),
    ipaddress.IPv6Network("::1/128"),
    ipaddress.IPv4Network("169.254.0.0/16"),
    ipaddress.IPv6Network("fe80::/10"),
    ipaddress.IPv4Network("100.64.0.0/10"),
    ipaddress.IPv4Network("0.0.0.0/8"),
    ipaddress.IPv6Network("::/128"),
    ipaddress.IPv6Network("fc00::/7"),
]

BLOCKED_SCHEMES: set[str] = {"file", "ftp", "gopher", "data", "dict", "ldap", "tftp"}
ALLOWED_SCHEMES: set[str] = {"http", "https"}

# Patterns to extract URLs from shell commands
URL_PATTERN = re.compile(
    r"""(?:https?|file|ftp|gopher|data|dict|ldap|tftp)://[^\s"'`|;<>()]+""",
    re.IGNORECASE,
)


def check_ip(ip_str: str) -> str | None:
    """Return a block reason if IP is in a blocked network, else None."""
    try:
        ip = ipaddress.ip_address(ip_str)
    except ValueError:
        return None

    for network in BLOCKED_NETWORKS:
        if ip in network:
            return f"IP {ip} is in blocked network {network}"

    if ip.is_private:
        return f"IP {ip} is a private address"

    return None


def validate_url(url: str) -> str | None:
    """Return a block reason if URL targets internal/blocked resources, else None."""
    try:
        parsed = urlparse(url)
    except Exception:
        return "Malformed URL"

    scheme = (parsed.scheme or "").lower()
    if scheme in BLOCKED_SCHEMES:
        return f"Blocked scheme: {scheme}"
    if scheme not in ALLOWED_SCHEMES:
        return f"Disallowed scheme: {scheme}"

    hostname = (parsed.hostname or "").lower().rstrip(".")
    if not hostname:
        return "No hostname in URL"

    if hostname in BLOCKED_HOSTNAMES:
        return f"Blocked hostname: {hostname}"

    ip_reason = check_ip(hostname)
    if ip_reason:
        return ip_reason

    return None


def extract_urls_from_command(command: str) -> list[str]:
    """Extract URLs from a shell command string."""
    return URL_PATTERN.findall(command)


def process_tool_call(tool_input: dict) -> str | None:
    """Check a tool call for SSRF. Returns block reason or None."""
    tool_name = tool_input.get("tool_name", "")
    tool_params = tool_input.get("tool_input", {})

    urls: list[str] = []

    if tool_name == "Bash":
        command = tool_params.get("command", "")
        urls = extract_urls_from_command(command)
    elif tool_name == "WebFetch":
        url = tool_params.get("url", "")
        if url:
            urls = [url]

    for url in urls:
        reason = validate_url(url)
        if reason:
            return f"SSRF blocked: {url} - {reason}"

    return None


def main():
    try:
        raw = sys.stdin.read()
        if not raw.strip():
            sys.exit(0)
        tool_input = json.loads(raw)
    except (json.JSONDecodeError, Exception):
        # Fail open on parse errors
        sys.exit(0)

    reason = process_tool_call(tool_input)

    if reason:
        json.dump({"decision": "block", "reason": reason}, sys.stdout)
        sys.exit(1)

    sys.exit(0)


if __name__ == "__main__":
    main()
