"""Exercise the actual backup runner with isolated files and a controlled exporter."""
import importlib.util
import json
import pathlib
import subprocess
import tempfile
import unittest
from unittest.mock import patch

root = pathlib.Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('backup_control', root / 'deploy/backup-control.py')
backup = importlib.util.module_from_spec(spec)
spec.loader.exec_module(backup)


class BackupRunnerTests(unittest.TestCase):
    def test_success_failure_recovery_and_retention(self):
        with tempfile.TemporaryDirectory() as folder:
            base = pathlib.Path(folder)
            config = base / 'config.json'
            config.write_text(json.dumps({'data_file': 'state.json'}))
            directory, key = base / 'backups', base / 'backup.key'
            def export(args, **kwargs):
                pathlib.Path(args[args.index('-backup') + 1]).write_bytes(b'encrypted-test-output')
            with patch.object(backup.subprocess, 'run', side_effect=export):
                first = backup.run_backup(config, key, directory, pathlib.Path('fixture-exporter'))
            status = json.loads((base / 'backup-status.json').read_text())
            self.assertEqual(status['bytes'], 21)
            self.assertEqual(len(key.read_bytes()), 32)
            original_key = key.read_bytes()
            failure = subprocess.CalledProcessError(1, ['fixture'], stderr='private-database-password')
            with patch.object(backup.subprocess, 'run', side_effect=failure):
                with self.assertRaisesRegex(RuntimeError, 'Encrypted control backup failed') as raised:
                    backup.run_backup(config, key, directory, pathlib.Path('fixture-exporter'))
            failed = json.loads((base / 'backup-status.json').read_text())
            self.assertEqual(failed['time'], status['time'])
            self.assertGreater(failed['failed_at'], 0)
            self.assertNotIn('private-database-password', str(raised.exception))
            self.assertNotIn('private-database-password', json.dumps(failed))
            for number in range(337):
                (directory / ('old-%03d.txbk' % number)).write_bytes(b'old')
            with patch.object(backup.subprocess, 'run', side_effect=export):
                second = backup.run_backup(config, key, directory, pathlib.Path('fixture-exporter'))
            self.assertNotIn('failed_at', json.loads((base / 'backup-status.json').read_text()))
            self.assertEqual(len(list(directory.glob('*.txbk'))), 336)
            self.assertEqual(key.read_bytes(), original_key)
            self.assertNotEqual(first, second)
            self.assertTrue((directory / second).is_file())


if __name__ == '__main__':
    unittest.main()
