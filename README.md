# Company Gateway

Company Gateway 是基于 [Sub2API](https://github.com/Wei-Shaw/sub2api) 开发的公司内部 AI API 网关源码。

## 当前能力

- 用户、管理员和 Super Administrator 权限体系
- 上游账号、模型目录和 API Key 管理
- 请求审计、受控原文披露和使用量关联
- Claude Code、Codex、OpenCode 等客户端协议支持
- Work Session 会话识别
- economy、general、advanced 三档 Auto 路由
- 独立 Internal Inference 分类服务接入

## 构建环境

- Go 1.26
- Node.js 24
- pnpm 9
- PostgreSQL 18
- Redis 8
- Docker Engine 24 或更新版本

安装前端依赖：

```bash
cd frontend
corepack enable
corepack prepare pnpm@9 --activate
pnpm install --frozen-lockfile
```

构建前端：

```bash
cd frontend
pnpm build
```

运行后端测试：

```bash
cd backend
go test ./...
```

构建 `linux/amd64` Docker 镜像：

```bash
docker buildx build \
  --platform linux/amd64 \
  --tag company/sub2api:latest \
  --load \
  .
```

## 安全要求

- 不要提交 `.env`、Provider Key、管理员密码或任何真实凭据。
- 不要提交 PostgreSQL、Redis、`data/`、日志或生产导出数据。
- 所有上游地址必须经过 URL Allowlist 限制。
- Audit Content Key 和 Work Session HMAC Key 必须使用不同密钥。

## 上游项目与许可

本仓库保留 Sub2API 的原始许可文件。详细条款见 [LICENSE](LICENSE)。
