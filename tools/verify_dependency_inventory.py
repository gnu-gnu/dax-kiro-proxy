#!/usr/bin/env python3
"""Check a reviewed dependency snapshot's bytes, offline; not a license clearance tool."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import stat
import sys


ROOT = Path(__file__).resolve().parents[1]
REPORT = ROOT / "third_party/inventory/macos-arm64-development.json"


def safe_path(base, relative):
    if not isinstance(relative, str) or not relative or len(relative) > 1024:
        raise ValueError("invalid inventory path")
    parts = relative.split("/")
    if any(part in ("", ".", "..") for part in parts):
        raise ValueError("invalid inventory path")
    path = base.joinpath(*parts)
    path.resolve().relative_to(base.resolve())
    return path


def read_regular(path, limit):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    try:
        before = os.fstat(fd)
        if not stat.S_ISREG(before.st_mode) or before.st_size > limit:
            raise ValueError("invalid inventory input type or size")
        with os.fdopen(fd, "rb", closefd=False) as source:
            data = source.read(limit + 1)
        after = os.fstat(fd)
        if (len(data) != before.st_size or before.st_size != after.st_size
                or before.st_mtime_ns != after.st_mtime_ns):
            raise ValueError("inventory input changed while reading")
        return data
    finally:
        os.close(fd)


def check_record(path, record, limit):
    data = read_regular(path, limit)
    if len(data) != record["bytes"] or hashlib.sha256(data).hexdigest() != record["sha256"]:
        raise ValueError("inventory byte mismatch")


def verify(cache, binary=None, report_path=None):
    report = json.loads(read_regular(report_path or REPORT, 1 << 20))
    if report["format"] != 1 or report["release_clearance"] is not False:
        raise ValueError("unsupported snapshot")
    if (len(report["notices"]) > 32 or len(report["modules"]) > 32
            or len(report["repository_inputs"]) > 512):
        raise ValueError("inventory entry limit")
    count = 0
    for record in report["repository_inputs"] + report["notices"]:
        check_record(safe_path(ROOT, record["path"]), record, 1 << 20)
        count += 1
    if "prior_snapshot" in report:
        record = report["prior_snapshot"]
        check_record(safe_path(ROOT, record["path"]), record, 1 << 20)
        count += 1
    for module in [report["toolchain"]] + report["modules"]:
        directory = safe_path(cache, module["cache_directory"])
        for record in module["notices"]:
            check_record(safe_path(directory, record["path"]), record, 1 << 20)
            count += 1
    schemas_path = safe_path(ROOT, report["metaschemas"]["path"])
    check_record(schemas_path, report["metaschemas"], 1 << 20)
    schemas = json.loads(read_regular(schemas_path, 1 << 20))
    if len(schemas["resources"]) != report["metaschemas"]["resource_count"]:
        raise ValueError("metaschema count mismatch")
    directory = safe_path(cache, schemas["cache_directory"])
    seen = set()
    for record in schemas["resources"]:
        relative = record["embedded_path"]
        if relative in seen:
            raise ValueError("duplicate metaschema")
        seen.add(relative)
        check_record(safe_path(directory, relative), {
            "bytes": record["embedded_bytes"], "sha256": record["embedded_sha256"]}, 65536)
        count += 1
    if binary is not None:
        check_record(binary, report["binary"], 128 << 20)
        count += 1
    return {"checked_file_records": count + 1, "binary_checked": binary is not None,
            "snapshot_bytes_match": True, "release_clearance": False}

COMPONENTS = ROOT / "third_party/inventory/components.json"


def verify_components(cache):
    report = json.loads(read_regular(COMPONENTS, 1 << 20))
    if report["format"] != 1 or report["release_clearance"] is not False:
        raise ValueError("unsupported components record")
    if len(report["linked_modules"]) > 32 or len(report["retained_notices"]) > 32:
        raise ValueError("components entry limit")
    requires = set()
    for line in read_regular(ROOT / "go.mod", 65536).decode().splitlines():
        parts = line.split()
        if len(parts) == 2 and "/" in parts[0] and parts[1].startswith("v"):
            requires.add((parts[0], parts[1]))
    sums = set()
    for line in read_regular(ROOT / "go.sum", 65536).decode().splitlines():
        parts = line.split()
        if len(parts) == 3 and not parts[1].endswith("/go.mod"):
            sums.add((parts[0], parts[1], parts[2]))
    count = 0
    notices = {record["path"] for record in report["retained_notices"]}
    for record in report["retained_notices"]:
        check_record(safe_path(ROOT, record["path"]), record, 1 << 20)
        count += 1
    for module in report["linked_modules"]:
        if (module["path"], module["version"]) not in requires:
            raise ValueError("linked module absent from go.mod")
        if (module["path"], module["version"], module["sum"]) not in sums:
            raise ValueError("linked module sum absent from go.sum")
        directory = safe_path(cache, module["path"] + "@" + module["version"])
        for key in ("license_file", "patents_file"):
            if key in module:
                check_record(safe_path(directory, module[key]["path"]), module[key], 1 << 20)
                count += 1
        for key in ("retained_notice", "retained_patents_notice"):
            if key in module and module[key] not in notices:
                raise ValueError("linked module notice not retained")
    if len(requires) != len(report["linked_modules"]):
        raise ValueError("go.mod requirement not covered by a linked module record")
    toolchain = report["toolchain"]
    directory = safe_path(cache, toolchain["cache_directory"])
    for key in ("license_file", "patents_file"):
        check_record(safe_path(directory, toolchain[key]["path"]), toolchain[key], 1 << 20)
        count += 1
    for resource in report["embedded_resources"]:
        check_record(safe_path(ROOT, resource["inventory"]["path"]), resource["inventory"], 1 << 20)
        count += 1
    return {"checked_file_records": count, "components_bytes_match": True,
            "linked_modules": len(report["linked_modules"]), "release_clearance": False}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gomodcache", type=Path, required=True)
    parser.add_argument("--binary", type=Path,
                        help="also require byte identity with the recorded development artifact")
    parser.add_argument("--components", action="store_true",
                        help="verify the D121 component record instead of an artifact snapshot")
    parser.add_argument("--snapshot",
                        choices=("development", "installation", "native-history", "relay-close", "effort", "usage", "output-styles", "tool-images", "client-version", "measured-client", "measured-kiro", "onboarding", "project-trust", "run-diagnostics"),
                        default="development",
                        help="select the frozen D78, D79, D87, D96, D99, D101, D108, D109, D110–D112, D113, D114, D115, D118 or D122 artifact record")
    args = parser.parse_args()
    try:
        if args.components:
            result = verify_components(args.gomodcache)
        else:
            report = ROOT / ("third_party/inventory/macos-arm64-" + args.snapshot + ".json")
            result = verify(args.gomodcache, args.binary, report)
    except (OSError, ValueError, KeyError, TypeError):
        print("dependency snapshot verification failed", file=sys.stderr)
        return 1
    print(json.dumps(result, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
