"""Record installed SDK versions without registry credentials or personal paths."""
import hashlib
import json
import os
import subprocess
from pathlib import Path


def dependency_snapshot(sdk, env=None, run=subprocess.run):
    def output(command):
        return run(command, env=env, check=True, text=True, capture_output=True, timeout=120).stdout

    node_tree = json.loads(output(['npm', 'ls', '--all', '--json', '--prefix', str(sdk / 'node')]))
    node_versions = {}

    def visit(tree):
        for name, package in tree.get('dependencies', {}).items():
            # npm represents absent optional dependencies as empty objects. A
            # missing required dependency already makes npm ls fail above.
            if 'version' in package:
                node_versions.setdefault(name, set()).add(package['version'])
            visit(package)

    visit(node_tree)
    python_packages = json.loads(output([str(sdk / 'venv/bin/python'), '-m', 'pip', 'list', '--format=json']))
    go_modules = []
    for line in output(['go', 'version', '-m', str(sdk / 'go-probe')]).splitlines():
        fields = line.strip().split('\t')
        if fields[0] == 'dep':
            go_modules.append({'module': fields[1], 'version': fields[2], 'sum': fields[3]})
    java_artifacts = []
    for entry in (sdk / 'java-classpath.txt').read_text().strip().split(os.pathsep):
        path = Path(entry)
        java_artifacts.append({'file': path.name, 'sha256': hashlib.sha256(path.read_bytes()).hexdigest()})
    return {
        'node': [{'name': name, 'versions': sorted(versions)} for name, versions in sorted(node_versions.items())],
        'python': sorted(python_packages, key=lambda package: package['name'].lower()),
        'go': sorted(go_modules, key=lambda module: module['module']),
        'java': sorted(java_artifacts, key=lambda artifact: artifact['file']),
    }
