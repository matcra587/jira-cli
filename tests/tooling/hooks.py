#!/usr/bin/env -S uv run --python 3.15 --script
# /// script
# requires-python = ">=3.15,<3.16"
# dependencies = []
# ///
"""Exercise the real hook configuration with isolated Git and tool observers."""

import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
HK = shutil.which("hk")


class HookTests(unittest.TestCase):
    def setUp(self) -> None:
        self.assertIsNotNone(HK, "run through mise run test:hooks")
        self.base = Path(self.enterContext(tempfile.TemporaryDirectory(prefix="jira-hook-")))
        self.root = self.base / "project with spaces"
        self.root.mkdir()
        self.bin = self.base / "bin"
        self.bin.mkdir()
        self.trace = self.base / "trace.jsonl"
        self.selection = self.base / "selected.nul"
        self.env = {
            key: value
            for key, value in os.environ.items()
            if not key.startswith(("HK_", "GIT_", "MISE_", "__MISE"))
        }
        self.env.update(
            HK_CONFIG_DIR=str(self.base / "config"),
            HK_STATE_DIR=str(self.base / "state"),
            HK_CACHE_DIR=str(self.base / "cache"),
            HK_PKL_CACHE_DIR=str(self.base / "pkl-cache"),
            HK_PKL_OFFLINE="1",
            HK_CACHE="0",
            HK_MISE="0",
            GIT_CONFIG_NOSYSTEM="1",
            GIT_CONFIG_GLOBAL=os.devnull,
            GIT_AUTHOR_NAME="Fixture",
            GIT_AUTHOR_EMAIL="fixture@example.invalid",
            GIT_COMMITTER_NAME="Fixture",
            GIT_COMMITTER_EMAIL="fixture@example.invalid",
            HOOK_TEST_TRACE=str(self.trace),
            PATH=str(self.bin) + os.pathsep + self.env["PATH"],
        )
        (self.root / "hk.pkl").write_bytes((REPO_ROOT / "hk.pkl").read_bytes())
        files = {
            "go.mod": "module example.com/hook-test\ngo 1.27.4\n",
            "go.sum": "",
            "main.go": "package main\nfunc main() {}\n",
            ".golangci.yml": 'version: "2"\n',
            ".miserc.toml": 'env = ["development"]\n',
            "mise/conf.d/tasks.development.toml": '[tasks.check]\nrun = "true"\n',
            "mise/tasks/build": "#!/usr/bin/env bash\ntrue\n",
            "mise/tasks/upstream-check.py": "#!/usr/bin/env -S uv run --script\nprint('fixture')\n",
            "script name.sh": "#!/bin/sh\ntrue\n",
        }
        for name, content in files.items():
            path = self.root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content)
        self.git("init", "--quiet")
        self.git("add", "--", "hk.pkl", *files)
        self.git("commit", "--quiet", "-m", "test: initialize owned hook fixture")
        observer = f"""#!{sys.executable}
import json, os, sys
name = os.path.basename(sys.argv[0])
record = {{"tool": name, "args": sys.argv[1:]}}
if name == "betterleaks" and sys.argv[1:2] == ["stdin"]:
    record["stdin"] = sys.stdin.read()
with open(os.environ["HOOK_TEST_TRACE"], "a") as stream:
    stream.write(json.dumps(record) + "\\n")
"""
        for name in ("go", "golangci-lint", "betterleaks", "mise", "shellcheck"):
            path = self.bin / name
            path.write_text(observer)
            path.chmod(0o755)

    def git(self, *args: str) -> None:
        subprocess.run(
            ["git", *args], cwd=self.root, env=self.env,
            check=True, capture_output=True, text=True, timeout=30,
        )

    def hook(self, hook: str, *paths: str, safe: bool = False) -> subprocess.CompletedProcess[str]:
        self.trace.unlink(missing_ok=True)
        self.selection.write_bytes(b"".join(os.fsencode(path) + b"\0" for path in paths))
        args = [HK, "run", hook, "--files0-from", str(self.selection), "--format", "json"]
        if safe:
            args.append("--safe")
        return subprocess.run(
            args, cwd=self.root, env=self.env, text=True,
            capture_output=True, input="", timeout=30,
        )

    def calls(self) -> list[dict]:
        return [json.loads(line) for line in self.trace.read_text().splitlines()] if self.trace.exists() else []

    def assert_passed(self, result: subprocess.CompletedProcess[str]) -> None:
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_dependency_only_changes_execute_all_go_analysis(self) -> None:
        for path in ("go.mod", "go.sum"):
            with self.subTest(path=path):
                self.assert_passed(self.hook("pre-push", path))
                calls = self.calls()
                self.assertIn({"tool": "go", "args": ["vet", "./..."]}, calls)
                self.assertIn({"tool": "go", "args": ["tool", "govulncheck", "./..."]}, calls)
                self.assertIn({"tool": "golangci-lint", "args": ["run", "--fix=false", "./..."]}, calls)
                self.assertFalse(any("gofumpt" in call["args"] for call in calls))

    def test_linter_configuration_executes_linter(self) -> None:
        self.assert_passed(self.hook("check", ".golangci.yml"))
        self.assertTrue(any(call["tool"] == "golangci-lint" for call in self.calls()))

    def test_mise_fragments_and_file_tasks_execute_validation(self) -> None:
        for path in (".miserc.toml", "mise/conf.d/tasks.development.toml", "mise/tasks/build"):
            with self.subTest(path=path):
                self.assert_passed(self.hook("check", path))
                self.assertIn({"tool": "mise", "args": ["run", "mise:check"]}, self.calls())

    def test_shell_selection_preserves_literal_paths_and_excludes_python(self) -> None:
        for path in ("script name.sh", "mise/tasks/build"):
            with self.subTest(path=path):
                self.assert_passed(self.hook("check", path))
                self.assertIn({"tool": "shellcheck", "args": ["-x", path]}, self.calls())
        self.assert_passed(self.hook("check", "mise/tasks/upstream-check.py"))
        self.assertFalse(any(call["tool"] == "shellcheck" for call in self.calls()))

    def test_publishing_brake_prevents_every_linter_invocation(self) -> None:
        (self.root / ".no-push").touch()
        result = self.hook("pre-push", "main.go")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(".no-push marker present", result.stdout + result.stderr)
        self.assertEqual(self.calls(), [])

    def test_safe_check_executes_declared_custom_commands(self) -> None:
        self.assert_passed(self.hook("check", "go.mod", "mise/conf.d/tasks.development.toml", safe=True))
        self.assertIn({"tool": "go", "args": ["tool", "govulncheck", "./..."]}, self.calls())
        self.assertIn({"tool": "mise", "args": ["run", "mise:check"]}, self.calls())

    def test_message_gates_receive_literal_path_and_contents(self) -> None:
        message = self.root / "commit message 'quoted' $literal.txt"
        content = "build: owned message fixture\n"
        message.write_text(content)
        result = subprocess.run(
            [HK, "run", "commit-msg", str(message), "--safe", "--format", "json"],
            cwd=self.root, env=self.env, text=True, capture_output=True, timeout=30,
        )
        self.assert_passed(result)
        self.assertIn({"tool": "mise", "args": ["run", "check-changie", str(message)]}, self.calls())
        self.assertIn({"tool": "betterleaks", "args": ["stdin", "--no-banner"], "stdin": content}, self.calls())

if __name__ == "__main__":
    unittest.main(verbosity=2)
