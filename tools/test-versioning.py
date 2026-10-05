import pathlib
import tempfile
import unittest
from versioning import ROOT, TARGETS, check, sync


class VersionContract(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = pathlib.Path(self.tmp.name)
        for name in ['VERSION', *TARGETS]:
            target = self.root / name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes((ROOT / name).read_bytes())

    def test_current_sources_agree(self):
        check(self.root)

    def test_reject_each_drift_and_missing_label(self):
        for name, rules in TARGETS.items():
            file = self.root / name
            original = file.read_text(encoding='utf-8')
            for pattern, count, prefixed in rules:
                import re
                m = re.search(pattern, original)
                for replacement in ['v9.9.9' if prefixed else '9.9.9', 'missing']:
                    file.write_text(original[:m.start(1)] + replacement + original[m.end(1):], encoding='utf-8')
                    with self.subTest(file=name, replacement=replacement), self.assertRaises(ValueError):
                        check(self.root)
                file.write_text(original, encoding='utf-8')

    def test_sync_new_release_preserves_core(self):
        model = self.root / 'internal/control/model.go'
        self.assertIn('const Version = "v0.4.0"', model.read_text(encoding='utf-8'))
        (self.root / 'VERSION').write_text('v9.8.7\n')
        self.assertEqual(sync(self.root), 'v9.8.7')
        self.assertIn('const Version = "v0.4.0"', model.read_text(encoding='utf-8'))
        with self.assertRaises(ValueError):
            check(self.root, 'v9.8.6')


if __name__ == '__main__':
    unittest.main()
