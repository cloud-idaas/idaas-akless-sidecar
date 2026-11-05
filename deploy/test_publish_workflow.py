"""Run the actual publishing verification step against a classic-store Docker fake."""
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest


WORKFLOW = Path(__file__).resolve().parents[1] / '.github/workflows/publish-images.yml'
WORKFLOW_EXECUTED_LINES = set()
INDEXES = ['sha256:' + '1' * 64, 'sha256:' + '2' * 64]
CHILDREN = {
    ('idaas-akless-sidecar', 'linux/amd64'): 'sha256:' + '3' * 64,
    ('idaas-akless-sidecar', 'linux/arm64'): 'sha256:' + '4' * 64,
    ('idaas-akless-business-example', 'linux/amd64'): 'sha256:' + '5' * 64,
    ('idaas-akless-business-example', 'linux/arm64'): 'sha256:' + '6' * 64,
}


def shell_step(name):
    lines = WORKFLOW.read_text().splitlines()
    start = lines.index('      - name: ' + name)
    run = lines.index('        run: |', start)
    body = []
    for line in lines[run + 1:]:
        if line.strip() and not line.startswith('          '):
            break
        body.append(line[10:])
    return '\n'.join(body) + '\n', run + 2


def verification_step():
    return shell_step('Verify remote indexes and both platform runtimes')


DOCKER = r'''#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys

path = Path(os.environ['AKLESS_VERIFY_STATE'])
state = json.loads(path.read_text())
args = sys.argv[1:]
state['calls'].append(args)
mode = state['mode']
indexes = ['sha256:' + '1' * 64, 'sha256:' + '2' * 64]
names = ['idaas-akless-sidecar', 'idaas-akless-business-example']
children = {(names[0], 'linux/amd64'): 'sha256:' + '3' * 64,
            (names[0], 'linux/arm64'): 'sha256:' + '4' * 64,
            (names[1], 'linux/amd64'): 'sha256:' + '5' * 64,
            (names[1], 'linux/arm64'): 'sha256:' + '6' * 64}
path.write_text(json.dumps(state))
if args[:3] == ['buildx', 'imagetools', 'inspect']:
    image, template = args[3], args[args.index('--format') + 1]
    name = next(name for name in names if '/' + name + '@' in image or '/' + name + ':' in image)
    index = indexes[names.index(name)]
    if template == '{{.Manifest.Digest}}':
        print('sha256:' + '9' * 64 if mode == 'changed-tag' else index)
    elif '.Platform.Architecture "' in template:
        platform = 'linux/amd64' if '"amd64"' in template else 'linux/arm64'
        digest = children[name, platform]
        if mode == 'missing-child' and platform == 'linux/arm64':
            digest = ''
        elif mode == 'duplicate-child':
            digest += '\n' + digest
        elif mode == 'malformed-child':
            digest = 'not-a-digest'
        print(digest)
    else:
        print('linux amd64\nlinux arm64')
        if mode == 'extra-platform':
            print('linux s390x')
elif args[:1] == ['run']:
    platform = args[args.index('--platform') + 1]
    image = next(arg for arg in args if arg.startswith('registry.invalid/fixture/'))
    name = next(name for name in names if '/' + name + '@' in image)
    digest = image.split('@')[1]
    if digest in indexes:
        # Classic Docker stores one image ID per index reference. A second
        # platform conflicts even though its layers download successfully.
        previous = state['local'].get(image)
        if previous is not None and previous != platform:
            print('docker: cannot overwrite digest ' + digest, file=sys.stderr)
            sys.exit(125)
        state['local'][image] = platform
        path.write_text(json.dumps(state))
    elif digest != children[name, platform]:
        raise SystemExit('wrong child digest for requested platform')
    if mode == 'runtime-failure':
        raise SystemExit(42)
    if '--version' in args:
        print('wrong version' if mode == 'wrong-version' else
              'idaas-akless-sidecar version=v0.1.0-rc.1 revision=' + os.environ['RELEASE_REVISION'])
    elif name == names[1]:
        arch = 'x64' if platform == 'linux/amd64' else 'arm64'
        assert 'EXPECTED_ARCH=' + arch in args
        print('PASS ' + arch + ' business HTTP health')
else:
    raise SystemExit('unexpected Docker command')
'''


