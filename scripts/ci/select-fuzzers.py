#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Pick the fuzzers a pull request needs to run.

    select-fuzzers.py <fuzzer-list.json> <repo-root> < NUL-separated-changed-files

A fuzzer is selected when its package, or any package it imports (directly or
transitively, test imports included) inside this module, holds a changed file.
The import graph comes from `go list`, not from parsing source, so aliased,
dot, blank and grouped imports all resolve the way the compiler sees them.

Build constraints: one `go list` run sees one configuration, so the graph is
the union of runs for linux, darwin and windows, each with the `integration`
tag. If any Go file is ignored in EVERY run (an unknown tag, a constraint no
run satisfies) the graph can't be trusted and this exits non-zero, so the
caller runs the full list.

A changed file belongs to the nearest ancestor directory that is a package, so
testdata/ (fuzz seed corpora) and embedded assets count toward their package.
Files outside every package (docs, website, workflows) select nothing; the
caller decides which of those must run everything.

Prints one compact JSON array. Any problem exits non-zero.
"""

import json
import os
import subprocess
import sys

CONFIGS = (
    {"GOOS": "linux", "GOARCH": "amd64"},
    {"GOOS": "darwin", "GOARCH": "arm64"},
    {"GOOS": "windows", "GOARCH": "amd64"},
)
TAGS = "integration"


def go_list(root, config):
    env = dict(os.environ, CGO_ENABLED="0", **config)
    result = subprocess.run(
        ["go", "list", "-e", "-tags", TAGS, "-json=ImportPath,Dir,Imports,TestImports,XTestImports,IgnoredGoFiles,Error", "./..."],
        cwd=root, env=env, capture_output=True, text=True, timeout=180, check=False,
    )
    if result.returncode != 0:
        raise SystemExit(f"go list failed for {config}: {result.stderr.strip()}")
    decoder = json.JSONDecoder()
    text, pos, packages = result.stdout, 0, []
    while pos < len(text):
        while pos < len(text) and text[pos].isspace():
            pos += 1
        if pos >= len(text):
            break
        package, pos = decoder.raw_decode(text, pos)
        packages.append(package)
    return packages


def build_graph(root):
    graph = {}
    by_path = {}
    ignored_everywhere = None
    for config in CONFIGS:
        ignored = set()
        for package in go_list(root, config):
            directory = os.path.relpath(package["Dir"], root)
            by_path[package["ImportPath"]] = directory
            deps = graph.setdefault(directory, set())
            for key in ("Imports", "TestImports", "XTestImports"):
                deps.update(package.get(key) or ())
            for name in package.get("IgnoredGoFiles") or ():
                ignored.add(os.path.join(directory, name))
        ignored_everywhere = ignored if ignored_everywhere is None else ignored_everywhere & ignored
    if ignored_everywhere:
        raise SystemExit(f"Go files excluded in every configuration: {sorted(ignored_everywhere)}")
    # Import paths -> directories, restricted to this module's own packages.
    resolved = {}
    for directory, imports in graph.items():
        resolved[directory] = {by_path[i] for i in imports if i in by_path}
    return resolved


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
    graph = build_graph(root)

    changed = set()
    for entry in sys.stdin.buffer.read().split(b"\0"):
        if not entry:
            continue
        package = owning_package(graph, os.fsdecode(entry))
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
