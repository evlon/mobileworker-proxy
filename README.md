# mobilework-proxy

把本机**已登录的 MobileWork 桌面端**内部的「本地签名代理」包装成标准 **OpenAI 兼容 API**，
并把模型**一键安装进 DeepSeek Harness**（DSH）的 profile 配置。

**双击即用，零依赖，单个可执行文件。** Go 实现，目标机器无需安装任何运行时。

> 本程序**不持有任何凭据**，也不重算签名 —— 全部交给 MobileWork 自带的内部代理。

---

## 下载

从 [Releases](https://github.com/evlon/mobileworker-proxy/releases) 取对应平台的文件：

| 平台 | 文件 |
|---|---|
| Windows x64 | `mobilework-proxy-<版本>-windows-amd64.exe` |
| Windows ARM64 | `mobilework-proxy-<版本>-windows-arm64.exe` |
| macOS Intel | `mobilework-proxy-<版本>-darwin-amd64` |
| macOS Apple 芯片 | `mobilework-proxy-<版本>-darwin-arm64` |
| Linux x64 | `mobilework-proxy-<版本>-linux-amd64` |
| Linux ARM64 | `mobilework-proxy-<版本>-linux-arm64` |

macOS / Linux 下载后需要加执行权限：

```bash
chmod +x mobilework-proxy-<版本>-<平台>-<架构>
```

### ⚠️ 平台支持现状

**MobileWork 桌面端目前只有 Windows 版**，它才会生成 `~/.mobilework/` 下的模型清单与
内部签名代理端口。所以：

| 平台 | 状态 |
|---|---|
| **Windows** | ✅ 已完整验证（含真实控制台按键、真实模型对话、真实配置写入） |
| **Linux** | ⚠️ 程序能跑、按键能选、代理逻辑一致（已在 Ubuntu 24.04 上实测），但**本机没有 MobileWork 运行时就取不到模型清单**，会明确报错退出 |
| **macOS** | ⚠️ 同上，且**尚未在真机验证**（只做了交叉编译） |

也就是说：非 Windows 平台是**先把版本发出去**，等真有人用并反馈了再修。
如果你在 macOS/Linux 上跑，欢迎到 Issues 反馈。

---

## 快速开始（小白向）

1. 确认 **MobileWork 桌面端已启动并登录**（模型清单由它生成）。
2. 双击 `mobilework-proxy.exe`。
3. 屏幕上会列出检测到的模型，并有 **10 秒倒计时**。**按数字键选择**：

   ```
   检测到 3 个可用模型，请选择要安装到 DeepSeek Harness 的模型：

      [1]  ▸  auto               ctx 196608 · out 32768
      [2]     dsv4.1-flash       ctx 389120 · out 65536 · text+image
      [3]     moma/coding-auto   ctx 196608 · out 32768

      按数字直接安装 · A=全部安装 · 回车/超时 采用 ▸ 标记的项 · Q=跳过
      倒计时 10 秒
   ```

   | 按键 | 作用 |
   |---|---|
   | `1` `2` `3`… | **直接安装**第几项，不用再按回车 |
   | `A` | 安装全部模型 |
   | `Q` | 跳过安装，只起代理 |
   | 回车 | 采用 `▸` 标记的那一项 |
   | 什么都不按 | 倒计时结束，自动采用 `[1]`（推荐项） |

   > 模型数超过 9 个时，编号是两位数（如 `[12]`）：先按 `1` 再按 `2`，
   > 程序判断不可能再有第三位，就会立刻安装第 12 项。
   > 按错了可以按退格键删掉重输。

4. 接着选要装进哪个 DSH profile（desktop / web / …），同样用数字键。
5. 装完程序**不会退出**，它会继续把代理跑着 —— 保持这个窗口开着即可。
6. 打开 DeepSeek Harness，在模型列表里就能看到刚装进去的模型。

> ⚠️ 安装完成后**不要关闭这个窗口**。DSH 里的模型地址指向本程序
> （`http://localhost:8989/v1`），窗口一关模型就用不了了。

---

## 它做了什么

```
OpenAI 客户端 / DeepSeek Harness
   │  POST /v1/chat/completions   GET /v1/models
   ▼
┌──────────────────────────────┐  127.0.0.1:8989
│  mobilework-proxy.exe         │  ← 本程序
│  模型名 → providerId 映射     │
└──────────────────────────────┘
   │  POST /p/<providerId>/chat/completions
   ▼
┌──────────────────────────────┐  127.0.0.1:<动态端口>
│  MobileWork model-proxy       │  复用桌面端登录态，逐请求签名
└──────────────────────────────┘
   │  https://…（中移模型网关 / MoMA / 磐智 / 聚智）
   ▼
真实大模型
```

安装动作 = 往 `%USERPROFILE%\.dsh\profiles\<profile>\cordis.patch.yml` 里
`id: llm-pi-ai` 那条的 `config.providers` 下写入一个 `mobilework` 路由。

---

## 命令行参数

| 参数 | 默认 | 说明 |
|---|---|---|
| `--host` | `127.0.0.1` | 监听地址 |
| `--port` | `8989` | 监听端口（**与 DSH 里已配的 baseURL 一致**）|
| `--api-key` | 无 | 启用后客户端需带同样 key（也读环境变量 `MW_OPENAI_KEY`）|
| `--reload-interval` | `30` | 重读 `providers.jsonc` 的间隔秒数 |
| `--countdown` | `10` | 选择界面倒计时秒数；`0` = 不等待 |
| `--yes` | 关 | 跳过交互，直接装默认项（适合脚本）|
| `--install` | 空 | 装到指定 profile（`desktop` / `web` / `all`），给定则跳过 profile 选择 |
| `--no-install` | 关 | 只起代理，不改 DSH 配置 |
| `--version` | — | 打印版本后退出 |

### 常用组合

```powershell
# 完全无交互：装全部模型到所有 profile，然后常驻
.\mobilework-proxy.exe --yes --install all

# 只起代理，不动配置（调试用）
.\mobilework-proxy.exe --no-install

# 换端口（记得同步改 DSH 里 baseURL）
.\mobilework-proxy.exe --port 9000
```

---

## 端口被占用怎么办

如果提示端口 8989 被占用，多半是**本程序已经有一个实例在跑**（或另一个占用该端口的程序）：

```powershell
# 找出占用者
netstat -ano | findstr :8989

# 结束它（PID 换成上面查到的）
taskkill /PID <PID> /F
```

或者用 `--port` 换一个端口，但要记得把 DSH 配置里的 `baseURL` 一起改掉。

---

## 安全与可回滚

- **每次改动前自动备份**：在原文件旁边生成
  `cordis.patch.yml.bak-<时间戳>-mwproxy`。
- **幂等**：同一个模型装两次，第二次会识别出「配置已是目标状态」，不写文件、不产生多余备份。
- **只动该动的地方**：采用「文本外科手术」——只重写 `llm-pi-ai` 的 `providers` 段落，
  文件其余字节（包括别的工具维护的区块、注释、`!!js` 表达式）原样保留。
  这一点有测试用 LCS 差分严格验证。
- **不碰凭据**：`apiKeyEnv` 只写引用名（`MOBILEWORK_API_KEY`），key 值本身不落配置。

### 手动回滚

```powershell
# 列出备份
Get-ChildItem "$env:USERPROFILE\.dsh\profiles\desktop\cordis.patch.yml.bak-*"

# 恢复（替换成实际备份名）
Copy-Item "$env:USERPROFILE\.dsh\profiles\desktop\cordis.patch.yml.bak-20261121-124648-mwproxy" `
          "$env:USERPROFILE\.dsh\profiles\desktop\cordis.patch.yml" -Force
```

---

## 验证接口

```powershell
# 健康检查
curl.exe http://127.0.0.1:8989/health

# 模型列表
curl.exe http://127.0.0.1:8989/v1/models

# 非流式对话
curl.exe http://127.0.0.1:8989/v1/chat/completions `
  -H "Content-Type: application/json" `
  -d '{\"model\":\"auto\",\"messages\":[{\"role\":\"user\",\"content\":\"你好\"}]}'

# 流式
curl.exe -N http://127.0.0.1:8989/v1/chat/completions `
  -H "Content-Type: application/json" `
  -d '{\"model\":\"auto\",\"stream\":true,\"messages\":[{\"role\":\"user\",\"content\":\"数1到5\"}]}'
```

---

## 自己编译

```powershell
.\build.ps1                    # 产物在 dist\mobilework-proxy.exe
.\build.ps1 -Tidy              # 先跑 go mod tidy
.\build.ps1 -Version v0.1.0    # 指定版本号（默认取 git describe）
```

- 需要 Go 1.26+（本机在 `D:\go`，脚本会自动找）。
- 依赖 `gopkg.in/yaml.v3`、`golang.org/x/term`、`golang.org/x/sys`（纯 Go，无需 cgo）。
- 构建脚本内置 `GOPROXY=https://goproxy.cn`（公司网络下 `proxy.golang.org` 不通）。

交叉编译其它平台：

```bash
GOOS=linux  GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=v0.1.0" -o dist/mwp-linux-amd64 .
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=v0.1.0" -o dist/mwp-darwin-arm64 .
```

跑测试：

```powershell
go test ./... -v
```

测试包含针对**本机真实 `cordis.patch.yml`** 的只读校验与 LCS 差分验证
（全程在副本上操作，不会改动你的真实配置）。在没有 DSH profile 的机器上（如 CI），
这几项会自动跳过。

---

## 发布新版本

发布由 GitHub Actions 自动完成（`.github/workflows/release.yml`）：
**打 tag 并推送，就会构建六个平台并发布 Release**。

```bash
git tag v0.1.0
git push origin v0.1.0
```

- tag 以 `v` 开头即可；写成 `0.1.0` 也会自动补上 `v`。
- tag 里带连字符（如 `v0.2.0-rc1`）会自动发布为 **pre-release**。
- 发布前会先跑 `go vet` 和全部单元测试，测试不过不会发。
- Release 里会附带 `SHA256SUMS.txt` 便于校验。
- 也可以在 Actions 页面手动触发（`workflow_dispatch`），填一个已存在的 tag。

---

## 常见问题

**提示「读取 MobileWork 模型清单失败」**
MobileWork 桌面端没启动或没登录。打开并登录一次即可。

**DSH 里模型用不了 / 502**
本程序窗口关了。重新双击运行。

**DSH 里没看到刚装的模型**
DSH 需要重载配置。重启 DeepSeek Harness，或在它里面触发一次配置重载。

**模型名要用哪个**
用 `/v1/models` 返回的 `id`，即 `auto` / `dsv4.1-flash` 等，不是 `gpt-4`。
注意模型名**大小写敏感**（与上游一致）。

**按方向键没反应**
这是已知情况：经实测，Windows 传统控制台里方向键读不到字节，
所以本程序**以数字键为准**。方向键能收到时会移动 `▸` 标记，收不到不影响任何功能。
用数字键选即可。

---

## 免责声明

个人学习研究用途，非官方产品，与 MobileWork / 中国移动无任何关联。
请在你合法订阅的前提下使用，遵守相关服务条款，自负风险。
