'use strict';
const http = require('node:http');
const { configuredRoleArn, configuredCredentialsURI, parseTimeoutMs, fetchJson } = require('./credentials-client');
const { handleRequest } = require('./runtime');

// 同一 Pod 的容器共享网络，业务通过 loopback 访问 Sidecar。
async function fetchCredential() {
  const url = new URL(configuredCredentialsURI());
  const roleArn = configuredRoleArn(process.env);
  if (roleArn) url.searchParams.set('roleArn', roleArn);
  const timeoutMs = parseTimeoutMs(process.env.IDAAS_AKLESS_CREDENTIALS_TIMEOUT_MS);
  return fetchJson(url, timeoutMs);
}

async function main() {
  // 阿里云响应字段；实际业务可将这三个凭据字段传给云 SDK。
  const { AccessKeyId, AccessKeySecret, SecurityToken, Expiration } = await fetchCredential();
  if (!AccessKeyId || !AccessKeySecret || !SecurityToken || !Expiration) {
    throw new Error('missing Alibaba Cloud STS credential fields');
  }
  // 不输出 AK、SK 或 Token。
  console.log('[ok] got STS credentials from sidecar, expires at %s', Expiration);
}

// 仅用于保持演示容器运行，并满足部署清单中的存活探针。
http.createServer(handleRequest).listen(process.env.PORT || 3000, '0.0.0.0');

// 启动时演示一次；真实业务应在需要凭据时调用，并处理失败及过期刷新。
main().catch(error => console.error('[err]', error.message));
