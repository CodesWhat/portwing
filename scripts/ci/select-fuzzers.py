#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Pick the fuzzers a pull request needs to run.

    select-fuzzers.py <fuzzer-list.json> <repo-root> < changed-files

A fuzzer is selected when its package, or any package it imports (directly or
transitively, test files included) inside this module, holds a changed file.
A changed file belongs to the nearest ancestor directory that holds Go files,
so testdata/ and embedded assets count toward their package. Files outside
every Go package (docs, website, workflows) select nothing; the caller decides
which of those must run everything.

Prints one compact JSON array. Any problem exits non-zero so the caller can
fall back to the full list.
"""

import json
import os
import re
import sys

SKIP_DIRS = {".git", ".claude", ".planning", "node_modules", "website", "docs", "vendor"}
IMPORT_RE = re.compile(r'^\s*(?:[\w.]+\s+)?"([^"\n]+)"', re.MULTILINE)


def module_path(root):
    with open(os.path.join(root, "go.mod"), encoding="utf-8") as handle:
        for line in handle:
            if line.startswith("module "):
                return line.split()[1]
    raise SystemExit("go.mod has no module line")


def build_graph(root, module):
    prefix = module + "/"
    graph = {}
    for current, dirs, files in os.walk(root):
        dirs[:] = [d for d in dirs if d not in SKIP_DIRS]
        go_files = [f for f in files if f.endswith(".go")]
        if not go_files:
            continue
        rel = os.path.relpath(current, root)
        deps = graph.setdefault(rel, set())
        for name in go_files:
            with open(os.path.join(current, name), encoding="utf-8") as handle:
                source = handle.read()
            for imported in IMPORT_RE.findall(source):
                if imported.startswith(prefix):
                    deps.add(imported[len(prefix):])
    return graph


def closure(graph, start):
    seen = {start}
    stack = [start]
    while stack:
        for dep in graph.get(stack.pop(), ()):
            if dep not in seen:
                seen.add(dep)
                stack.append(dep)
    return seen


def owning_package(graph, path):
    directory = os.path.dirname(path)
    while directory:
        if directory in graph:
            return directory
        directory = os.path.dirname(directory)
    return None


def main():
    if len(sys.argv) != 3:
        raise SystemExit(__doc__)
    list_path, root = sys.argv[1], sys.argv[2]
    with open(list_path, encoding="utf-8") as handle:
        fuzzers = json.load(handle)
    graph = build_graph(root, module_path(root))

    changed = set()
    for line in sys.stdin.read().splitlines():
        package = owning_package(graph, line.strip())
        if package:
            changed.add(package)

    selected = []
    for fuzzer in fuzzers:
        package = os.path.normpath(fuzzer["pkg"])
        if package not in graph:
            raise SystemExit(f"{fuzzer['name']}: package {package} not found in the module")
        if closure(graph, package) & changed:
            selected.append(fuzzer)
    print(json.dumps(selected, separators=(",", ":")))


if __name__ == "__main__":
    main()
