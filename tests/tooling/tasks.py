#!/usr/bin/env -S uv run --python 3.14 --script
# /// script
# requires-python = ">=3.14,<3.15"
# dependencies = []
# ///
"""Execute native mise task contracts in owned, tool-free fixtures.

Application tools are independent argv observers. These tests prove task
orchestration, not successful Go builds, Jira calls or native Windows behavior.
"""

import json
import os
import shlex
import shutil
import subprocess
import sys
import tempfile
import tomllib
import unittest
from pathlib import Path
from typing import Any

REPO_ROOT = Path(__file__).resolve().parents[2]
MISE = shutil.which("mise")
TASKS = tomllib.loads((REPO_ROOT / "tasks.toml").read_text())


def toml_value(value: Any) -> str:
    """Encode the scalar/list/inline-table shapes used by task fixtures."""
    if isinstance(value, dict):
        return (
            "{ "
            + ", ".join(
                f"{json.dumps(key)} = {toml_value(item)}" for key, item in value.items()
            )
            + " }"
        )
    if isinstance(value, list):
        return "[" + ", ".join(toml_value(item) for item in value) + "]"
    return json.dumps(value)


class NativeTaskTests(unittest.TestCase):
    def setUp(self) -> None:
        self.assertEqual(sys.version_info[:2], (3, 14))
        self.assertIsNotNone(MISE, "run through mise run test:tasks")
        directory = tempfile.TemporaryDirectory(prefix="jira-task-test-")
        self.addCleanup(directory.cleanup)
        self.base = Path(directory.name)
        self.root = self.base / "project"
        self.root.mkdir()
        self.bin = self.base / "bin"
        self.bin.mkdir()
        self.trace = self.base / "trace.jsonl"
        self.env = {
            key: value
            for key, value in os.environ.items()
            if not key.startswith(("MISE_", "usage_"))
        }
        for key, suffix in {
            "MISE_CONFIG_DIR": "config",
            "MISE_GLOBAL_CONFIG_FILE": "global.toml",
            "MISE_SYSTEM_CONFIG_DIR": "system",
            "MISE_DATA_DIR": "data",
            "MISE_CACHE_DIR": "cache",
            "MISE_STATE_DIR": "state",
            "MISE_TMP_DIR": "tmp",
        }.items():
            self.env[key] = str(self.base / suffix)
        self.env.update(
            MISE_CEILING_PATHS=str(self.base),
            MISE_TRUSTED_CONFIG_PATHS=str(self.root),
            MISE_OFFLINE="true",
            MISE_TASK_AUTO_INSTALL="false",
            TASK_TEST_TRACE=str(self.trace),
            PATH=str(self.bin) + os.pathsep + self.env["PATH"],
        )
        shutil.copyfile(REPO_ROOT / "tasks.toml", self.root / "tasks.toml")
        shutil.copytree(REPO_ROOT / ".mise", self.root / ".mise")
        self.config = {
            "env": {
                "BINARY_NAME": "jira",
                "DIST_DIR": "./dist",
                "GOTOOLCHAIN": "local",
            },
            "task_config": {"includes": ["tasks.toml", ".mise/tasks"]},
        }
        self.write_config(self.config)
        self.observer = f"""#!{sys.executable}
import json, os, sys
from pathlib import Path
name = Path(sys.argv[0]).name
args = sys.argv[1:]
with open(os.environ['TASK_TEST_TRACE'], 'a') as stream:
    stream.write(json.dumps([name, args]) + '\\n')
failure = os.environ.get('TASK_TEST_FAILURE', '')
if name == 'go':
    if args[:2] == ['list', '-m']:
        if '-versions' in args:
            if failure == 'versions': sys.exit(37)
            print('v1.2.2\\nv1.2.3\\nv1.2.4-rc.0\\nv1.3.0')
        elif '-u' in args:
            print('v1.3.0')
        else:
            if failure == 'modules': sys.exit(37)
            print('example.com/library v1.2.2')
    elif args[:1] == ['build']:
        output = Path(args[args.index('-o') + 1])
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text('fixture binary')
    elif args[:1] == ['env']:
        print({{'GOOS': 'linux', 'GOARCH': 'amd64'}}[args[1]])
if name == 'changie' and args[:1] == ['batch'] and failure == 'batch':
    sys.exit(37)
"""
        for command in ("go", "changie", "uvx", "goreleaser", "rumdl"):
            self.write_executable(self.bin / command, self.observer)

    def write_config(self, config: dict[str, Any]) -> None:
        content = "\n".join(
            f"{key} = {toml_value(value)}" for key, value in config.items()
        )
        (self.root / ".mise.toml").write_text(content + "\n")

    def write_executable(self, path: Path, content: str) -> None:
        path.write_text(content)
        path.chmod(0o755)

    def run_mise(
        self, *args: str, cwd: Path | None = None
    ) -> subprocess.CompletedProcess[str]:
        assert MISE is not None
        return subprocess.run(
            [MISE, *args],
            cwd=cwd or self.root,
            env=self.env,
            text=True,
            capture_output=True,
            timeout=30,
            check=False,
        )

    def succeeds(self, *args: str, cwd: Path | None = None) -> str:
        result = self.run_mise(*args, cwd=cwd)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        return result.stdout

    def calls(self) -> list[list[Any]]:
        if not self.trace.exists():
            return []
        return [json.loads(line) for line in self.trace.read_text().splitlines()]

    def test_complete_inventory_and_native_owners(self) -> None:
        expected = set(
            """
            audit build check check-changie ci clean default deps deps:update
            deps:update:minor docs:build docs:serve fix fmt fmt:check fmt:file
            generate install lint lint:fix pre-commit release release-notes
            release:build release:check release:preflight release:snapshot
            rumdl rumdl:fix run security test test:all test:attachment:windows
            test:cache-sync test:config:windows test:draft:windows
            test:editor:windows test:hooks test:input:windows test:keyring
            test:keyring:windows test:live:guards test:matrix test:query-swap
            test:race test:release test:tasks tidy upstream-check version vet
            workflow:lint
        """.split()
        )
        inventory = json.loads(self.succeeds("tasks", "--local", "--json"))
        self.assertEqual({task["name"] for task in inventory}, expected)
        for name in ("build", "install", "release", "upstream-check", "version"):
            task = next(task for task in inventory if task["name"] == name)
            self.assertIn("/.mise/tasks/", task["source"])
        loaded = self.succeeds("config", "ls")
        self.assertIn(str(self.root / ".mise.toml"), loaded)
        self.assertNotIn(str(REPO_ROOT), loaded)
        self.succeeds("tasks", "validate")

    def test_release_help_and_parser_refusals_have_no_effects(self) -> None:
        self.write_executable(self.bin / "git", self.observer)
        output = self.succeeds("run", "release", "--help")
        self.assertIn("0.16.0-beta.0", output)
        for arguments in (
            (),
            ("--unknown", "0.16.0"),
            ("0.16.0", "--unknown"),
            ("0.16.0", "HEAD", "extra"),
        ):
            with self.subTest(arguments=arguments):
                result = self.run_mise("run", "release", *arguments)
                self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.calls(), [])
        self.assertFalse((self.root / ".git").exists())

    def test_literal_check_changie_argument(self) -> None:
        value = "message $(touch injected-marker); 'quoted' $literal.md"
        self.succeeds("run", "check-changie", value)
        self.assertEqual(self.calls(), [["go", ["run", "./cmd/check-changie", value]]])
        self.assertFalse((self.root / "injected-marker").exists())

    def test_owned_task_help_and_unexpected_arguments_do_not_run_tools(self) -> None:
        self.write_executable(self.bin / "git", self.observer)
        for task in (
            "build",
            "clean",
            "install",
            "version",
            "deps:update",
            "deps:update:minor",
            "check",
            "ci",
            "fix",
            "release:preflight",
        ):
            with self.subTest(task=task):
                self.trace.unlink(missing_ok=True)
                self.succeeds("run", task, "--help")
                self.assertEqual(self.calls(), [], task + " help entered a tool")
                for argument in ("--unknown", "extra"):
                    result = self.run_mise("run", task, argument)
                    self.assertNotEqual(result.returncode, 0)
                    self.assertEqual(
                        self.calls(), [], task + " " + argument + " entered a tool"
                    )
        self.assertEqual(self.calls(), [])

    def test_literal_release_notes_and_failed_batch_stops_merge(self) -> None:
        value = "0.16.0-beta.0$(touch injected-marker)"
        self.succeeds("run", "release-notes", value)
        self.assertEqual(
            self.calls(), [["changie", ["batch", "--", value]], ["changie", ["merge"]]]
        )
        self.assertFalse((self.root / "injected-marker").exists())
        self.trace.unlink()
        self.env["TASK_TEST_FAILURE"] = "batch"
        result = self.run_mise("run", "release-notes", "0.16.0-beta.0")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.calls(), [["changie", ["batch", "--", "0.16.0-beta.0"]]])

    def test_build_reenters_go_cache_after_non_go_changes(self) -> None:
        embedded = self.root / "internal/agentguides/guides/fixture.md"
        embedded.parent.mkdir(parents=True)
        embedded.write_text("before")
        self.succeeds("run", "build")
        embedded.write_text("after")
        subdir = self.root / "nested directory"
        subdir.mkdir()
        self.succeeds("run", "b", cwd=subdir)
        builds = [
            args for name, args in self.calls() if name == "go" and args[0] == "build"
        ]
        self.assertEqual(len(builds), 2)
        self.assertTrue((self.root / "dist/jira-linux-amd64").is_file())

    def test_docs_serve_forwards_literal_native_options(self) -> None:
        address = "127.0.0.1:8123"
        self.succeeds("run", "docs:serve", "--", "--dev-addr", address)
        self.assertEqual(
            self.calls(),
            [
                [
                    "uvx",
                    [
                        "--python",
                        "3.14",
                        "--from",
                        "zensical==0.0.60",
                        "zensical",
                        "serve",
                        "-f",
                        "zensical.toml",
                        "--dev-addr",
                        address,
                    ],
                ]
            ],
        )

    def test_snapshot_disables_local_signing(self) -> None:
        self.succeeds("run", "release:snapshot")
        self.assertEqual(
            self.calls(),
            [["goreleaser", ["release", "--snapshot", "--clean", "--skip=sign"]]],
        )

    def test_markdown_lint_forwards_literal_paths(self) -> None:
        path = "notes with spaces.md"
        self.succeeds("run", "rumdl", "--", path)
        self.succeeds("run", "rumdl:fix", "--", path)
        self.assertEqual(
            self.calls(), [["rumdl", ["check", path]], ["rumdl", ["fmt", path]]]
        )

    def test_clean_removes_only_owned_outputs(self) -> None:
        for directory in ("bin", "dist"):
            target = self.root / directory
            target.mkdir()
            (target / "fixture").write_text("output")
        (self.root / "coverage.out").write_text("coverage")
        retained = self.root / "keep this.md"
        retained.write_text("keep")
        self.succeeds("run", "clean")
        self.assertEqual(retained.read_text(), "keep")
        for member in ("bin", "dist", "coverage.out"):
            self.assertFalse((self.root / member).exists())

    def test_dependency_update_patch_and_minor_policies(self) -> None:
        self.succeeds("run", "deps:update")
        self.assertIn(["go", ["get", "example.com/library@v1.2.3"]], self.calls())
        self.assertEqual(self.calls()[-1], ["go", ["mod", "tidy"]])
        self.trace.unlink()
        self.succeeds("run", "deps:update:minor")
        self.assertIn(["go", ["get", "example.com/library@v1.3.0"]], self.calls())

    def test_dependency_lookup_failure_prevents_mutation(self) -> None:
        for failure in ("modules", "versions"):
            with self.subTest(failure=failure):
                self.trace.unlink(missing_ok=True)
                self.env["TASK_TEST_FAILURE"] = failure
                result = self.run_mise("run", "deps:update")
                self.assertNotEqual(result.returncode, 0)
                self.assertTrue(self.calls())
                self.assertTrue(all(args[0] == "list" for _, args in self.calls()))

    def test_upstream_native_flags_and_help_without_network(self) -> None:
        task = self.root / ".mise/tasks/upstream-check.py"
        header = task.read_text().split('"""', 1)[0]
        # Substitute the network worker, retaining its actual launcher, runtime
        # metadata and native Usage declarations.
        task.write_text(
            header
            + "import os, json\nprint(json.dumps([os.environ['usage_type'], os.environ.get('usage_check', 'false')]))\n"
        )
        output = self.succeeds("run", "upstream-check", "--help")
        self.assertIn("adf, api, all", output)
        for arguments, expected in [
            ((), ["all", "false"]),
            (("--type", "adf", "--check"), ["adf", "true"]),
        ]:
            with self.subTest(arguments=arguments):
                self.assertEqual(
                    json.loads(self.succeeds("run", "upstream-check", *arguments)),
                    expected,
                )
        for arguments in (("--type", "invalid"), ("--oops",), ("extra",)):
            with self.subTest(arguments=arguments):
                result = self.run_mise("run", "upstream-check", *arguments)
                self.assertNotEqual(result.returncode, 0)

    def test_serial_phases_and_failed_prerequisites(self) -> None:
        cases = {
            "ci": [
                "tidy",
                "check",
                "test:race",
                "test:matrix",
                "test:live:guards",
                "test:hooks",
                "test:release",
                "test:tasks",
                "audit",
                "workflow:lint",
                "security",
            ],
            "fix": ["fmt", "lint:fix", "rumdl:fix"],
            "release:preflight": ["ci", "release:check", "release:build"],
        }
        for parent, children in cases.items():
            for failed in (False, True):
                with self.subTest(parent=parent, failed=failed):
                    self.trace.unlink(missing_ok=True)
                    tasks = {parent: TASKS[parent]}
                    for index, child in enumerate(children):
                        command = f"printf '%s\\n' {shlex.quote(child)} >> {shlex.quote(str(self.trace))}"
                        if failed and index == 0:
                            command += "\nexit 37"
                        tasks[child] = {"run": command}
                    self.write_config({"tasks": tasks})
                    result = self.run_mise("run", parent)
                    observed = self.trace.read_text().splitlines()
                    self.assertEqual(
                        result.returncode == 0,
                        not failed,
                        result.stdout + result.stderr,
                    )
                    self.assertEqual(observed[0], children[0])
                    self.assertEqual(
                        set(observed), set(children[:1] if failed else children)
                    )
                    self.assertEqual(len(observed), 1 if failed else len(children))
                    if parent == "release:preflight" and not failed:
                        self.assertEqual(observed, children)


if __name__ == "__main__":
    unittest.main(verbosity=2)
