#!/usr/bin/env python3
"""Prepare a local annotated release tag; push only when explicitly requested."""
import argparse
from pathlib import Path
import re
import subprocess
import sys

SEMVER = re.compile(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\Z")
ROOT = Path(__file__).resolve().parent.parent


def git(*args):
    return subprocess.run(["git", *args], cwd=ROOT, check=True, text=True,
                          stdout=subprocess.PIPE).stdout.strip()


def check_tree():
    if git("branch", "--show-current") != "main":
        raise ValueError("releases must be created from main")
    if git("status", "--porcelain", "--untracked-files=all"):
        raise ValueError("working tree not clean - commit or stash first")


def release(args):
    check_tree()
    original = git("rev-parse", "HEAD")
    version = args.version
    if version in ("major", "minor", "patch"):
        versions = [tuple(map(int, tag[1:].split(".")))
                    for tag in git("tag", "--list", "v*").splitlines()
                    if SEMVER.fullmatch(tag[1:])]
        current = list(max(versions, default=(0, 0, 0)))
        index = ("major", "minor", "patch").index(version)
        current[index] += 1
        current[index + 1:] = [0] * (2 - index)
        version = ".".join(map(str, current))
    if not SEMVER.fullmatch(version):
        raise ValueError(f"Invalid VERSION: {version}")
    tag = "v" + version
    if git("tag", "--list", tag):
        raise ValueError(f"Tag already exists: {tag}")
    print(f"Release tag: {tag}", flush=True)
    if not args.skip_tests:
        subprocess.run(["make", "test"], cwd=ROOT, check=True)
    check_tree()
    if git("rev-parse", "HEAD") != original:
        raise ValueError("HEAD changed during preparation; no tag created")
    if args.dry_run:
        print(f"[dry-run] Would create annotated tag {tag}")
        return
    subprocess.run(["git", "tag", "-a", tag, "-m", tag, original], cwd=ROOT, check=True)
    print(f"Created tag {tag}", flush=True)
    if args.autopush:
        subprocess.run(["git", "push", "--atomic", "origin", "HEAD:refs/heads/main",
                        f"refs/tags/{tag}:refs/tags/{tag}"], cwd=ROOT, check=True)
    else:
        print(f"To trigger release: git push --atomic origin HEAD:main {tag}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True)
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--skip-tests", action="store_true")
    parser.add_argument("--autopush", action="store_true")
    try:
        release(parser.parse_args())
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        print(f"Error: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
