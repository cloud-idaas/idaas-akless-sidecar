import hashlib
import importlib.util
import json
import subprocess
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace

spec = importlib.util.spec_from_file_location('dependency_snapshot', Path(__file__).with_name('dependency_snapshot.py'))
snapshot = importlib.util.module_from_spec(spec)
spec.loader.exec_module(snapshot)


class DependencySnapshotTest(unittest.TestCase):
    def test_installed_tree_versions_and_artifact_hashes_are_recorded_without_paths(self):
        with tempfile.TemporaryDirectory() as directory:
            sdk = Path(directory)
            artifact = sdk / 'sdk-1.2.3.jar'
            artifact.write_bytes(b'fixture jar')
            (sdk / 'java-classpath.txt').write_text(str(artifact))
            node = {'dependencies': {
                'a': {'version': '1.0.0', 'resolved': 'https://user:secret@registry.invalid/a',
                      'dependencies': {'b': {'version': '2.0.0'}}},
                'b': {'version': '1.0.0'},
                'uninstalled-optional': {},
            }}

            def run(command, **kwargs):
                self.assertTrue(kwargs['check'])
                if command[0] == 'npm':
                    return SimpleNamespace(stdout=json.dumps(node))
                if command[0] == 'go':
                    return SimpleNamespace(stdout='/private/build/path\n\tdep\tmodule\tv1.0.0\th1:fixture\n')
                return SimpleNamespace(stdout='[{"name":"z","version":"2"},{"name":"a","version":"1"}]')

            result = snapshot.dependency_snapshot(sdk, run=run)
            self.assertEqual(result['node'], [{'name': 'a', 'versions': ['1.0.0']},
                                              {'name': 'b', 'versions': ['1.0.0', '2.0.0']}])
            self.assertEqual(result['python'][0]['name'], 'a')
            self.assertEqual(result['go'], [{'module': 'module', 'version': 'v1.0.0', 'sum': 'h1:fixture'}])
            self.assertEqual(result['java'], [{'file': artifact.name, 'sha256': hashlib.sha256(b'fixture jar').hexdigest()}])
            self.assertNotIn(directory, json.dumps(result))
            self.assertNotIn('secret', json.dumps(result))

    def test_failed_dependency_inspection_is_not_reported_as_success(self):
        def fail(command, **kwargs):
            raise subprocess.CalledProcessError(1, command)
        with self.assertRaises(subprocess.CalledProcessError):
            snapshot.dependency_snapshot(Path('/unused'), run=fail)


if __name__ == '__main__':
    unittest.main()
