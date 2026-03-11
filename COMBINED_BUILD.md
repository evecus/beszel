# Beszel — 单一可执行文件构建说明

本分支将 hub 和 agent 合并为**一个可执行文件** `beszel`，通过命令行参数选择运行模式。

## 运行模式

```
./beszel              # 自监控模式：hub + 本地 agent 同时运行
./beszel -hub         # Hub 模式：只运行 web 管理界面（管理远程被监控服务器）
./beszel -agent       # Agent 模式：只运行 agent（被监控的服务器）
```

---

## 编译方法

### 前置要求

- Go 1.22+（`go version`）
- Node.js / npm 或 bun（用于编译前端 UI）

### 编译步骤

```bash
# 克隆或解压源码
cd beszel-main

# 编译单一二进制文件（包含完整 Web UI）
make build-combined

# 输出文件：./build/beszel（Linux/macOS）或 ./build/beszel.exe（Windows）
```

如果不需要编译前端（例如只测试功能），可以跳过：

```bash
make build-combined SKIP_WEB=true
# 注意：跳过 web UI 编译后，hub 界面无法访问，仅用于测试
```

开发模式（不内嵌 UI，UI 由 vite dev server 提供）：

```bash
make build-combined-dev
```

---

## 使用说明

### 模式一：自监控（`./beszel`）

适用于只有一台服务器，想监控自身的场景。

```bash
./beszel serve --http 0.0.0.0:8090
```

启动后会输出：
- 本机 agent 运行在 unix socket 上
- 用于在 hub UI 中添加本机的**公钥**
- 私钥保存路径（`beszel_data/self_agent.key`）

然后打开浏览器访问 `http://服务器IP:8090`，在 hub UI 中：
1. 添加系统 → Host 填 `localhost`，Port 留空（走 unix socket）
2. 公钥粘贴启动时打印的内容

---

### 模式二：Hub 模式（`./beszel -hub`）

用于**中心服务器**，负责展示和管理所有被监控的服务器。

```bash
./beszel -hub serve --http 0.0.0.0:8090
```

访问 `http://服务器IP:8090` 进入管理界面。在界面中添加系统时，Hub 会生成一个 SSH 公钥，复制该公钥到被监控服务器的 agent 启动命令中。

---

### 模式三：Agent 模式（`./beszel -agent`）

用于**被监控的服务器**，将数据上报给 Hub。

```bash
# 从 Hub 界面复制公钥后启动
./beszel -agent --key "ssh-ed25519 AAAA..." --listen :45876
```

或使用环境变量：

```bash
KEY="ssh-ed25519 AAAA..." ./beszel -agent --listen :45876
```

Agent 参数说明：

| 参数 | 说明 |
|------|------|
| `-k / --key` | Hub 的 SSH 公钥（从 Hub UI 复制） |
| `-l / --listen` | 监听地址，默认 `:45876` |
| `-u / --url` | Hub 的 URL（用于主动连接模式） |
| `-t / --token` | 认证 token |

---

## 典型部署场景

### 场景 A：一台服务器自监控

```bash
# 在该服务器上运行
./beszel serve --http 0.0.0.0:8090
```

### 场景 B：一个 Hub + 多台被监控服务器

```bash
# Hub 服务器
./beszel -hub serve --http 0.0.0.0:8090

# 每台被监控服务器（复制 Hub UI 中的公钥）
./beszel -agent --key "ssh-ed25519 AAAA..." --listen :45876
```

---

## 目录结构变更

新增文件：
- `internal/cmd/combined/main.go` — 统一入口

Makefile 新增构建目标：
- `make build-combined` — 编译单一二进制（含 Web UI）
- `make build-combined-dev` — 编译单一二进制（开发模式）
