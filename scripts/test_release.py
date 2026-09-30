import importlib.util
import json
import os
from pathlib import Path
import tarfile
import tempfile
import tomllib
import unittest
import subprocess
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("release", Path(__file__).with_name("release.py"))
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


class PackagingTests(unittest.TestCase):
    def test_upstream_exact_pins(self):
        self.assertEqual(release.PIN["commit"], "73523b31cfd25d77dee862e6fc9f5e1fb5e485ef")
        self.assertEqual(release.PIN["submodules"], {"libs/hbb_common": "83419b6549636ee39dacef7776c473f5802e08d6"})

    def test_versions_cannot_inject_paths_or_shell(self):
        for version in ["0.1.0", "1.1.16", "0.0.0-dev.123", "1.2.3-rc.1"]:
            release.validate_version(version)
        for version in ["../oops", "1.2.3\n", "v1.2.3", "01.2.3", "$(whoami)", "1.2", "1.2.3/evil"]:
            with self.assertRaises(ValueError):
                release.validate_version(version)

    def test_archive_reproducible_and_executable(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source = root / "source"
            (source / "bin").mkdir(parents=True)
            binary = source / "bin/tool"
            binary.write_bytes(b"fixture")
            binary.chmod(0o755)
            (source / "NOTICE").write_text("notice")
            release.archive(source, root / "one.tar.gz")
            release.archive(source, root / "two.tar.gz")
            self.assertEqual((root / "one.tar.gz").read_bytes(), (root / "two.tar.gz").read_bytes())
            with tarfile.open(root / "one.tar.gz") as archive:
                self.assertEqual(archive.getmember("bin/tool").mode, 0o755)
                self.assertEqual(archive.getmember("NOTICE").mode, 0o644)
                self.assertEqual(archive.getmember("bin/tool").uid, 0)

    def test_manifest_matches_actual_asset_layout(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(release, "ROOT", Path(directory)):
            dist = Path(directory) / "dist"
            dist.mkdir()
            for arch in ("arm64", "amd64"):
                for suffix in (".tar.gz", ".cdx.json", ".source.json"):
                    (dist / f"rustdesk-server-macos-0.1.0-darwin-{arch}{suffix}").write_bytes(b"fixture")
            (dist / "rustdesk-server-macos-0.1.0-source.tar.gz").write_bytes(b"source")
            release.manifest("0.1.0")
            parsed = tomllib.loads((dist / "packslip.toml").read_text())
            self.assertEqual(parsed["bin"], ["bin/rustdesk-server", "bin/hbbs", "bin/hbbr"])
            self.assertEqual([a["arch"] for a in parsed["artifact"]], ["aarch64", "x86_64"])
            self.assertEqual([a["requires"]["os_min"] for a in parsed["artifact"]], ["15.0", "15.0"])
            self.assertEqual(len(parsed["resource"]), 2)
            for resource, artifact in zip(parsed["resource"], parsed["artifact"]):
                self.assertEqual(resource["kind"], "sbom")
                self.assertEqual(resource["artifact"], Path(artifact["path"]).name)
            release.checksums()
            before = (dist / "SHA256SUMS").read_text()
            release.checksums()
            self.assertEqual(before, (dist / "SHA256SUMS").read_text())
            self.assertNotIn("  SHA256SUMS", before)

    def test_missing_release_assets_fail_closed(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(release, "ROOT", Path(directory)):
            (Path(directory) / "dist").mkdir()
            with self.assertRaises(ValueError):
                release.manifest("0.1.0")

    def test_license_inventory_preserves_notices(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            crate = root / "crate"
            crate.mkdir()
            (crate / "LICENSE").write_text("a license")
            (crate / "NOTICE").write_text("a notice")
            package = {"name": "example", "version": "1.0", "id": "example 1.0", "license": "MIT/Apache-2.0", "manifest_path": str(crate / "Cargo.toml")}
            release.collect_licenses([package], root / "notices")
            index = json.loads((root / "notices/index.json").read_text())
            self.assertEqual(index[0]["license"], "MIT/Apache-2.0")
            self.assertEqual(len(index[0]["files"]), 2)
            for file in index[0]["files"]:
                self.assertTrue((root / "notices" / file).is_file())

    def test_git_export_contains_committed_source_only(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            repo = root / "repo"
            repo.mkdir()
            subprocess.run(["git", "init", "-q", str(repo)], check=True)
            (repo / "source.txt").write_text("committed source")
            subprocess.run(["git", "add", "source.txt"], cwd=repo, check=True)
            subprocess.run(["git", "-c", "user.name=Packaging Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "fixture"], cwd=repo, check=True)
            (repo / "private-runtime-key").write_text("never export")
            release.export_git(repo, root / "export")
            self.assertEqual((root / "export/source.txt").read_text(), "committed source")
            self.assertFalse((root / "export/private-runtime-key").exists())
            self.assertFalse((root / "export/.git").exists())

    def test_sbom_target_filter_and_binary_hashes(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            upstream = root / "upstream"
            upstream.mkdir()
            (upstream / "LICENSE").write_text("upstream license")
            (upstream / "Cargo.lock").write_text("locked")
            (root / "LICENSE").write_text("distribution license")
            stage = root / "stage"
            (stage / "bin").mkdir(parents=True)
            for name in release.NAMES:
                (stage / "bin" / name).write_bytes(name.encode())
            package = {"id": "hbbs-id", "name": "hbbs", "version": "1.1.16", "source": None,
                       "manifest_path": str(upstream / "Cargo.toml"), "license": None}
            inactive = dict(package, id="windows-only", name="windows-only")
            raw = {"packages": [package, inactive], "resolve": {"root": "hbbs-id", "nodes": [{"id": "hbbs-id", "deps": []}]}}
            metadata = root / "cargo.json"
            metadata.write_text(json.dumps(raw))
            with patch.object(release, "ROOT", root), patch.object(release, "check_upstream"), patch.object(release, "source_metadata", return_value={"build": {"go": "go1.26.8"}}):
                release.metadata(upstream, metadata, stage, "0.1.0", "arm64")
            bom = json.loads((stage / "share/rustdesk-server/bom.cdx.json").read_text())
            self.assertEqual(bom["bomFormat"], "CycloneDX")
            self.assertNotIn("windows-only", {p["name"] for p in bom["components"]})
            helper = next(p for p in bom["components"] if p.get("bom-ref") == "binary:rustdesk-server")
            self.assertEqual(helper["hashes"][0]["content"], release.sha256(stage / "bin/rustdesk-server"))


if __name__ == "__main__":
    unittest.main()
