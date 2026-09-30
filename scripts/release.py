#!/usr/bin/env python3
"""Stdlib-only release packaging, provenance inventory, and manifest generation."""
import argparse
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import tempfile
import uuid

ROOT = Path(__file__).resolve().parent.parent
PIN = json.loads((ROOT / "upstream.json").read_text())
NAMES = ("rustdesk-server", "hbbs", "hbbr")


def command(*args, cwd=None):
    return subprocess.check_output(args, cwd=cwd, text=True).strip()


def sha256(path):
    digest = hashlib.sha256()
    with Path(path).open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def write_json(path, value):
    Path(path).write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def validate_version(version):
    if not re.fullmatch(r"(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?", version):
        raise ValueError("Expected a safe semver version without a leading v")


def check_upstream(upstream):
    upstream = Path(upstream).resolve()
    if command("git", "rev-parse", "HEAD", cwd=upstream) != PIN["commit"]:
        raise ValueError("Upstream commit does not match upstream.json")
    actual = {}
    for line in command("git", "submodule", "status", "--recursive", cwd=upstream).splitlines():
        if line[0] in "-+U":
            raise ValueError("Submodule is uninitialized, dirty, or conflicted: " + line)
        commit, path, *_ = line.strip().split()
        actual[path] = commit
    if actual != PIN["submodules"]:
        raise ValueError(f"Submodule pins differ: {actual}")
    for path in [upstream, *(upstream / p for p in actual)]:
        if command("git", "diff", "HEAD", "--", cwd=path):
            raise ValueError(f"Tracked upstream files have been modified: {path}")
    if not (upstream / "Cargo.lock").is_file():
        raise ValueError("Upstream Cargo.lock is missing")


def source_metadata(upstream, version, arch):
    return {
        "distribution": {
            "repository": os.environ.get("GITHUB_REPOSITORY", "webkaz-labs/rustdesk-server-macos"),
            "version": version,
            "commit": command("git", "rev-parse", "HEAD", cwd=ROOT),
        },
        "upstream": {**PIN, "cargo_lock_sha256": sha256(upstream / "Cargo.lock")},
        "build": {
            "architecture": arch, "os": "darwin", "minimum_macos": PIN["minimum_macos"],
            "go": command("go", "version"),
            "rustc": command("rustc", "+" + PIN["rust_toolchain"], "--version", "--verbose"),
            "macos": command("sw_vers", "-productVersion"),
            "xcode": command("xcodebuild", "-version"),
            "sdk": command("xcrun", "--show-sdk-version"),
        },
        "corresponding_source_asset": f"rustdesk-server-macos-{version}-source.tar.gz",
    }


def collect_licenses(packages, out):
    """Copy available upstream copyright/license notices, with an honest index."""
    out.mkdir(parents=True, exist_ok=True)
    records = []
    for package in sorted(packages, key=lambda p: (p["name"], p["version"], p["id"])):
        base = Path(package["manifest_path"]).parent
        key = f'{package["name"]}-{package["version"]}-{hashlib.sha256(package["id"].encode()).hexdigest()[:8]}'
        files = set()
        for pattern in ("LICENSE*", "LICENCE*", "COPYING*", "NOTICE*", "COPYRIGHT*", "UNLICENSE*", "license*", "licence*"):
            files.update(p for p in base.glob(pattern) if p.is_file())
        if package.get("license_file"):
            candidate = base / package["license_file"]
            if candidate.is_file():
                files.add(candidate)
        copied = []
        for file in sorted(files):
            target = out / key / file.name
            target.parent.mkdir(exist_ok=True)
            shutil.copyfile(file, target)
            copied.append(str(target.relative_to(out)))
        records.append({"name": package["name"], "version": package["version"],
                        "id": package["id"], "license": package.get("license"), "files": copied})
    write_json(out / "index.json", records)
    (out / "README.txt").write_text(
        "License expressions are package authors' Cargo metadata. Available top-level\n"
        "license and notice files are copied here; this is not a legal conclusion.\n"
        "See the matching complete source archive for nested notices, bundled C sources,\n"
        "and the full source of registry and Git dependencies. Some packages do not\n"
        "ship separate license files; index.json records those entries explicitly.\n")


