"""Exercise the release script without building or pushing customer images."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


DOCKER_STUB = r'''#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys

args = sys.argv[1:]
path = Path(os.environ["AKLESS_DOCKER_STATE"])
state = json.loads(path.read_text())
if args == ["buildx", "version"]:
    print("github.com/docker/buildx test")
elif args[:2] == ["buildx", "build"]:
    tag = args[args.index("-t") + 1]
    # docker-container's default output is cache-only. Existing local tags
    # remain untouched unless the build explicitly loads the result.
    if "--load" in args:
        state["images"][tag] = {"arch": "linux/amd64", "generation": "new"}
        state["images"][tag]["buildArgs"] = dict(
            args[index + 1].split("=", 1) for index, value in enumerate(args)
            if value == "--build-arg")
elif args[:2] == ["image", "inspect"]:
    image = state["images"].get(args[-1])
    if image is None:
        sys.exit(1)
    print(image["arch"])
elif args[:1] == ["tag"]:
    state["images"][args[2]] = state["images"][args[1]].copy()
elif args[:1] == ["push"]:
    state["pushed"][args[1]] = state["images"][args[1]].copy()
else:
    raise SystemExit("unexpected docker command: " + repr(args))
path.write_text(json.dumps(state))
'''


class BuildImagesTest(unittest.TestCase):
    def test_complete_runtime_and_toolchain_references_reach_the_correct_builds(self):
        script = Path(__file__).with_name('build-images.sh')
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            docker = root / 'docker'
            docker.write_text(DOCKER_STUB)
            docker.chmod(0o755)
            state_path = root / 'state.json'
            state_path.write_text(json.dumps({'images': {}, 'pushed': {}}))
            overrides = {'BUILD_BASE_IMAGE': 'registry.invalid/go@sha256:' + '1' * 64,
                         'RUNTIME_BASE_IMAGE': 'registry.invalid/alinux@sha256:' + '2' * 64,
                         'NODE_BASE_IMAGE': 'registry.invalid/node-glibc@sha256:' + '3' * 64}
            env = dict(os.environ, PATH=str(root) + os.pathsep + os.environ['PATH'],
                       ACR_REGISTRY='registry.invalid', ACR_NS='tests', IMG_ARCH='linux/amd64',
                       AKLESS_DOCKER_STATE=str(state_path), **overrides)
            env.pop('BASE_MIRROR', None)
            result = subprocess.run(['bash', str(script), '--no-push'], env=env,
                                    capture_output=True, text=True, timeout=30)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            images = json.loads(state_path.read_text())['images']
            sidecar = images['idaas-akless-sidecar:build-linux-amd64']['buildArgs']
            business = images['idaas-akless-business-example:build-linux-amd64']['buildArgs']
            self.assertEqual(sidecar['BUILD_BASE_IMAGE'], overrides['BUILD_BASE_IMAGE'])
            self.assertEqual(sidecar['RUNTIME_BASE_IMAGE'], overrides['RUNTIME_BASE_IMAGE'])
            self.assertEqual(business, {'BASE_IMAGE': overrides['RUNTIME_BASE_IMAGE'],
                                        'NODE_BASE_IMAGE': overrides['NODE_BASE_IMAGE']})

    def test_legacy_prefix_mirror_fails_before_docker(self):
        script = Path(__file__).with_name('build-images.sh')
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            marker = root / 'docker-called'
            docker = root / 'docker'
            docker.write_text('#!/bin/sh\ntouch "$AKLESS_DOCKER_MARKER"\nexit 99\n')
            docker.chmod(0o755)
            env = dict(os.environ, PATH=str(root) + os.pathsep + os.environ['PATH'],
                       BASE_MIRROR='registry.invalid/library', AKLESS_DOCKER_MARKER=str(marker))
            result = subprocess.run(['bash', str(script), '--no-push'], env=env,
                                    capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 2)
            self.assertIn('complete BUILD_BASE_IMAGE', result.stderr)
            self.assertFalse(marker.exists())

    def test_help_and_invalid_arguments_do_not_invoke_docker(self):
        script = Path(__file__).with_name('build-images.sh')
        for args, expected in [(['--help'], 0), (['-h'], 0),
                               (['--no-puhs'], 2), (['--unexpected'], 2),
                               (['--help', '--no-push'], 2),
                               (['--no-push', '--unexpected'], 2),
                               (['--no-push', '--no-push'], 2)]:
            with self.subTest(args=args), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                marker = root / 'docker-called'
                docker = root / 'docker'
                docker.write_text('#!/bin/sh\ntouch "$AKLESS_DOCKER_MARKER"\nexit 99\n')
                docker.chmod(0o755)
                env = dict(os.environ, PATH=str(root) + os.pathsep + os.environ['PATH'],
                           AKLESS_DOCKER_MARKER=str(marker))
                env.pop('ACR_REGISTRY', None)
                env.pop('ACR_NS', None)
                result = subprocess.run(['bash', str(script), *args], env=env,
                                        text=True, capture_output=True, timeout=10)
                self.assertEqual(result.returncode, expected, result.stdout + result.stderr)
                self.assertIn('Usage:', result.stdout + result.stderr)
                self.assertFalse(marker.exists())

    def test_no_push_builds_and_verifies_without_publishing(self):
        script = Path(__file__).with_name('build-images.sh')
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            # macOS ships Bash 3.2: nounset rejects expansion of an empty array.
            # Exercise the system interpreter for the generated scripts as well.
            if Path('/bin/bash').exists():
                (root / 'bash').symlink_to('/bin/bash')
            docker = root / 'docker'
            docker.write_text(DOCKER_STUB)
            docker.chmod(0o755)
            state_path = root / 'state.json'
            state_path.write_text(json.dumps({'images': {}, 'pushed': {}}))
            env = dict(os.environ, PATH=str(root) + os.pathsep + os.environ['PATH'],
                       ACR_REGISTRY='registry.invalid', ACR_NS='tests', IMG_ARCH='linux/amd64',
                       AKLESS_DOCKER_STATE=str(state_path))
            for name in ('BASE_MIRROR', 'BUILD_BASE_IMAGE', 'RUNTIME_BASE_IMAGE', 'NODE_BASE_IMAGE'):
                env.pop(name, None)
            result = subprocess.run(['bash', str(script), '--no-push'], env=env,
                                    text=True, capture_output=True, timeout=30)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            state = json.loads(state_path.read_text())
            self.assertEqual(len(state['images']), 2)
            self.assertEqual(state['pushed'], {})
            self.assertEqual(state['images']['idaas-akless-business-example:build-linux-amd64']['buildArgs'], {})
            self.assertEqual(set(state['images']['idaas-akless-sidecar:build-linux-amd64']['buildArgs']),
                             {'VERSION', 'REVISION'})

    def test_container_driver_publishes_current_build_with_or_without_old_tags(self):
        script = Path(__file__).with_name("build-images.sh")
        for old_tags in (False, True):
            with self.subTest(old_tags=old_tags), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                docker = root / "docker"
                docker.write_text(DOCKER_STUB)
                docker.chmod(0o755)
                state_path = root / "state.json"
                images = {}
                if old_tags:
                    for name in ("idaas-akless-sidecar", "idaas-akless-business-example"):
                        images[name + ":build-linux-amd64"] = {
                            "arch": "linux/amd64", "generation": "old"
                        }
                state_path.write_text(json.dumps({"images": images, "pushed": {}}))
                env = dict(os.environ, PATH=str(root) + os.pathsep + os.environ["PATH"],
                           ACR_REGISTRY="registry.invalid", ACR_NS="tests",
                           IMG_ARCH="linux/amd64", IMG_TAG="regression", REVISION="fixture-revision",
                           AKLESS_DOCKER_STATE=str(state_path))
                result = subprocess.run(["bash", str(script)], env=env,
                                        text=True, capture_output=True, timeout=30)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                pushed = json.loads(state_path.read_text())["pushed"]
                self.assertEqual(len(pushed), 2)
                self.assertEqual({image["generation"] for image in pushed.values()}, {"new"},
                                 "the release script published a stale local tag")
                sidecar = next(image for name, image in pushed.items() if '/idaas-akless-sidecar:' in name)
                self.assertEqual(sidecar['buildArgs']['VERSION'], 'regression')
                self.assertEqual(sidecar['buildArgs']['REVISION'], 'fixture-revision')


if __name__ == "__main__":
    unittest.main()
