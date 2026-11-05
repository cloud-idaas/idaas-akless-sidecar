# Test-only image; build context is verification/native-uri/.
ARG NODE_BASE_IMAGE=node:24.22.2-bookworm-slim@sha256:c86ecead9e55a5210ac1727de38a4ca840bf03dcaf34dc9749addbf383b1920c
ARG BASE_IMAGE=alibaba-cloud-linux-3-registry.cn-hangzhou.cr.aliyuncs.com/alinux3/alinux3:3.13.4@sha256:fd25a109fcbd1ff33692ac32e1890c986fe7b4b656ab54a27a69aba04b8360c7
FROM ${NODE_BASE_IMAGE} AS node-runtime
FROM ${BASE_IMAGE}
# Public HTTPS sources work outside ECS; these RPMs need no module/plus/EPEL repos.
RUN sed -i 's|http://mirrors.cloud.aliyuncs.com/|https://mirrors.aliyun.com/|g; s|http://mirrors.aliyun.com/|https://mirrors.aliyun.com/|g' /etc/yum.repos.d/*.repo && \
    yum --disablerepo='*' --enablerepo=alinux3-os --enablerepo=alinux3-updates install -y ca-certificates libstdc++ libatomic shadow-utils && \
    yum clean all && rm -rf /var/cache/yum /var/cache/dnf && \
    groupadd --gid 1000 node && \
    useradd --no-log-init --uid 1000 --gid 1000 --create-home --shell /bin/sh node
COPY --from=node-runtime /usr/local/bin/node /usr/local/bin/node
COPY --from=node-runtime /usr/local/lib/node_modules/npm /usr/local/lib/node_modules/npm
COPY --from=node-runtime /usr/local/LICENSE /usr/share/licenses/nodejs/LICENSE
RUN ln -s /usr/local/lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm && \
    ln -s /usr/local/lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx && \
    node --version && npm --version
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts --no-audit --no-fund
COPY integration.cjs ./
COPY LICENSE /usr/share/licenses/idaas-akless-sidecar/LICENSE
USER 1000:1000
CMD ["node", "-e", "setInterval(()=>{},60000)"]