def metadata(upstream, metadata_file, stage, version, arch):
    upstream, stage = Path(upstream), Path(stage)
    check_upstream(upstream)
    raw = json.loads(Path(metadata_file).read_text())
    nodes = raw["resolve"]["nodes"]
    active = {node["id"] for node in nodes}
    packages = [p for p in raw["packages"] if p["id"] in active]
    out = stage / "share/rustdesk-server"
    out.mkdir(parents=True, exist_ok=True)
    details = source_metadata(upstream, version, arch)
    details["binaries"] = {name: {"sha256": sha256(stage / "bin" / name)} for name in NAMES}
    write_json(out / "source.json", details)
    collect_licenses(packages, out / "licenses")
    shutil.copyfile(upstream / "LICENSE", out / "licenses/AGPL-3.0-upstream.txt")
    shutil.copyfile(ROOT / "LICENSE", out / "licenses/distribution-LICENSE.txt")
    shutil.copyfile(upstream / "Cargo.lock", out / "Cargo.lock")
    component_ids = {p["id"]: "cargo:" + hashlib.sha256(p["id"].encode()).hexdigest() for p in packages}
    components = []
    for package in packages:
        component = {"type": "library", "bom-ref": component_ids[package["id"]],
                     "name": package["name"], "version": package["version"],
                     "properties": [{"name": "cargo:package-id", "value": package["id"]}]}
        # `license.name` preserves legacy Cargo expressions without inventing SPDX IDs.
        if package.get("license"):
            component["licenses"] = [{"license": {"name": package["license"]}}]
        if package.get("source", "") and package["source"].startswith("registry+"):
            component["purl"] = f'pkg:cargo/{package["name"]}@{package["version"]}'
        components.append(component)
    for name in NAMES:
        components.append({"type": "application", "bom-ref": "binary:" + name,
                           "name": name, "version": version if name == "rustdesk-server" else PIN["version"],
                           "hashes": [{"alg": "SHA-256", "content": details["binaries"][name]["sha256"]}]})
    dependencies = [{"ref": component_ids[node["id"]],
                     "dependsOn": sorted(component_ids[d["pkg"]] for d in node["deps"] if d["pkg"] in component_ids)}
                    for node in nodes if node["id"] in component_ids]
    root_id = component_ids[raw["resolve"]["root"]]
    dependencies.extend([{"ref": "binary:hbbs", "dependsOn": [root_id]},
                         {"ref": "binary:hbbr", "dependsOn": [root_id]},
                         {"ref": "binary:rustdesk-server", "dependsOn": []}])
    bom = {"bomFormat": "CycloneDX", "specVersion": "1.5", "version": 1,
           "serialNumber": "urn:uuid:" + str(uuid.uuid5(uuid.NAMESPACE_URL, json.dumps(details, sort_keys=True))),
           "metadata": {"component": {"type": "application", "name": "rustdesk-server-macos", "version": version},
                        "properties": [{"name": "inventory:scope", "value": "Cargo target-filtered dependency graph including build dependencies; Go helper uses only standard library; system SDK libraries are not vendored"},
                                       {"name": "build:go", "value": details["build"]["go"]},
                                       {"name": "build:target", "value": "darwin-" + arch}]},
           "components": components, "dependencies": dependencies}
    write_json(out / "bom.cdx.json", bom)


def archive(source, destination):
    """Stable ordering/owners/mode timestamps; gzip header has no local filename."""
    source, destination = Path(source), Path(destination)
    destination.parent.mkdir(parents=True, exist_ok=True)
    epoch = int(os.environ.get("SOURCE_DATE_EPOCH", "0"))
    with destination.open("wb") as raw, gzip.GzipFile(fileobj=raw, mode="wb", filename="", mtime=epoch) as zipped:
        with tarfile.open(fileobj=zipped, mode="w") as tar:
            for path in sorted(source.rglob("*")):
                entry = tar.gettarinfo(str(path), arcname=str(path.relative_to(source)))
                entry.uid = entry.gid = 0
                entry.uname = entry.gname = "root"
                entry.mtime = epoch
                if path.is_file() and not path.is_symlink():
                    entry.mode = 0o755 if os.access(path, os.X_OK) else 0o644
                    with path.open("rb") as stream:
                        tar.addfile(entry, stream)
                else:
                    if path.is_dir():
                        entry.mode = 0o755
                    tar.addfile(entry)


def export_git(repo, destination):
    # git archive exports tracked source only: never CI state, .git, build outputs,
    # server databases, generated keys, environment secrets or untracked files.
    destination.mkdir(parents=True, exist_ok=True)
    data = subprocess.check_output(["git", "archive", "HEAD"], cwd=repo)
    with tarfile.open(fileobj=io.BytesIO(data)) as tar:
        tar.extractall(destination, filter="data")