class PublishWorkflowTest(unittest.TestCase):
    def run_verification(self, mode='ok', existing=False, digest=None, release_tag='v0.1.0-rc.1'):
        trace_blocks = {}

        def traced_step(name):
            body, first_line = shell_step(name)
            token = str(len(trace_blocks))
            trace_blocks[token] = first_line
            return ': WORKFLOW_TRACE_START_' + token + '\n' + body + ': WORKFLOW_TRACE_END_' + token + '\n'

        with tempfile.TemporaryDirectory(prefix='idaas-publish-test-') as directory:
            root = Path(directory)
            docker = root / 'docker'
            docker.write_text(DOCKER)
            docker.chmod(0o755)
            git = root / 'git'
            git.write_text('''#!/usr/bin/env python3
import os,sys
if sys.argv[1:] == ['rev-parse', 'HEAD']:
    print('a' * 40)
elif sys.argv[1:] == ['-C', 'release-source', 'rev-parse', 'HEAD']:
    if os.environ['AKLESS_VERIFY_MODE'] == 'missing-source':
        raise SystemExit(128)
    print('b' * 40)
else:
    raise SystemExit('unexpected Git command')
''')
            git.chmod(0o755)
            state = root / 'docker-state.json'
            state.write_text(json.dumps({'mode': mode, 'calls': [], 'local': {}}))
            for name, built_digest in zip(['sidecar', 'business'], INDEXES):
                (root / (name + '-build.json')).write_text(json.dumps({'containerimage.digest': built_digest}))
            summary = root / 'summary.md'
            env = {key: value for key, value in os.environ.items()
                   if not key.startswith(('ACR_', 'RELEASE_', 'GITHUB_'))}
            env.update(PATH=str(root) + os.pathsep + os.environ['PATH'],
                       ACR_REGISTRY='registry.invalid', ACR_NS='fixture',
                       RELEASE_VERSION='v0.1.0-rc.1', RELEASE_REVISION='fixture-revision',
                       RUNNER_TEMP=str(root), GITHUB_STEP_SUMMARY=str(summary),
                       AKLESS_VERIFY_STATE=str(state), AKLESS_VERIFY_MODE=mode,
                       VERIFY_EXISTING='true' if existing else 'false',
                       GITHUB_SHA='a' * 40, GITHUB_ENV=str(root / 'github-env'),
                       ACR_USERNAME='fixture-user', ACR_PASSWORD='fixture-password',
                       EXISTING_SIDECAR_DIGEST=INDEXES[0] if digest is None else digest,
                       EXISTING_BUSINESS_DIGEST=INDEXES[1])
            preparation = traced_step('Validate release configuration')
            if existing:
                env['RELEASE_VERSION'] = release_tag
                # The real workflow validates first, resolves the original tag
                # checkout and carries GITHUB_ENV into the shared verifier.
                preparation += traced_step('Prepare verification of existing images')
                preparation += 'set -a\nsource "$GITHUB_ENV"\nset +a\n'
            # Trace only fictional fixture values to measure the actual shell
            # commands exercised; no cloud credential or Docker call is used.
            traced = "PS4='+TRACE:${LINENO}:'\nset -x\n" + preparation
            traced += traced_step('Verify remote indexes and both platform runtimes')
            result = subprocess.run(['bash', '-c', traced], env=env,
                                    capture_output=True, text=True, timeout=30)
            self.assertIn('WORKFLOW_TRACE_START_', result.stderr)
            active = None
            for number, command in re.findall(r'^\++TRACE:(\d+):(.*)$', result.stderr, re.MULTILINE):
                if command.startswith(': WORKFLOW_TRACE_START_'):
                    token = command.removeprefix(': WORKFLOW_TRACE_START_')
                    active = (int(number), trace_blocks[token])
                elif command.startswith(': WORKFLOW_TRACE_END_'):
                    active = None
                elif active is not None:
                    WORKFLOW_EXECUTED_LINES.add(active[1] + int(number) - active[0] - 1)
            return result, json.loads(state.read_text())['calls'], summary.read_text() if summary.exists() else ''

    def test_each_platform_runs_its_verified_child_manifest(self):
        result, calls, summary = self.run_verification()
        self.assertEqual(result.returncode, 0, result.stderr)
        runs = [call for call in calls if call[0] == 'run']
        self.assertEqual(len(runs), 6)
        for call in runs:
            platform = call[call.index('--platform') + 1]
            image = next(arg for arg in call if arg.startswith('registry.invalid/fixture/'))
            name = image.split('/')[-1].split('@')[0]
            self.assertEqual(image.split('@')[1], CHILDREN[name, platform])
        for digest in INDEXES:
            self.assertIn('@' + digest, summary)
        self.assertIn('PASS linux/arm64', result.stdout)

    def test_invalid_or_ambiguous_child_is_rejected(self):
        for mode in ['missing-child', 'duplicate-child', 'malformed-child']:
            with self.subTest(mode=mode):
                result, calls, summary = self.run_verification(mode)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('Expected one valid child manifest', result.stderr)
                self.assertFalse(any(c[0] == 'run' and 'linux/arm64' in c for c in calls))
                self.assertEqual(summary, '')

    def test_changed_remote_tag_is_rejected_before_runtime_checks(self):
        result, calls, _ = self.run_verification('changed-tag')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Remote tag no longer matches', result.stderr)
        self.assertFalse(any(c[0] == 'run' for c in calls))

    def test_unexpected_index_platform_is_rejected(self):
        result, calls, _ = self.run_verification('extra-platform')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Unexpected platform index', result.stderr)
        self.assertFalse(any(c[0] == 'run' for c in calls))

    def test_wrong_version_cannot_pass_smoke_checks(self):
        result, _, summary = self.run_verification('wrong-version')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Sidecar version mismatch', result.stderr)
        self.assertEqual(summary, '')

    def test_runtime_failure_is_propagated(self):
        result, _, summary = self.run_verification('runtime-failure')
        self.assertEqual(result.returncode, 42)
        self.assertEqual(summary, '')

    def test_existing_images_use_original_revision_without_build_or_push(self):
        result, calls, summary = self.run_verification(existing=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('Source commit: ' + 'b' * 40, summary)
        self.assertNotIn('Source commit: ' + 'a' * 40, summary)
        self.assertIn('Verification only:', summary)
        self.assertEqual(len([call for call in calls if call[0] == 'run']), 6)
        self.assertFalse(any('push' in call or 'build' in call for call in calls))
        # Check the declarative workflow guard as well as the shell behavior.
        workflow = WORKFLOW.read_text()
        build = workflow.split('      - name: Build and push both multi-platform images\n', 1)[1].split('      - name:', 1)[0]
        self.assertIn("        if: env.VERIFY_EXISTING != 'true'", build)

    def test_existing_images_require_original_valid_index_digests(self):
        for digest in ['', 'sha256:123', '$(touch should-not-execute)']:
            with self.subTest(digest=digest):
                result, calls, summary = self.run_verification(existing=True, digest=digest)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('original build', result.stdout + result.stderr)
                self.assertEqual(calls, [])
                self.assertEqual(summary, '')

    def test_existing_version_rejects_invalid_ref_before_any_docker_access(self):
        result, calls, _ = self.run_verification(existing=True, release_tag='main; echo invalid')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('version tag', result.stdout + result.stderr)
        self.assertEqual(calls, [])

    def test_missing_original_source_cannot_use_main_revision(self):
        result, calls, summary = self.run_verification('missing-source', existing=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(calls, [])
        self.assertEqual(summary, '')


if __name__ == '__main__':
    unittest.main()
