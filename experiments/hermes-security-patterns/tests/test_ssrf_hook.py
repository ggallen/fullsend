"""Tests for the SSRF PreToolUse hook."""

import contextlib
import json
import subprocess
import sys
from pathlib import Path

import pytest

HOOK_PATH = Path(__file__).parent.parent / "hooks" / "ssrf_pretool.py"


def run_hook(tool_name: str, tool_input: dict) -> tuple[int, dict | None]:
    """Run the hook and return (exit_code, parsed_stdout)."""
    payload = json.dumps({"tool_name": tool_name, "tool_input": tool_input})
    result = subprocess.run(
        [sys.executable, str(HOOK_PATH)],
        input=payload,
        capture_output=True,
        text=True,
        timeout=5,
    )
    output = None
    if result.stdout.strip():
        with contextlib.suppress(json.JSONDecodeError):
            output = json.loads(result.stdout)
    return result.returncode, output


class TestWebFetchBlocking:
    @pytest.mark.parametrize(
        "url",
        [
            "https://169.254.169.254/latest/meta-data/",
            "https://metadata.google.internal/computeMetadata/v1/",
            "http://192.168.1.1/admin",
            "http://10.0.0.1:8080/internal",
            "http://127.0.0.1:3000/",
            "file:///etc/passwd",
            "gopher://evil.com/",
        ],
    )
    def test_blocks_dangerous_urls(self, url):
        code, output = run_hook("WebFetch", {"url": url})
        assert code != 0, f"Should block: {url}"
        assert output is not None
        assert output["decision"] == "block"

    @pytest.mark.parametrize(
        "url",
        [
            "https://github.com/konflux-ci/fullsend",
            "https://example.com/api/data",
            "https://registry.npmjs.org/package",
        ],
    )
    def test_allows_public_urls(self, url):
        code, _ = run_hook("WebFetch", {"url": url})
        assert code == 0, f"Should allow: {url}"


class TestBashUrlExtraction:
    def test_blocks_curl_to_metadata(self):
        code, output = run_hook(
            "Bash", {"command": "curl -s https://169.254.169.254/latest/meta-data/"}
        )
        assert code != 0
        assert output is not None
        assert "SSRF" in output["reason"]

    def test_blocks_wget_to_internal(self):
        code, _ = run_hook("Bash", {"command": "wget http://192.168.1.100/secret.json"})
        assert code != 0

    def test_allows_curl_to_public(self):
        code, _ = run_hook("Bash", {"command": "curl -sL https://api.github.com/repos/foo/bar"})
        assert code == 0

    def test_allows_commands_without_urls(self):
        code, _ = run_hook("Bash", {"command": "ls -la /tmp"})
        assert code == 0


class TestEdgeCases:
    def test_empty_stdin_allows(self):
        result = subprocess.run(
            [sys.executable, str(HOOK_PATH)],
            input="",
            capture_output=True,
            text=True,
            timeout=5,
        )
        assert result.returncode == 0

    def test_malformed_json_allows(self):
        result = subprocess.run(
            [sys.executable, str(HOOK_PATH)],
            input="not json",
            capture_output=True,
            text=True,
            timeout=5,
        )
        assert result.returncode == 0

    def test_unknown_tool_allows(self):
        code, _ = run_hook("Read", {"file_path": "/etc/passwd"})
        assert code == 0