def source_bundle(upstream, vendor, vendor_config, version):
    upstream, vendor = Path(upstream).resolve(), Path(vendor).resolve()
    check_upstream(upstream)
    if not any(vendor.glob("*/.cargo-checksum.json")):
        raise ValueError("Cargo vendor output is empty")
    with tempfile.TemporaryDirectory(prefix="source-", dir=ROOT / ".build") as temporary:
        stage = Path(temporary)
        export_git(upstream, stage / "upstream")
        for submodule in PIN["submodules"]:
            export_git(upstream / submodule, stage / "upstream" / submodule)
        export_git(ROOT, stage / "distribution")
        shutil.copytree(vendor, stage / "upstream/vendor")
        # Preserve upstream platform rustflags, appending only Cargo's actual
        # source replacement configuration, with portable relative vendor paths.
        cfg = stage / "upstream/.cargo/config.toml"
        legacy = stage / "upstream/.cargo/config"
        if legacy.exists() and not cfg.exists():
            legacy.rename(cfg)
        cfg.parent.mkdir(exist_ok=True)
        text = cfg.read_text() if cfg.exists() else ""
        vendor_text = Path(vendor_config).read_text().replace(str(vendor), "vendor")
        cfg.write_text(text + "\n# Added by the distribution for offline source builds.\n" + vendor_text)
        # Validate source replacement with an empty Cargo cache. This proves
        # source resolution is offline; it is not a second compilation.
        with tempfile.TemporaryDirectory(prefix="cargo-offline-") as cargo_home:
            env = dict(os.environ, CARGO_HOME=cargo_home)
            subprocess.run(["cargo", "+" + PIN["rust_toolchain"], "metadata", "--locked", "--offline",
                            "--format-version", "1"], cwd=stage / "upstream", env=env,
                           stdout=subprocess.DEVNULL, check=True)
        write_json(stage / "source.json", {"distribution_version": version,
                   "distribution_commit": command("git", "rev-parse", "HEAD", cwd=ROOT), "upstream": PIN,
                   "cargo_lock_sha256": sha256(upstream / "Cargo.lock")})
        (stage / "BUILDING.txt").write_text(f"""Complete corresponding source for rustdesk-server-macos {version}

upstream/ contains RustDesk Server {PIN['version']} at {PIN['commit']},
all pinned recursive submodules, Cargo.lock, and every Cargo registry/Git dependency
vendored by cargo vendor --locked --versioned-dirs. Original notices are retained.
distribution/ contains the Go helper and exact release workflow/build scripts.

Build prerequisites: macOS 15+, Apple's Command Line Tools/Xcode SDK, Rust
{PIN['rust_toolchain']}, Go {PIN['go_toolchain']}, Python 3.12+ and Git. Toolchains
and Apple system libraries/SDK are build prerequisites, not part of this archive.

After installing those prerequisites, on the desired native architecture:
  cd upstream
  MACOSX_DEPLOYMENT_TARGET=15.0 cargo +{PIN['rust_toolchain']} build --release --locked --offline --bin hbbs --bin hbbr
  cd ../distribution
  CGO_ENABLED=0 GOTOOLCHAIN=local go build -trimpath -ldflags '-s -w -X main.version={version}' -o rustdesk-server ./cmd/rustdesk-server

MACOSX_DEPLOYMENT_TARGET=15.0 was used for official builds. The distribution
scripts document packaging, Mach-O checks, source export, and Packslip signing.
The source tree includes the upstream .env and sample db_v2.sqlite3 exactly as
tracked by upstream; neither is included in an installable binary archive.

RustDesk Server is licensed under AGPL-3.0. See upstream/LICENSE and all dependency
notices. The helper's license is distribution/LICENSE. This source archive is
provided alongside the binaries, not as a source-availability promise.
""")
        archive(stage, ROOT / f"dist/rustdesk-server-macos-{version}-source.tar.gz")


def manifest(version):
    validate_version(version)
    dist = ROOT / "dist"
    text = ['# Generated from verified final release assets; Packslip TOML input, not a signature.',
            'bin = ["bin/rustdesk-server", "bin/hbbs", "bin/hbbr"]', ""]
    for arch, canonical in (("arm64", "aarch64"), ("amd64", "x86_64")):
        stem = f"rustdesk-server-macos-{version}-darwin-{arch}"
        for suffix in (".tar.gz", ".cdx.json", ".source.json"):
            if not (dist / (stem + suffix)).is_file():
                raise ValueError("Missing final release asset: " + stem + suffix)
        text.extend(['[[artifact]]', f'path = "dist/{stem}.tar.gz"', 'os = "darwin"',
                     f'arch = "{canonical}"', 'format = "tar.gz"',
                     f'requires = {{ os_min = "{PIN["minimum_macos"]}" }}', "",
                     '[[resource]]', 'kind = "sbom"', 'format = "cyclonedx"',
                     f'artifact = "{stem}.tar.gz"', f'asset = "dist/{stem}.cdx.json"', ""])
    source = f"rustdesk-server-macos-{version}-source.tar.gz"
    # A namespaced extension signs the corresponding-source digest without
    # misrepresenting a source tarball as an installable platform artifact.
    text.extend(['[extensions."github.com/webkaz-labs/rustdesk-server-macos"]',
                 f'upstream_commit = "{PIN["commit"]}"', f'source_asset = "{source}"',
                 f'source_sha256 = "{sha256(dist / source)}"', ""])
    (dist / "packslip.toml").write_text("\n".join(text))


def checksums():
    dist = ROOT / "dist"
    files = sorted(p for p in dist.iterdir() if p.is_file() and p.name != "SHA256SUMS")
    (dist / "SHA256SUMS").write_text("".join(f"{sha256(p)}  {p.name}\n" for p in files))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["validate-version", "check-upstream", "metadata", "archive", "source", "manifest", "checksums"])
    parser.add_argument("args", nargs="*")
    args = parser.parse_args()
    handlers = {"validate-version": validate_version, "check-upstream": check_upstream,
                "metadata": metadata, "archive": archive, "source": source_bundle,
                "manifest": manifest, "checksums": checksums}
    handlers[args.command](*args.args)


if __name__ == "__main__":
    main()
