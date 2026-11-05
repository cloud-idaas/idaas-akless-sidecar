"""Feed actual handler output to the prepared, pinned SDK probes, without sockets."""
import argparse
import hashlib
import json
import os
import subprocess
import tempfile
from datetime import datetime, timezone
from pathlib import Path
from dependency_snapshot import dependency_snapshot

parser = argparse.ArgumentParser()
parser.add_argument('--sdk-dir', type=Path, default=Path(__file__).resolve().parent)
parser.add_argument('--output', type=Path, required=True)
args = parser.parse_args()
sdk = args.sdk_dir.resolve()
repo = Path(__file__).resolve().parents[2]
env = {k: v for k, v in os.environ.items()
       if not k.startswith(('ALIBABA_CLOUD_', 'AWS_', 'AZURE_', 'GOOGLE_', 'TENCENTCLOUD_', 'HUAWEICLOUD_'))
       and k.lower() not in ('http_proxy', 'https_proxy', 'all_proxy')}
results = []
installed_dependencies = dependency_snapshot(sdk, env)

def run(command, local_env):
    p = subprocess.run(command, cwd=repo, env=local_env, text=True, capture_output=True, timeout=120)
    if p.returncode:
        raise RuntimeError(f'{command[0]} exited {p.returncode}: {p.stderr[-500:]}')
    lines = [line for line in p.stdout.splitlines() if line.startswith('{')]
    return json.loads(lines[-1])

with tempfile.TemporaryDirectory(prefix='idaas-endpoint-fixtures-') as directory:
    root = Path(directory)
    build_env = env.copy()
    build_env['IDAAS_AKLESS_TEST_RESPONSE_DIR'] = str(root)
    subprocess.run(['go', 'test', './internal/credentials', '-count=1', '-run',
                    '^TestCredentialsHandlerDefaultAndRawContracts$'], cwd=repo, env=build_env, check=True)
    env.update(ALIBABA_CLOUD_CREDENTIALS_FILE=str(root / 'missing.ini'),
               ALIBABA_CLOUD_CONFIG_FILE=str(root / 'missing.json'),
               ALIBABA_CLOUD_CLI_PROFILE_DISABLED='true', ALIBABA_CLOUD_ECS_METADATA_DISABLED='true',
               AWS_EC2_METADATA_DISABLED='true', AWS_SHARED_CREDENTIALS_FILE=str(root / 'missing-aws'),
               AWS_CONFIG_FILE=str(root / 'missing-aws'))
    cp = str(sdk) + os.pathsep + (sdk / 'java-classpath.txt').read_text().strip()
    for vendor in ['alibaba_cloud', 'aws']:
        for language in (['Go', 'Python', 'Java', 'Node.js'] if vendor == 'alibaba_cloud' else ['Go', 'Python', 'Node.js']):
            for mode in (['explicit', 'default'] if vendor == 'alibaba_cloud' else ['container']):
                target = 'https://sdk-fixture.invalid/credentials' if language == 'Go' or vendor == 'aws' else 'http://sdk-fixture.invalid/credentials'
                local_env = dict(env, SDK_FIXTURE_FILE=str(root / (vendor + '.json')),
                                 ALIBABA_CLOUD_CREDENTIALS_URI=target)
                cloud = 'alibaba' if vendor == 'alibaba_cloud' else 'aws'
                if language == 'Go':
                    command = [str(sdk / 'go-probe'), cloud, mode, target]
                elif language == 'Python':
                    command = [str(sdk / 'venv/bin/python'), str(sdk / 'python_probe.py'), cloud, mode, target]
                elif language == 'Java':
                    command = ['java', '-cp', cp, 'JavaProbe', mode, target]
                else:
                    command = ['node', str(sdk / 'node/probe.cjs'), cloud, mode, target]
                result = run(command, local_env)
                assert result['complete'], (language, vendor, mode, result)
                results.append(dict(result, language=language, vendor=vendor, mode=mode))

    local_env = dict(env, SDK_FIXTURE_FILE=str(root / 'tencent_cloud.json'))
    command = [str(sdk / 'venv/bin/python'), '-c',
               "import os,json;from tencentcloud.common.credential import Credential;"
               "c=json.load(open(os.environ['SDK_FIXTURE_FILE']));"
               "v=Credential(c['tmpSecretId'],c['tmpSecretKey'],c['token']);"
               "assert v.get_credential_info()==('FAKE_ID','FAKE_SECRET','FAKE_TOKEN');"
               "print(json.dumps({'complete':True}))"]
    results.append(dict(run(command, local_env), language='Python', vendor='tencent_cloud', mode='mapped-DTO'))
    local_env['NODE_PATH'] = str(sdk / 'node/node_modules')
    command = ['node', '-e',
               "const fs=require('fs'),assert=require('assert');"
               "const {BasicCredential}=require('tencentcloud-sdk-nodejs-common/tencentcloud/common/credential');"
               "const c=JSON.parse(fs.readFileSync(process.env.SDK_FIXTURE_FILE));"
               "const v=new BasicCredential(c.tmpSecretId,c.tmpSecretKey,c.token);"
               "assert.deepStrictEqual([v.secretId,v.secretKey,v.token],['FAKE_ID','FAKE_SECRET','FAKE_TOKEN']);"
               "console.log(JSON.stringify({complete:true}));"]
    results.append(dict(run(command, local_env), language='Node.js', vendor='tencent_cloud', mode='mapped-DTO'))
    hashes = {p.stem: hashlib.sha256(p.read_bytes()).hexdigest() for p in root.glob('*.json')}

assert len(results) == 13
args.output.write_text(json.dumps({
    'verifiedAt': datetime.now(timezone.utc).isoformat(),
    'scope': 'Current handler responses with fake credentials. SDK transport/fetcher substitutes; Tencent field mapping. No network, refresh-cycle or cloud-permission verification.',
    'dependencyManifestSha256': {
        str(path.relative_to(sdk)): hashlib.sha256(path.read_bytes()).hexdigest()
        for path in (sdk / 'requirements.txt', sdk / 'requirements.lock', sdk / 'node/package.json', sdk / 'node/package-lock.json',
                     sdk / 'go/go.mod', sdk / 'go/go.sum', sdk / 'java/pom.xml')
    },
    'installedDependencies': installed_dependencies,
    'responseSha256': hashes, 'cases': results,
}, indent=2) + '\n')
print('13 actual-handler-to-SDK cases passed: Alibaba Cloud 8, AWS container 3, Tencent DTO mapping 2.')
