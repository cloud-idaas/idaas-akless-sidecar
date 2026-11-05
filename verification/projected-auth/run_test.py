import importlib.util
import subprocess
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('projected_runner', Path(__file__).with_name('run.py'))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)
REPO = Path(__file__).resolve().parents[2]


class ProjectedRunnerTest(unittest.TestCase):
    def fake(self, commands, failure=None, architecture='aarch64'):
        def run(command, **kwargs):
            commands.append((command, kwargs))
            if command[:3] == ['go', 'test', '-c']:
                Path(command[4]).write_bytes(b'fake test executable')
            if failure and failure in command:
                raise subprocess.CalledProcessError(1, command)
            return SimpleNamespace(stdout=architecture)
        return run

    def test_root_setup_and_nonroot_readonly_runtime(self):
        commands = []
        runner.run_checks(REPO, None, self.fake(commands))
        build = next((c, k) for c, k in commands if c[:3] == ['go', 'test', '-c'])
        self.assertEqual(build[1]['env']['GOARCH'], 'arm64')
        self.assertEqual(build[1]['env']['CGO_ENABLED'], '0')
        setup, runtime = [c for c, _ in commands if c[:2] == ['docker', 'run']]
        self.assertIn('--user=0:0', setup)
        self.assertIn('--cap-add=CHOWN', setup)
        self.assertIn('--user=10001:20001', runtime)
        self.assertNotIn('--cap-add=CHOWN', runtime)
        self.assertIn('--network=none', runtime)
        self.assertIn('AKLESS_PROJECTED_TEST_ROOT=/fixtures', runtime)
        self.assertTrue(any('target=/fixtures/ro,readonly' in arg for arg in runtime))
        self.assertTrue(any(arg.endswith('target=/fixtures/rw') for arg in runtime))
        expected_image = next(line.split('=', 1)[1] for line in (REPO / 'Dockerfile').read_text().splitlines()
                              if line.startswith('ARG RUNTIME_BASE_IMAGE='))
        self.assertIn(expected_image, runtime)
        self.assertEqual(commands[-1][0][:3], ['docker', 'volume', 'rm'])

    def test_failed_runtime_does_not_pass_and_cleans_volume(self):
        commands = []
        with self.assertRaises(subprocess.CalledProcessError):
            runner.run_checks(REPO, 'fixture-image', self.fake(commands, '-test.run=^TestProjectedMountRuntime$'))
        self.assertEqual(commands[-1][0][:3], ['docker', 'volume', 'rm'])

    def test_failed_setup_does_not_run_runtime_and_cleans_volume(self):
        commands = []
        with self.assertRaises(subprocess.CalledProcessError):
            runner.run_checks(REPO, 'fixture-image', self.fake(commands, '-test.run=^TestWriteProjectedMountFixtures$'))
        self.assertEqual(len([c for c, _ in commands if c[:2] == ['docker', 'run']]), 1)
        self.assertEqual(commands[-1][0][:3], ['docker', 'volume', 'rm'])

    def test_unsupported_engine_stops_before_build(self):
        commands = []
        with self.assertRaisesRegex(ValueError, 'amd64 or arm64'):
            runner.run_checks(REPO, 'fixture-image', self.fake(commands, architecture='unsupported'))
        self.assertEqual(len(commands), 1)

    def test_cli_passes_explicit_image_to_runner(self):
        with patch('sys.argv', ['run.py', '--image', 'local-fixture-image']), patch.object(runner, 'run_checks') as check:
            runner.main()
        check.assert_called_once_with(REPO, 'local-fixture-image')


if __name__ == '__main__':
    unittest.main()
