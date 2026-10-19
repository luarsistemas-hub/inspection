#!/usr/bin/env python3
"""Fail when required Go tests are missing, skipped, or did not pass."""

from __future__ import annotations

import argparse
import json
import sys
from collections.abc import Iterable
from typing import TextIO


def check_stream(lines: Iterable[str], required: set[str]) -> list[str]:
    terminal: dict[str, str] = {}
    errors: list[str] = []
    for line_number, line in enumerate(lines, 1):
        if not line.strip():
            continue
        try:
            event = json.loads(line)
        except json.JSONDecodeError as exc:
            errors.append(f"line {line_number}: invalid go test JSON: {exc}")
            continue
        name = event.get("Test")
        action = event.get("Action")
        if name in required and action in {"pass", "skip", "fail"}:
            terminal[name] = action

    for name in sorted(required):
        action = terminal.get(name)
        if action is None:
            errors.append(f"required test did not execute: {name}")
        elif action == "skip":
            errors.append(f"required test was skipped: {name}")
        elif action != "pass":
            errors.append(f"required test did not pass (action={action}): {name}")
    return errors


def main(argv: list[str] | None = None, stdin: TextIO = sys.stdin) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--required", action="append", required=True, help="exact Go test name; repeat for each required test")
    args = parser.parse_args(argv)
    errors = check_stream(stdin, set(args.required))
    if errors:
        for error in errors:
            print(error, file=sys.stderr)
        return 1
    print(f"All {len(set(args.required))} required Go tests passed without skips.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
