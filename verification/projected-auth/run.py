"""Verify Linux projected-file authentication using disposable Docker mounts."""
import argparse
import os
import subprocess
import tempfile
import uuid
from pathlib import Path


def run_checks(repo, image, run=subprocess.run):
    architecture = run(['docker', 'info', '--format', '{{.Architecture}}'],
                       check=True, capture_output=True, text=True, timeout=30).stdout.strip()
    architecture = {'aarch64': 'arm64', 'x86_64': 'amd64'}.get(architecture, architecture)
    if architecture not in ('amd64', 'arm64'):
        raise ValueError('projected mount tests require an amd64 or arm64 Docker engine')
    if image is None:
        image = next(line.split('=', 1)[1] for line in (repo / 'Dockerfile').read_text().splitlines()
                     if line.startswith('ARG RUNTIME_BASE_IMAGE='))
    volume = 'idaas-projected-test-' + uuid.uuid4().hex
    with tempfile.TemporaryDirectory(prefix='idaas-projected-test-') as directory:
        binary = str(Path(directory) / 'projected-auth.test')
        env = dict(os.environ, GOOS='linux', GOARCH=architecture, CGO_ENABLED='0')
        run(['go', 'test', '-c', '-o', binary, './internal/projectedauth'],
            cwd=repo, env=env, check=True, timeout=300)
        common = ['docker', 'run', '--rm', '--network=none', '--read-only',
                  '--security-opt=no-new-privileges', '--cap-drop=ALL']
        volumes = []
        try:
            for name in (volume, volume + '-bin'):
                run(['docker', 'volume', 'create', name], check=True, timeout=30)
                volumes.append(name)
            # Stream the executable rather than bind-mounting a host temp path:
            # remote Docker engines and Colima do not necessarily share that path.
            run(common + ['--user=0:0', '--cap-add=CHOWN', '--cap-add=FOWNER',
                          '--mount', f'type=volume,source={volume}-bin,target=/work',
                          '--mount', f'type=volume,source={volume},target=/fixtures',
                          '-e', 'AKLESS_PROJECTED_FIXTURE_OUTPUT=/fixtures',
                          '-i', image, '/bin/sh', '-ec',
                          'cat > /work/projected-auth.test; chmod 0555 /work/projected-auth.test; '
                          'exec /work/projected-auth.test "$@"', 'fixture',
                          '-test.run=^TestWriteProjectedMountFixtures$', '-test.v'],
                input=Path(binary).read_bytes(), check=True, timeout=300)
            run(common + ['--user=10001:20001',
                          '--mount', f'type=volume,source={volume}-bin,target=/work,readonly',
                          '--mount', f'type=volume,source={volume},target=/fixtures/ro,readonly',
                          '--mount', f'type=volume,source={volume},target=/fixtures/rw',
                          '-e', 'AKLESS_PROJECTED_TEST_ROOT=/fixtures',
                          image, '/work/projected-auth.test',
                          '-test.run=^TestProjectedMountRuntime$', '-test.v'],
                check=True, timeout=120)
        finally:
            for name in reversed(volumes):
                run(['docker', 'volume', 'rm', name], check=True, timeout=30)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', help='runtime image; defaults to the digest-pinned Dockerfile base')
    args = parser.parse_args()
    run_checks(Path(__file__).resolve().parents[2], args.image)


if __name__ == '__main__':
    main()
