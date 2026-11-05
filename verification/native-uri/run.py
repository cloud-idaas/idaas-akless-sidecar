#!/usr/bin/env python3
"""Run real native SDK tests in isolated Pods copied from an existing Deployment."""
import argparse
import copy
import json
import re
import subprocess
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--context', required=True)
    parser.add_argument('--namespace', default='idaas-akless')
    parser.add_argument('--deployment', default='akless-demo')
    parser.add_argument('--image', required=True, help='SDK test image pinned with @sha256:...')
    parser.add_argument('--expected-role-arn', required=True)
    parser.add_argument('--wait-for-refresh', action='store_true')
    args = parser.parse_args()
    if not re.fullmatch(r'[^@\s]+@sha256:[0-9a-f]{64}', args.image):
        parser.error('--image must be pinned by a sha256 digest with 64 lowercase hexadecimal characters')
    kubectl = ['kubectl', '--context', args.context, '-n', args.namespace]

    def call(command, **kwargs):
        return subprocess.run(kubectl + command, check=True, timeout=40, **kwargs)

    deployment = json.loads(call(['get', 'deployment', args.deployment, '-o', 'json'],
                                 capture_output=True, text=True).stdout)
    source_spec = deployment['spec']['template']['spec']
    if source_spec.get('hostNetwork', False):
        raise ValueError('hostNetwork is not allowed for this test')
    source_sidecar = next(c for c in source_spec['initContainers'] if c['name'] == 'idaas-akless-sidecar')
    binary = '/usr/local/bin/idaas-akless-sidecar'
    if (source_sidecar.get('command') or []) not in ([], [binary]):
        raise ValueError('unsupported Sidecar command: use the image entrypoint or direct '
                         + binary + ' only; put startup options in args, not in a wrapper or command')
    # Normalize only these options; retain the source config path and other flags.
    sidecar_parser = argparse.ArgumentParser(add_help=False, allow_abbrev=False)
    sidecar_parser.add_argument('--listen-port', '-listen-port')
    sidecar_parser.add_argument('--credential-auth-enabled', '-credential-auth-enabled')
    source_options, remaining_args = sidecar_parser.parse_known_args(source_sidecar.get('args', []))
    port = source_options.listen_port
    if port is None:
        port_env = next((e for e in reversed(source_sidecar.get('env', []))
                         if e['name'] == 'IDAAS_AKLESS_SIDECAR_LISTEN_PORT'), {})
        if 'valueFrom' in port_env:
            ref = port_env['valueFrom'].get('configMapKeyRef', {})
            if not ref.get('name') or not ref.get('key'):
                raise ValueError('source port requires a literal value or configMapKeyRef')
            command = ['get', 'configmap', ref['name'], '-o', 'json']
            if ref.get('optional'):
                command.append('--ignore-not-found')
            configmap = json.loads(call(command, capture_output=True, text=True).stdout or '{}')
            port = configmap.get('data', {}).get(ref['key'])
            if port is None and not ref.get('optional'):
                raise ValueError('required source port ConfigMap key is missing')
            port = port or '61765'
        else:
            port = port_env.get('value') or '61765'
    if not isinstance(port, str) or not port.isascii() or not port.isdecimal() or not 1 <= int(port) <= 65535:
        raise ValueError('source Sidecar port must be an integer from 1 to 65535')
    port = str(int(port))
    created = []
    try:
        for auth in ['enabled', 'disabled']:
            name = 'akless-native-uri-' + auth + '-' + uuid.uuid4().hex[:8]
            spec = copy.deepcopy(deployment['spec']['template']['spec'])
            # Cover two 7300s executions plus startup (190s) and a 210s margin.
            spec.update(restartPolicy='Never', activeDeadlineSeconds=15000 if args.wait_for_refresh else 900)
            sidecar = next(c for c in spec['initContainers'] if c['name'] == 'idaas-akless-sidecar')
            # Execute the binary directly so no image entrypoint can swallow test flags.
            sidecar['command'] = [binary]
            auth_value = 'true' if auth == 'enabled' else 'false'
            sidecar['args'] = ['--credential-auth-enabled=' + auth_value,
                               '--listen-port=' + port, *remaining_args]
            sidecar['env'] = [e for e in sidecar.get('env', []) if e['name'] not in
                              ('IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED', 'IDAAS_AKLESS_SIDECAR_LISTEN_PORT')]
            sidecar['env'].extend([
                {'name': 'IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED', 'value': auth_value},
                {'name': 'IDAAS_AKLESS_SIDECAR_LISTEN_PORT', 'value': port},
            ])
            # env is replaced below, but envFrom and other mounts are inherited.
            # Use a trusted source without alternate credentials or bootstrap mounts.
            business = next(c for c in spec['containers'] if c['name'] == 'business')
            spec['containers'] = [business]
            business.update(image=args.image, command=['node', '-e', 'setInterval(()=>{},60000)'], args=[], env=[
                {'name': 'ALIBABA_CLOUD_CREDENTIALS_URI', 'value': 'http://127.0.0.1:' + port + '/credentials'},
                {'name': 'ALIBABA_CLOUD_ECS_METADATA_DISABLED', 'value': 'true'},
                {'name': 'ALIBABA_CLOUD_CLI_PROFILE_DISABLED', 'value': 'true'},
                {'name': 'ALIBABA_CLOUD_CREDENTIALS_FILE', 'value': '/nonexistent/credentials'},
                {'name': 'SDK_AUTH_MODE', 'value': auth},
                {'name': 'EXPECTED_ROLE_ARN', 'value': args.expected_role_arn},
                {'name': 'SDK_WAIT_FOR_REFRESH', 'value': str(args.wait_for_refresh).lower()},
            ])
            for key in ['livenessProbe', 'readinessProbe', 'startupProbe', 'ports', 'lifecycle']:
                business.pop(key, None)
            business['securityContext']['readOnlyRootFilesystem'] = True
            if auth == 'disabled':
                spec['volumes'] = [v for v in spec['volumes'] if v['name'] != 'credential-auth-token']
                for c in [sidecar, business]:
                    c['volumeMounts'] = [m for m in c.get('volumeMounts', []) if m['name'] != 'credential-auth-token']
            pod = {'apiVersion': 'v1', 'kind': 'Pod', 'metadata': {'name': name,
                   'labels': {'app': name, 'test-purpose': 'native-credentials-uri'}}, 'spec': spec}
            created.append(name)
            call(['apply', '--validate=strict', '-f', '-'], input=json.dumps(pod), text=True)
            subprocess.run(kubectl + ['wait', '--for=condition=Ready', 'pod/' + name, '--timeout=180s'],
                           check=True, timeout=190)
            for mode in ['explicit', 'default']:
                subprocess.run(kubectl + ['exec', name, '-c', 'business', '--', 'env',
                               'SDK_URI_MODE=' + mode, 'node', '/app/integration.cjs'], check=True,
                               timeout=7300 if args.wait_for_refresh else 120)
        print('RESULT all four native SDK mode/authentication combinations passed', flush=True)
    finally:
        if created:
            subprocess.run(kubectl + ['delete', 'pod', *created, '--ignore-not-found', '--timeout=60s'],
                           check=True, timeout=70)


if __name__ == '__main__':
    main()
