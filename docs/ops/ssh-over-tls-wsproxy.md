# SSH over TLS(443) 生产部署指南（外挂 WebSocket 代理）

本文档描述如何在**不改动 ClawSSH 网关逻辑**的前提下，通过 `clawssh-wsproxy` 提供
`SSH over TLS(443)` 能力，并给出单机与双机房上线建议。

## 1. 架构说明

外部客户端（WSS）-> TLS 终结（可在 wsproxy 或 Nginx/Caddy）-> `clawssh-wsproxy` -> `127.0.0.1:22`

说明：
- `clawssh-wsproxy` 只做字节流转发，不解析 SSH 协议。
- 原 SSH 网关（或系统 sshd）保持原有行为不变。
- 推荐将目标端口限制为回环地址，仅本机可访问。

## 2. 二进制部署（systemd）

已提供服务文件模板：`deploy/systemd/clawssh-wsproxy.service`。

### 2.1 准备

1. 构建并安装：

```bash
go build -o /usr/local/bin/clawssh-wsproxy ./cmd/clawssh-wsproxy
```

2. 创建运行用户和目录（示例）：

```bash
sudo useradd --system --no-create-home --shell /usr/sbin/nologin clawssh || true
sudo mkdir -p /etc/clawssh
sudo chown root:root /etc/clawssh
sudo chmod 0755 /etc/clawssh
```

3. 写入环境文件 `/etc/clawssh/wsproxy.env`：

```bash
CLAWSSH_WSPROXY_ADDR=:443
CLAWSSH_WSPROXY_PATH=/ws
CLAWSSH_WSPROXY_TARGET_ADDR=127.0.0.1:22
CLAWSSH_WSPROXY_TLS_CERT_FILE=/etc/letsencrypt/live/ssh.example.com/fullchain.pem
CLAWSSH_WSPROXY_TLS_KEY_FILE=/etc/letsencrypt/live/ssh.example.com/privkey.pem

# 生产建议
CLAWSSH_WSPROXY_MAX_CONNS=20000
CLAWSSH_WSPROXY_ALLOW_ORIGINS=*
CLAWSSH_WSPROXY_ALLOW_NO_ORIGIN=true
```

4. 安装并启动：

```bash
sudo cp deploy/systemd/clawssh-wsproxy.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now clawssh-wsproxy
sudo systemctl status clawssh-wsproxy
```

## 3. 使用 Nginx 前置 TLS（回源到 wsproxy 的 TLS 8443）

配置模板：`deploy/nginx/clawssh-wsproxy.conf`

适用场景：
- 证书、TLS 策略统一在 Nginx 管理；
- `clawssh-wsproxy` 监听 `127.0.0.1:8443`（TLS）；
- 外网只暴露 Nginx 443。

关键点：
- 必须透传 `Upgrade` / `Connection` 头；
- `proxy_read_timeout` 需足够大（长连接场景）；
- Nginx 到 wsproxy 使用 `https://` 回源，且启用 `proxy_ssl_server_name on;`；
- 建议开启 `least_conn` 或 `ip_hash`（按连接模型调优）。

## 4. 使用 Caddy 前置 TLS（回源到 wsproxy 的 TLS 8443）

配置模板：`deploy/caddy/Caddyfile`

适用场景：
- 希望自动签发和续期证书；
- 配置简单，快速上线。

同样建议让 `clawssh-wsproxy` 仅绑定本地地址（如 `127.0.0.1:8443`）。

## 5. 双机房上线建议（A/B 两地）

### 5.1 网络与流量

- 两地各自部署：
  - `clawssh`（或目标 SSH 服务）
  - `clawssh-wsproxy`
  -（可选）Nginx/Caddy 作为 TLS 入口
- 通过 GSLB/Anycast/DNS 权重做跨机房分流。
- 建议优先“同城就近 + 故障切流”。

### 5.2 配置一致性

- 使用同一套环境变量模板，仅替换站点特定值（证书路径、节点标签）。
- 建议把 `/etc/clawssh/wsproxy.env` 纳入配置管理（Ansible/Salt/自研 CMDB）。
- 保证两地 `CLAWSSH_WSPROXY_PATH` 一致，避免客户端分支逻辑。

### 5.3 健康检查与摘流

- L4/L7 健康检查：`GET /healthz` 返回 `200 ok`。
- 摘流流程：
  1) 负载均衡摘除节点；
  2) 等待连接自然回收（或观察活跃连接归零）；
  3) 再执行重启/发布。

### 5.4 容量与参数建议

建议起始值（按压测再调）：
- `CLAWSSH_WSPROXY_MAX_CONNS=20000`
- `CLAWSSH_WSPROXY_PING_INTERVAL_SECONDS=25`
- `CLAWSSH_WSPROXY_WS_IDLE_TIMEOUT_SECONDS=90`
- `CLAWSSH_WSPROXY_WRITE_TIMEOUT_SECONDS=15`

系统参数（Linux）建议：
- `ulimit -n` 提升到至少 `200000`
- `net.core.somaxconn=65535`
- `net.ipv4.ip_local_port_range` 扩容
- `net.ipv4.tcp_tw_reuse=1`（按内核与合规要求评估）

## 6. 安全建议

- 若无浏览器场景，可将 `CLAWSSH_WSPROXY_ALLOW_ORIGINS=*` 且 `ALLOW_NO_ORIGIN=true`。
- 若有 Web 控制台来源，建议显式配置 `ALLOW_ORIGINS` 白名单。
- 证书私钥权限建议 `0600`，仅 root 可读。
- `CLAWSSH_WSPROXY_TARGET_ADDR` 优先 `127.0.0.1:22`，避免横向暴露。

## 7. 验证清单

1. 本地健康检查：

```bash
curl -k https://127.0.0.1/healthz
```

2. 端口监听：

```bash
ss -lntp | rg ":443|:22|:8443"
```

3. 服务日志：

```bash
journalctl -u clawssh-wsproxy -f
```

4. 灰度验证：
- 小比例节点先发布；
- 观察连接数、错误率、重连率，再全量。
