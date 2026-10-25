#!/usr/bin/env -S uv run --python 3.15 --script
# [MISE] description="Check vendored specs/schemas against live upstream"
# [USAGE] unknown_flags "error"
# [USAGE] flag "--type <type>" help="Pins to check" default="all" {
# [USAGE]   choices "adf" "api" "all"
# [USAGE] }
# [USAGE] flag "--check" help="Exit nonzero if any pin is outdated or unverifiable"
# /// script
# requires-python = ">=3.15,<3.16"
# dependencies = []
# ///
"""upstream-check: pull what upstream serves today and report whether the
vendored pins are out of date — and what actually changed, path by path
inside each definition. Read-only: never rewrites a pin or the working
tree; bumping stays a deliberate act (clover + the documented procedures).

  --type adf|api|all   which pins to check (default all)
  --check              exit 1 when anything is out of date (CI/cron use)

UPSTREAM_CHECK_ADF_VERSION overrides the "latest" adf-schema version, so
the differ itself can be exercised against any published version.
"""

import hashlib
import io
import json
import os
import re
import sys
import tarfile
import urllib.error
import urllib.request
from collections.abc import Iterator
from pathlib import Path
from typing import Any

ADF_SCHEMA_README = Path("internal/adf/schema/README.md")
ADF_SCHEMA_FILE = Path("internal/adf/schema/full.json")
API_SPEC_GO = Path("internal/jira/spec/spec.go")
API_SPEC_DIR = Path("internal/jira/spec")
NPM_REGISTRY = "https://registry.npmjs.org/@atlaskit/adf-schema"

# Definitions with more changed paths than this get elided — a reshaped
# schema can touch hundreds of leaves, and the point is orientation, not a
# full dump (the real diff happens at the reviewed bump).
MAX_PATHS_PER_DEFINITION = 8


def fetch(url: str) -> bytes:
    with urllib.request.urlopen(url, timeout=60) as resp:
        return resp.read()


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def summarize(value: Any, limit: int = 60) -> str:
    text = json.dumps(value, separators=(",", ":"))
    return text if len(text) <= limit else text[: limit - 1] + "…"


def walk_diff(old: Any, new: Any, path: str = "") -> Iterator[tuple[str, str, str]]:
    """Yield (path, kind, detail) leaves where two JSON values diverge."""
    if type(old) is not type(new):
        yield path, "changed", f"{summarize(old)} -> {summarize(new)}"
        return
    if isinstance(old, dict):
        for key in sorted(old.keys() | new.keys()):
            sub = f"{path}.{key}" if path else key
            if key not in new:
                yield sub, "removed", summarize(old[key])
            elif key not in old:
                yield sub, "added", summarize(new[key])
            else:
                yield from walk_diff(old[key], new[key], sub)
    elif isinstance(old, list):
        if old == new:
            return
        # Order matters in JSON Schema lists (enum, required, anyOf), but
        # the common change is an append — report set-style adds/removes
        # when possible, positional old -> new otherwise.
        added = [x for x in new if x not in old]
        removed = [x for x in old if x not in new]
        if added or removed:
            parts = []
            if added:
                parts.append(f"added {summarize(added)}")
            if removed:
                parts.append(f"removed {summarize(removed)}")
            yield path, "changed", "; ".join(parts)
        else:
            yield path, "changed", f"{summarize(old)} -> {summarize(new)}"
    elif old != new:
        yield path, "changed", f"{summarize(old)} -> {summarize(new)}"


def report_definitions(old: dict[str, Any], new: dict[str, Any]) -> None:
    for name in sorted(old.keys() | new.keys()):
        if name not in new:
            print(f"  removed: {name}")
            continue
        if name not in old:
            print(f"  added:   {name}")
            continue
        paths = list(walk_diff(old[name], new[name]))
        if not paths:
            continue
        print(f"  changed: {name}")
        for p, kind, detail in paths[:MAX_PATHS_PER_DEFINITION]:
            print(f"    {p or '(root)'}: {kind} {detail}")
        if len(paths) > MAX_PATHS_PER_DEFINITION:
            print(f"    … and {len(paths) - MAX_PATHS_PER_DEFINITION} more paths")


