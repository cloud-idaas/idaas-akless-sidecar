"""Verify the aggregate gate propagates coverage failures without cloud access."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


class CheckGateTest(unittest.TestCase):
    def test_aggregate_check_executes_coverage_gate_and_propagates_failure(self):
        repo = Path(__file__).resolve().parents[1]
        make = shutil.which('make')
        self.assertIsNotNone(make, 'make is required by the project check entry point')
        for fail in (False, True):
            with self.subTest(fail=fail), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                (root / 'Makefile').write_text((repo / 'Makefile').read_text())
                log = root / 'commands.log'
                stub = '''#!/bin/sh
printf '%s\\n' "$0 $*" >> "$AKLESS_CHECK_LOG"
if [ "${AKLESS_FAIL_GATE:-0}" = 1 ] && [ "${1:-}" = test ] && \\
   [ "${2:-}" = --prefix ] && [ "${3:-}" = business-example/multicloud ]; then
  exit 42
fi
'''
                for name in ('npm', 'node', 'go', 'python3'):
                    command = root / name
                    command.write_text(stub)
                    command.chmod(0o755)
                env = dict(os.environ, PATH=str(root) + os.pathsep + os.environ['PATH'],
                           AKLESS_CHECK_LOG=str(log), AKLESS_FAIL_GATE=str(int(fail)))
                env.pop('MAKEFLAGS', None)
                env.pop('MFLAGS', None)
                result = subprocess.run([make, 'check'], cwd=root, env=env,
                                        capture_output=True, text=True, timeout=30)
                self.assertEqual(result.returncode, 2 if fail else 0,
                                 result.stdout + result.stderr)
                self.assertIn('npm test --prefix business-example/multicloud', log.read_text())

    def test_isolated_image_context_license_copies_match_project_license(self):
        repo = Path(__file__).resolve().parents[1]
        expected = (repo / 'LICENSE').read_bytes()
        for context in ('business-example', 'verification/native-uri'):
            with self.subTest(context=context):
                self.assertEqual((repo / context / 'LICENSE').read_bytes(), expected,
                                 'sync the isolated build-context copy when LICENSE changes')


if __name__ == '__main__':
    unittest.main()
