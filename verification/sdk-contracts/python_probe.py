import json, sys
vendor, mode, target = sys.argv[1:4]
result = {"resolved": False, "complete": False}
import os
from types import SimpleNamespace
payload = open(os.environ['SDK_FIXTURE_FILE'],'rb').read()
if vendor == 'alibaba':
    from Tea.core import TeaCore
    TeaCore.do_action = staticmethod(lambda *a, **k: SimpleNamespace(status_code=200, body=payload))
if vendor == 'aws' and mode == 'container':
    from botocore.utils import ContainerMetadataFetcher
    class FixtureFetcher(ContainerMetadataFetcher):
        def retrieve_full_uri(self, *a, **k): return json.loads(payload)

try:
    if vendor == 'alibaba':
        from alibabacloud_credentials.client import Client
        from alibabacloud_credentials.models import Config
        c = Client(Config(type='credentials_uri', credentials_uri=target)) if mode == 'explicit' else Client()
        cred = c.get_credential()
        values = (cred.access_key_id, cred.access_key_secret, cred.security_token)
    elif vendor == 'aws':
        from botocore.credentials import ContainerProvider
        provider = ContainerProvider(environ={'AWS_CONTAINER_CREDENTIALS_FULL_URI': target}, fetcher=FixtureFetcher())
        cred = provider.load().get_frozen_credentials()
        values = (cred.access_key, cred.secret_key, cred.token)
    result['resolved'] = True
    result['complete'] = values == ('FAKE_ID', 'FAKE_SECRET', 'FAKE_TOKEN')
except Exception as exc:
    result['errorClass'] = type(exc).__name__
print(json.dumps(result))