def check_adf() -> bool:
    readme = ADF_SCHEMA_README.read_text(encoding="utf-8")
    match = re.search(r"Version: \*\*([^*]+)\*\*", readme)
    if match is None:
        sys.exit(
            f"{ADF_SCHEMA_README} no longer records the package version — cannot compare"
        )
    pinned_version = match.group(1)

    latest = os.environ.get("UPSTREAM_CHECK_ADF_VERSION")
    if not latest:
        latest = json.loads(fetch(f"{NPM_REGISTRY}/latest"))["version"]

    if latest == pinned_version:
        print(f"adf-schema: up to date ({pinned_version})")
        return False

    print(f"adf-schema: pinned {pinned_version}, upstream {latest}")
    tarball = fetch(f"{NPM_REGISTRY}/-/adf-schema-{latest}.tgz")
    with tarfile.open(fileobj=io.BytesIO(tarball), mode="r:gz") as tar:
        member = tar.extractfile("package/json-schema/v1/full.json")
        if member is None:
            print(
                "  package no longer ships json-schema/v1/full.json — investigate before bumping"
            )
            return True
        with member:
            fetched_raw = member.read()

    pinned_raw = ADF_SCHEMA_FILE.read_bytes()
    if sha256(pinned_raw) == sha256(fetched_raw):
        # The package versions faster than the schema: three of the last
        # four releases shipped a byte-identical full.json.
        print(
            "  schema content identical — only the package version moved; bump is pin-only"
        )
        return True

    report_definitions(
        json.loads(pinned_raw).get("definitions", {}),
        json.loads(fetched_raw).get("definitions", {}),
    )
    print(
        "  bump: internal/adf/schema/README.md procedure, then `go generate ./internal/adf`"
    )
    return True


def check_api() -> bool:
    # The pins live in spec.go as Go struct literals; pull each field out by
    # name rather than assuming declaration order.
    src = API_SPEC_GO.read_text(encoding="utf-8")
    # The findall also matches the `[]Pin{Platform, Agile}` list literal in
    # Pins(); only blocks that actually carry fields are pins.
    pins = [
        fields
        for block in re.findall(r"Pin\{(.*?)\}", src, re.DOTALL)
        if (
            fields := dict(re.findall(r'(File|URL|Version|SHA256):\s*"([^"]*)"', block))
        )
    ]

    stale = False
    for pin in pins:
        missing = {"File", "URL", "Version", "SHA256"} - pin.keys()
        if missing:
            sys.exit(
                f"{API_SPEC_GO}: pin missing {sorted(missing)} — parser and file have drifted"
            )

        # One pin failing to fetch must not hide the others' status; an
        # unverifiable pin still counts against --check (can't prove current).
        try:
            fetched_raw = fetch(pin["URL"])
        except (urllib.error.URLError, TimeoutError) as e:
            print(f"{pin['File']}: fetch failed ({e}) — cannot verify")
            stale = True
            continue

        if sha256(fetched_raw) == pin["SHA256"]:
            print(f"{pin['File']}: up to date ({pin['Version']})")
            continue

        stale = True
        fetched = json.loads(fetched_raw)
        upstream_version = fetched.get("info", {}).get("version", "?")
        print(f"{pin['File']}: pinned {pin['Version']}, upstream {upstream_version}")

        pinned = json.loads((API_SPEC_DIR / pin["File"]).read_text(encoding="utf-8"))
        report_definitions(
            pinned.get("components", {}).get("schemas", {}),
            fetched.get("components", {}).get("schemas", {}),
        )
        print(
            "  bump: re-vendor per internal/jira/spec/README.md, then `go generate ./internal/jira`"
        )
    return stale


def main() -> int:
    selected_type = os.environ.get("usage_type")
    if selected_type not in ("adf", "api", "all"):
        sys.exit("upstream-check: use mise run upstream-check --help")
    check = os.environ.get("usage_check", "false") == "true"

    stale = False
    if selected_type in ("adf", "all"):
        stale |= check_adf()
    if selected_type in ("api", "all"):
        stale |= check_api()

    if stale:
        if check:
            return 1
        print("out of date — nothing was modified; bumps are deliberate.")
    return 0


if __name__ == "__main__":
    # The boundary handler: network and decode failures are expected weather
    # for a probe that talks to two registries — report them plainly, exit
    # nonzero, never dump a traceback at the operator.
    try:
        sys.exit(main())
    except (urllib.error.URLError, TimeoutError) as e:
        sys.exit(f"upstream-check: network failure: {e}")
    except (json.JSONDecodeError, tarfile.TarError, KeyError) as e:
        sys.exit(f"upstream-check: upstream served an unexpected shape: {e!r}")
