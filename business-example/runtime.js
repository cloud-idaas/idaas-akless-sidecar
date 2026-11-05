'use strict';

// 仅检查 HTTP 进程可响应；真实凭据链路由部署验收单独验证。
function handleRequest(req, res) {
  const path = req.url.split('?')[0];
  if (path === '/healthz') {
    res.end('ok');
    return;
  }
  if (path === '/') {
    res.end('IDaaS Akless business example (Node.js) running\n');
    return;
  }
  res.statusCode = 404;
  res.end('not found');
}

module.exports = { handleRequest };
