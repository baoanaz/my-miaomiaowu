# 妙妙屋 fork 维护说明

本目录是对 [iluobei/miaomiaowu](https://github.com/iluobei/miaomiaowu) 的 fork，
只在官方基础上加了「邀请码自助注册」，其余功能保持与上游一致。

- 上游仓库：`upstream` → https://github.com/iluobei/miaomiaowu.git
- 自研分支：`feat/register`
- 基线版本：**已合并上游 v0.8.6**（`be11980`，2026-09-27；fork 起点为 v0.8.5 `a8c504c`）

## 为什么是 fork

官方没有注册功能，且**登录页入口必须改前端源码**才能加（前端产物 `//go:embed` 进二进制，
sidecar 反代改不了页面内容）。所以选择 fork，但把改动面压到最小。

## 改动清单

新增文件（不与上游冲突）：

| 文件 | 作用 |
| --- | --- |
| `internal/handler/register.go` | 注册接口 `/api/register`、状态 `/api/register/status` |
| `internal/handler/invite_codes.go` | 邀请码增删查、注册开关 |
| `internal/storage/invite_codes.go` | `invite_codes` 表 + 邀请码生成/消费 |
| `miaomiaowu/src/routes/register.tsx` | 注册页 |
| `miaomiaowu/src/components/invite-code-dialog.tsx` | 邀请码弹窗 |

修改上游文件（改动都很小，merge 时注意）：

| 文件 | 改动 |
| --- | --- |
| `cmd/server/main.go` | +4 行路由注册；`reservedFrontendRoutes` 加 `register`（关键，否则被当短链探测封 IP） |
| `internal/storage/traffic.go` | `migrate()` 末尾 +3 行调用 `migrateInviteCodes()` |
| `miaomiaowu/src/routes/login.tsx` | 登录页底部加注册入口（仅在开放注册时显示） |
| `miaomiaowu/src/routes/users.tsx` | 「新增用户」左侧加「创建邀请码」按钮 + 弹窗挂载 |
| `miaomiaowu/src/routes/subscription.index.tsx` | 删掉「转换客户端代理是从 substore 抄过来的…」警告提示；「复制」拆成主按钮 + 右侧下拉，主按钮默认复制 `t=auto`（`raw_output` 文件不带 `t`） |
| `internal/handler/subscription.go` | 见下方「上游 bug 修复」 |
| `internal/handler/subscribe_files.go` | 同上 |
| `internal/handler/update.go` | `getUpdateTargetPath` 开头直接拒绝自更新（防止覆盖 fork） |

## 上游 bug 修复（重要）

**问题**：上游假设全局只有一个管理员。`GetAdminUsername()` 用 `SELECT ... LIMIT 1`
取第一个管理员，节点全部存在该管理员名下；其他用户生成订阅时去借这个管理员的节点。

一旦支持注册多个管理员，新管理员会按**自己的用户名**取节点 → 拿到空列表 → 订阅是空的
（实测：新注册管理员订阅里 `proxies: []`）。

**修复**：抽出 `resolveNodeOwner()`，规则改为「自己名下有节点就用自己，否则回退首个管理员」。
这样三类用户都能拿到同一份节点池：首个管理员、新注册管理员、普通用户。

影响 3 处调用点：`subscription.go` 的 `generateFromTemplate` 与 `generateFromSelectedTags`，
`subscribe_files.go` 的 `regenerateFromTemplate`。

> 注：`/api/admin/nodes` 列表页仍只显示自己名下的节点（新管理员看到 0 个）——
> 这是上游列表页的既有行为，**不影响订阅生成**，未改动。

## 邀请码规则

- 格式：**首字母 + 5 位随机字符**，字母表去掉了易混的 `I/O/0/1`
- `A?????` → 注册出**管理员**；`B?????` → 注册出**普通用户**
- **一次性**：`used_by` 非空即失效。用条件更新（`WHERE used_by = ''`）保证并发下只被消费一次
- 注册开关默认**关闭**，需要在「用户管理 → 创建邀请码」里手动打开
- 普通用户注册后自动授权 `mmw-guest.yaml`（「普通用户配置」）

## 部署与构建

镜像在 **Docker 内构建**（本机不需要装 Go）：

```bash
cd /root/xuwenzheng/mmw-fork
docker build -t mmw-fork:register .
cd /root/xuwenzheng/vpn/mmw && docker compose up -d
```

前端单独调试（本机有 Node）：

```bash
cd /root/xuwenzheng/mmw-fork/miaomiaowu
npm ci                # 首次
npx tsc -b            # 类型检查
npm run build:only    # 只构建前端，产物到 ../internal/web/dist
```

## 跟进上游更新

```bash
cd /root/xuwenzheng/vpn/mmw-fork
git fetch upstream main
git log --oneline HEAD..upstream/main     # 先看有哪些新提交
git merge upstream/main                   # 冲突面很小，集中在上面「修改上游文件」那 7 个文件

docker tag mmw-fork:register mmw-fork:register-prev   # 留回滚镜像
docker build -t mmw-fork:register .
cd /root/xuwenzheng/vpn/mmw && docker compose up -d
docker compose ps                          # 确认 healthy
```

升级后要复验：面板 `https://154.12.34.214:8443/` 可达、注册页 `/register` 200、
订阅实时链接仍含 `ai.cviauto.cn: 10.50.11.36` 与 8 节点/99 规则。
回滚镜像：`docker tag mmw-fork:register-prev mmw-fork:register && docker compose up -d`。

**注意**：面板内的「系统更新」按钮已被**代码层禁用**（`getUpdateTargetPath` 直接返回错误）。
原因是它会去拉上游官方 release 并写入 `/app/data/server`，而 `docker-entrypoint.sh`
优先执行该文件，会静默把邀请码注册等功能抹掉。升级一律用上面的 `docker build` 流程。

## 回滚

```bash
cd /root/xuwenzheng/vpn/mmw
# 把 docker-compose.yml 的 image 换回 ghcr.io/iluobei/miaomiaowu:latest
docker compose up -d
```

数据库无需回滚：`invite_codes` 是新增表，官方镜像不会读它，留着不影响运行。

## 数据备份

```bash
bash /root/xuwenzheng/vpn/scripts/mmw_backup.sh
```

已接入 crontab（每日 03:15），产物在 `/root/xuwenzheng/vpn/backups/mmw/`，保留 30 份。

## 已知限制

- 注册接口复用登录的限流器（同 IP 短时间多次尝试会被拦），但没有独立的注册频率限制
- 邀请码没有有效期，只有「一次性」；如需过期机制要自行扩展
- 用户名限制为 `[A-Za-z0-9_-]{3,32}`，不支持中文（否则订阅 URL 会被编码）
- 邮箱为可选字段，未做验证
