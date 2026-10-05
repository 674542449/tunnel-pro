"""Exercise final release rejection without network or live configuration changes."""
import hashlib
import importlib.util
import io
import json
import pathlib
import tempfile
import unittest
from unittest.mock import patch
from versioning import ROOT, TARGETS, check

spec = importlib.util.spec_from_file_location('release_gate', ROOT / 'tools/verify-release.py')
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)


class ReleaseGate(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = pathlib.Path(temp.name)
        for name in ['VERSION', *TARGETS]:
            path = self.root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes((ROOT / name).read_bytes())
        self.version = check(self.root)
        self.archive = b'accepted archive fixture'
        self.download = self.archive
        self.url = 'https://downloads.example.test/client.zip'
        self.health = dict(ready=True, version=self.version)
        self.release = dict(version=self.version, url=self.url, sha256=hashlib.sha256(self.archive).hexdigest())
        exe = self.root / ('dist/desktop-' + self.version + '/tunnelx-desktop.exe')
        exe.parent.mkdir(parents=True)
        exe.write_bytes(b'accepted executable fixture')
        (self.root / ('dist/tunnelX-desktop-windows-x64-' + self.version + '.zip')).write_bytes(self.archive)
        (self.root / 'docs').mkdir()
        for name, data in [
            ('admin-desktop-acceptance-', dict(passed=True, desktop_sha256=hashlib.sha256(exe.read_bytes()).hexdigest())),
            ('desktop-package-', dict(passed=True, desktop_version=self.version, archive_sha256=self.release['sha256'])),
        ]:
            (self.root / ('docs/' + name + self.version + '.json')).write_text(json.dumps(data))

    def run_gate(self):
        def get_json(url):
            return self.health if url.endswith('/health') else {'release': self.release}

        def urlopen(url, **kwargs):
            response = io.BytesIO(self.download)
            response.url = url
            return response

        with patch.object(gate, 'get_json', get_json), patch.object(gate.urllib.request, 'urlopen', urlopen):
            return gate.verify(['https://console.example.test/control'], self.root)

    def test_accept_matching_live_release(self):
        self.assertTrue(self.run_gate()['passed'])

    def test_reject_old_controller_or_download_version(self):
        for state in [self.health, self.release]:
            state['version'] = 'v0.0.1'
            with self.assertRaises(ValueError):
                self.run_gate()
            state['version'] = self.version

    def test_reject_replaced_download(self):
        self.download = b'another build with the same version name'
        with self.assertRaises(ValueError):
            self.run_gate()

    def test_reject_unaccepted_executable(self):
        (self.root / ('dist/desktop-' + self.version + '/tunnelx-desktop.exe')).write_bytes(b'rebuilt after acceptance')
        with self.assertRaises(ValueError):
            self.run_gate()


if __name__ == '__main__':
    unittest.main()
