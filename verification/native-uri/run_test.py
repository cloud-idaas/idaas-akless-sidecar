import importlib.util
import json
from pathlib import Path
import subprocess
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('native_uri_run', Path(__file__).with_name('run.py'))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)


class RunnerTest(unittest.TestCase):
    def setUp(self):
        self.pod_spec = {
            'containers': [{'name': 'business', 'securityContext': {'runAsNonRoot': True},
                            'livenessProbe': {}, 'ports': [],
                            'volumeMounts': [{'name': 'credential-auth-token'}]}],
            'initContainers': [{'name': 'idaas-akless-sidecar',
                                'env': [{'name': 'IDAAS_AKLESS_CREDENTIAL_AUTH_ENABLED', 'valueFrom': {}}],
                                'volumeMounts': [{'name': 'credential-auth-token'}, {'name': 'oidc-federated-token'}]}],
            'volumes': [{'name': 'credential-auth-token'}, {'name': 'oidc-federated-token'}]}
        self.deployment = {'spec': {'template': {'spec': self.pod_spec}}}
        self.configmap = {'data': {'IDAAS_AKLESS_SIDECAR_LISTEN_PORT': '61765'}}
        self.commands = []
        self.exec_timeouts = []
        self.pods = []
        self.fail_exec = False
        self.image = 'test@sha256:' + 'a' * 64
        self.argv = ['run.py', '--context', 'test-context', '--image', self.image,
                     '--expected-role-arn', 'acs:ram::123:role/test']

    def execute(self, command, **kwargs):
        self.commands.append(command)
        if 'exec' in command:
            self.exec_timeouts.append(kwargs['timeout'])
        if 'get' in command and 'configmap' in command:
            return subprocess.CompletedProcess(command, 0,
                                               stdout=json.dumps(self.configmap) if self.configmap is not None else '')
        if 'get' in command:
            return subprocess.CompletedProcess(command, 0, stdout=json.dumps(self.deployment))
        if 'apply' in command:
            self.pods.append(json.loads(kwargs['input']))
        if 'exec' in command and self.fail_exec:
            raise subprocess.CalledProcessError(1, command)
        return subprocess.CompletedProcess(command, 0)

    def run_main(self):
        with patch('sys.argv', self.argv), patch.object(runner.subprocess, 'run', side_effect=self.execute):
            runner.main()

    def test_all_four_combinations_and_isolation(self):
        self.run_main()
        self.assertEqual(len(self.pods), 2)
        self.assertEqual(sum('exec' in c for c in self.commands), 4)
        self.assertTrue(all(c[1:3] == ['--context', 'test-context'] for c in self.commands))
        enabled, disabled = [p['spec'] for p in self.pods]
        self.assertEqual(enabled['restartPolicy'], 'Never')
        self.assertEqual(enabled['activeDeadlineSeconds'], 900)
        self.assertEqual(len(enabled['volumes']), 2)
        self.assertEqual(disabled['volumes'], [{'name': 'oidc-federated-token'}])
        self.assertEqual(disabled['containers'][0]['volumeMounts'], [])
        self.assertEqual(len(disabled['initContainers'][0]['volumeMounts']), 1)
        self.assertNotIn('livenessProbe', enabled['containers'][0])
        self.assertTrue(enabled['containers'][0]['securityContext']['readOnlyRootFilesystem'])
        self.assertNotIn('valueFrom', disabled['initContainers'][0]['env'][0])
        self.assertIn('delete', self.commands[-1])
        self.assertNotIn('restartPolicy', self.pod_spec)

    def test_long_refresh_mode(self):
        self.argv.append('--wait-for-refresh')
        self.run_main()
        deadline = self.pods[1]['spec']['activeDeadlineSeconds']
        exec_timeouts = self.exec_timeouts[-2:]
        self.assertGreaterEqual(deadline, 190 + sum(exec_timeouts))
        values = {e['name']: e['value'] for e in self.pods[1]['spec']['containers'][0]['env']}
        self.assertEqual(values['SDK_WAIT_FOR_REFRESH'], 'true')

    def test_authentication_cli_overrides_are_replaced_for_each_scenario(self):
        for flags in [
            ['--credential-auth-enabled=true'],
            ['--credential-auth-enabled', 'false'],
            ['-credential-auth-enabled=true', '--credential-auth-enabled=false'],
        ]:
            with self.subTest(flags=flags):
                self.setUp()
                sidecar = self.pod_spec['initContainers'][0]
                sidecar['args'] = ['--config', '/custom/config.json', *flags]
                self.run_main()
                for pod, expected in zip(self.pods, ['true', 'false']):
                    args = pod['spec']['initContainers'][0]['args']
                    self.assertEqual(args, ['--credential-auth-enabled=' + expected,
                                            '--listen-port=61765', '--config', '/custom/config.json'])
                self.assertEqual(sidecar['args'], ['--config', '/custom/config.json', *flags])

    def test_supported_commands_use_the_direct_binary_in_both_scenarios(self):
        binary = '/usr/local/bin/idaas-akless-sidecar'
        for command in [None, [], [binary]]:
            with self.subTest(command=command):
                self.setUp()
                source = self.pod_spec['initContainers'][0]
                if command is not None:
                    source['command'] = command.copy()
                source['args'] = ['--config=/custom.json', '--credential-auth-enabled=true']
                self.run_main()
                for pod, expected in zip(self.pods, ['true', 'false']):
                    sidecar = pod['spec']['initContainers'][0]
                    self.assertEqual(sidecar['command'], [binary])
                    self.assertEqual(sidecar['args'], ['--credential-auth-enabled=' + expected,
                                                       '--listen-port=61765', '--config=/custom.json'])
                self.assertEqual(source.get('command'), command)

    def test_wrappers_and_command_embedded_options_are_rejected_before_creation(self):
        binary = '/usr/local/bin/idaas-akless-sidecar'
        for command in [
            ['/bin/sh', '-c', 'exec ' + binary + ' --credential-auth-enabled=true'],
            ['sh', '-c', 'exec ' + binary + ' "$@"'],
            ['/usr/bin/env', binary],
            ['/custom/sidecar-wrapper'],
            [binary, '--credential-auth-enabled=false'],
        ]:
            with self.subTest(command=command):
                self.setUp()
                self.pod_spec['initContainers'][0]['command'] = command.copy()
                with self.assertRaisesRegex(ValueError, 'Sidecar command'):
                    self.run_main()
                self.assertEqual(self.pods, [])
                self.assertEqual(len(self.commands), 1)  # Source read only; no apply or delete.
                self.assertEqual(self.pod_spec['initContainers'][0]['command'], command)

    def test_invalid_or_unresolved_port_fails_before_creating_pods(self):
        for flags, env in [
            ([], [{'name': 'IDAAS_AKLESS_SIDECAR_LISTEN_PORT', 'value': 'invalid'}]),
            (['--listen-port=0'], []),
            (['-listen-port', '65536'], []),
            (['--listen-port=-1'], []),
            (['--listen-port=１２３４'], []),
            ([], [{'name': 'IDAAS_AKLESS_SIDECAR_LISTEN_PORT', 'valueFrom': {}}]),
        ]:
            with self.subTest(flags=flags, env=env):
                self.setUp()
                sidecar = self.pod_spec['initContainers'][0]
                sidecar['args'] = flags
                sidecar['env'].extend(env)
                with self.assertRaisesRegex(ValueError, 'port'):
                    self.run_main()
                self.assertEqual(self.pods, [])
                self.assertEqual(len(self.commands), 1)  # Only read the source Deployment.

    def test_configmap_port_is_shared_by_sidecar_healthcheck_and_sdk(self):
        sidecar = self.pod_spec['initContainers'][0]
        sidecar['env'].append({'name': 'IDAAS_AKLESS_SIDECAR_LISTEN_PORT', 'valueFrom': {
            'configMapKeyRef': {'name': 'settings', 'key': 'IDAAS_AKLESS_SIDECAR_LISTEN_PORT'}}})
        self.configmap['data']['IDAAS_AKLESS_SIDECAR_LISTEN_PORT'] = '61865'
        self.run_main()
        self.assertIn('configmap', self.commands[1])
        for pod in self.pods:
            source = pod['spec']['initContainers'][0]
            self.assertIn('--listen-port=61865', source['args'])
            env = {e['name']: e.get('value') for e in source['env']}
            self.assertEqual(env['IDAAS_AKLESS_SIDECAR_LISTEN_PORT'], '61865')
            business_env = {e['name']: e['value'] for e in pod['spec']['containers'][0]['env']}
            self.assertEqual(business_env['ALIBABA_CLOUD_CREDENTIALS_URI'],
                             'http://127.0.0.1:61865/credentials')

    def test_missing_or_invalid_configmap_port_fails_before_creating_pods(self):
        for value in [None, '0', '65536', 'invalid']:
            with self.subTest(value=value):
                self.setUp()
                self.pod_spec['initContainers'][0]['env'].append({
                    'name': 'IDAAS_AKLESS_SIDECAR_LISTEN_PORT', 'valueFrom': {
                        'configMapKeyRef': {'name': 'settings', 'key': 'port'}}})
                self.configmap = {'data': {'port': value}} if value is not None else {}
                with self.assertRaisesRegex(ValueError, 'port'):
                    self.run_main()
                self.assertEqual(self.pods, [])

    def test_optional_missing_or_empty_configmap_port_uses_the_default(self):
        for configmap in [None, {}, {'data': {'port': ''}}]:
            with self.subTest(configmap=configmap):
                self.setUp()
                self.configmap = configmap
                self.pod_spec['initContainers'][0]['env'].append({
                    'name': 'IDAAS_AKLESS_SIDECAR_LISTEN_PORT', 'valueFrom': {
                        'configMapKeyRef': {'name': 'settings', 'key': 'port', 'optional': True}}})
                self.run_main()
                self.assertIn('--ignore-not-found', self.commands[1])
                for pod in self.pods:
                    self.assertIn('--listen-port=61765', pod['spec']['initContainers'][0]['args'])
                    env = {e['name']: e['value'] for e in pod['spec']['containers'][0]['env']}
                    self.assertEqual(env['ALIBABA_CLOUD_CREDENTIALS_URI'],
                                     'http://127.0.0.1:61765/credentials')

    def test_literal_custom_and_legacy_ports_are_preserved(self):
        for port in ['61865', '8080']:
            with self.subTest(port=port):
                self.setUp()
                self.pod_spec['initContainers'][0]['env'].append({
                    'name': 'IDAAS_AKLESS_SIDECAR_LISTEN_PORT', 'value': port})
                self.run_main()
                for pod in self.pods:
                    self.assertIn('--listen-port=' + port, pod['spec']['initContainers'][0]['args'])
                    env = {e['name']: e['value'] for e in pod['spec']['containers'][0]['env']}
                    self.assertEqual(env['ALIBABA_CLOUD_CREDENTIALS_URI'],
                                     'http://127.0.0.1:' + port + '/credentials')

    def test_cli_port_takes_precedence_and_other_configuration_is_preserved(self):
        sidecar = self.pod_spec['initContainers'][0]
        sidecar['args'] = ['--listen-port', '61765', '--config=/custom.json']
        sidecar['env'].append({'name': 'IDAAS_AKLESS_SIDECAR_LISTEN_PORT', 'value': '8081'})
        self.run_main()
        for pod in self.pods:
            sidecar = pod['spec']['initContainers'][0]
            self.assertIn('--config=/custom.json', sidecar['args'])
            env = {e['name']: e.get('value') for e in sidecar['env']}
            self.assertEqual(env['IDAAS_AKLESS_SIDECAR_LISTEN_PORT'], '61765')

    def test_two_ninety_minute_refresh_scenarios_fit_within_the_pod_deadline(self):
        self.argv.append('--wait-for-refresh')
        original_execute = self.execute
        elapsed = 190

        def execute(command, **kwargs):
            nonlocal elapsed
            result = original_execute(command, **kwargs)
            if 'exec' in command and self.pods[-1]['metadata']['name'].startswith('akless-native-uri-disabled'):
                elapsed += 5340
                self.assertLess(elapsed, self.pods[-1]['spec']['activeDeadlineSeconds'])
                self.assertEqual(kwargs['timeout'], 7300)
            return result

        with patch('sys.argv', self.argv), patch.object(runner.subprocess, 'run', side_effect=execute):
            runner.main()
        self.assertEqual(elapsed, 10870)

    def test_failed_exec_still_cleans_up(self):
        self.fail_exec = True
        with self.assertRaises(subprocess.CalledProcessError):
            self.run_main()
        self.assertIn('delete', self.commands[-1])
        self.assertEqual(len(self.pods), 1)

    def test_host_network_rejected_before_creation(self):
        self.pod_spec['hostNetwork'] = True
        with self.assertRaisesRegex(ValueError, 'hostNetwork'):
            self.run_main()
        self.assertEqual(self.pods, [])
        self.assertFalse(any('delete' in c for c in self.commands))

    def test_unpinned_image_rejected(self):
        self.argv[self.argv.index(self.image)] = 'test:latest'
        with self.assertRaises(SystemExit):
            self.run_main()
        self.assertEqual(self.commands, [])

    def test_malformed_image_digest_is_rejected_before_any_cluster_access(self):
        for image in [
            'test@sha256:123', 'test@sha256:' + 'g' * 64,
            'test@sha256:' + 'a' * 63, 'test@sha256:' + 'a' * 65,
            'test@sha256:' + 'a' * 64 + ':latest', '@sha256:' + 'a' * 64,
            'test name@sha256:' + 'a' * 64,
        ]:
            with self.subTest(image=image):
                self.setUp()
                self.argv[self.argv.index(self.image)] = image
                with self.assertRaises(SystemExit) as error:
                    self.run_main()
                self.assertEqual(error.exception.code, 2)
                self.assertEqual(self.commands, [])

    def test_digest_pinned_tag_and_registry_port_are_supported(self):
        self.argv[self.argv.index(self.image)] = 'registry.example.com:5000/ns/test:v1.2.3@sha256:' + 'b' * 64
        self.run_main()
        self.assertEqual(len(self.pods), 2)
        for pod in self.pods:
            self.assertEqual(pod['spec']['containers'][0]['image'], self.argv[4])


if __name__ == '__main__':
    unittest.main()
