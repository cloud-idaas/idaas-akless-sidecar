'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { handleRequest } = require('./runtime');

function request(url) {
  const response = { statusCode: 200, end(body) { this.body = body; } };
  handleRequest({ url }, response);
  return response;
}

test('health and home routes respond independently of credentials; unknown routes return 404', () => {
  assert.equal(request('/healthz?check=1').body, 'ok');
  assert.equal(request('/healthz').statusCode, 200);
  assert.match(request('/').body, /running/);
  assert.equal(request('/unknown').statusCode, 404);
  assert.equal(request('/unknown').body, 'not found');
});
