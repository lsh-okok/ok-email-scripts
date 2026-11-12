# Ubuntu / Debian 一键部署与更新

支持 Linux 本机安装，以及 Windows 打包后通过 SSH 部署。两个入口共用 Docker 构建、停机备份和健康检查流程。

## Linux 全新独立安装（空数据库）

在 Ubuntu / Debian 服务器下载并执行：

```bash
curl -fL https://raw.githubusercontent.com/lsh-okok/ok-email-scripts/main/install-mail-lite.sh -o install-mail-lite.sh && sudo bash install-mail-lite.sh --fresh
```

如果已经上传当前源码，也可在仓库根目录执行 `sudo bash install-mail-lite.sh --fresh`。脚本优先使用旁边的 `mail-lite/` 源码，包含尚未提交的修改；旁边没有源码时，从本仓库 `main` 分支下载。

`--fresh` 创建一套完全独立的实例：

- 空数据库、独立 Docker 数据卷，以及新的管理员密码和加密密钥。
- 独立目录 `/opt/mail-lite-<日期>-<随机尾缀>` 和带相同后缀的 Compose 项目名。
- 从 8080 开始选择空闲 HTTP 端口；8080 已被旧服务占用时使用后面的空闲端口。
- 自动检测公网 IPv4，先通过 IP 管理页面使用；无需预先填写域名、代理或 MX 信息，MX 默认关闭。

现有服务、配置和数据继续保留。脚本自动安装缺失的 Docker / Compose 并构建新实例，服务器无需手动安装 Go 或 Node.js。安装结束会打印新管理员密码、实际管理 URL、实例目录及 `.env` 位置。请保存这些信息，并在云安全组放行输出中的实际 TCP 端口。

无人值守安装也会自动检测 IP 和选择端口：

```bash
sudo bash install-mail-lite.sh --fresh --yes
```

公网 IP 检测会排除 AWS `172.31.*`、`10.*` 等私网地址，并尝试备用 HTTPS 查询服务。无法检测时，交互运行会要求手动填写；无人值守运行可添加 `--host 服务器公网IP`。手动 `--host` 可覆盖检测结果。

### 指定新实例的目录、项目和端口

```bash
sudo bash install-mail-lite.sh --fresh --dir /opt/mail-lite-new --project mail-lite-new --port 8081
```

这些选项用于指定未被占用的新安装目标。发现已有配置、项目容器（包括已停止的容器）或数据卷时，脚本会拒绝复用，避免把旧实例当成空数据库安装。指定端口已占用时应选择另一个可用端口。`--fresh` 每次都表示创建另一个新实例，不能用它更新已有实例。

### 继续安装或更新同一实例

去掉 `--fresh`，并用安装结束打印的实际目录替换下面的示例：

```bash
sudo bash install-mail-lite.sh --dir /opt/mail-lite-20261112-120000-a1b2c3
```

脚本会从该目录的部署记录读取 Compose 项目名，沿用该实例的 `.env`、管理员密码、数据卷和端口，并在版本切换前备份。安装中途失败后若要继续同一实例，也应使用其已生成的目录运行上述命令。

原有未带 `--fresh` 的安装 / 更新行为保持：不指定 `--dir` 时仍使用 `/opt/mail-lite` 和默认项目 `mail-lite`。普通更新保留设置；显式传入 `--mode` / `--domain` 会更新网站访问配置，`--mx-host` 会更新 MX 配置。`--source` 可指定本地源码目录，`--repo` / `--ref` 可指定下载来源；完整参数见 `bash install-mail-lite.sh --help`。

### 后续配置域名和 MX

先确认新实例的 IP 管理地址可以访问。后续命令均需指定该实例的实际 `--dir`，例如使用 Caddy 自动 HTTPS：

```bash
sudo bash install-mail-lite.sh --dir /opt/mail-lite-20261112-120000-a1b2c3 --mode caddy --domain mail.example.com
```

请自行将域名解析到服务器，确保 TCP 80、443 可入站且端口空闲。接入现有 NPM 时使用：

```bash
sudo bash install-mail-lite.sh --dir /opt/mail-lite-20261112-120000-a1b2c3 --mode npm --domain mail.example.com --npm-network 实际网络名
```

再按下文配置 NPM 转发和 HTTPS 证书，并使用脚本打印的上游名称。每个实例的上游别名随 Compose 项目名变化。脚本显示的域名 URL 只表示已保存配置，本机健康检查不能证明公网 DNS、代理和证书已就绪。

启用 MX 收信时，针对相同实例运行：

```bash
sudo bash install-mail-lite.sh --dir /opt/mail-lite-20261112-120000-a1b2c3 --mx-host mx.example.com
```

该选项保留现有网站访问方式；另需配置 DNS A / MX 记录，并开放公网入站 TCP 25。同一主机上已有 MX 服务占用 25 时，需先自行安排收信服务，不能让两个实例同时绑定该端口。

已有代理实例要改回 IP 访问，也应带实际 `--dir` 并添加 `--mode direct`。脚本会备份原 `.env`、更新 HTTP 绑定和来源地址，同时保留该实例的管理员密码、数据、HTTP 端口及 MX 设置。

## 从 Windows 首次部署

Windows 入口直接打包当前工作区并通过 SSH 上传，包含尚未提交的源码，不依赖 GitHub 分支是否已推送。

1. 准备一台 Ubuntu / Debian 服务器，能够通过 SSH 登录。使用 `root`，或具有免密 `sudo` 权限的账号。本机需要 Windows OpenSSH 客户端和 `tar`（当前工作区所在电脑已具备）。
2. 如果使用域名，先把网站域名的 A 记录解析到服务器公网 IP，在云服务器安全组开放 TCP 80、443。没有域名时可先通过 `http://服务器IP:8080/admin/` 使用，对应开放 TCP 8080。
3. 双击仓库根目录的 **`deploy.bat`**，按提示填写服务器 IP、SSH 用户和端口、安装目录、Compose 项目名、代理模式、网站域名，以及是否启用 MX 收信。默认安装目录为 `/opt/mail-lite`，项目名为 `mail-lite`，代理模式为 `npm`（使用服务器现有的 Nginx Proxy Manager）。
4. 首次 SSH 连接需要确认主机指纹；使用密码登录时按 OpenSSH 提示输入服务器密码。配置 SSH 密钥后，后续更新无需重复输入服务器密码。

脚本会自动安装缺失的 Docker / Compose、创建随机管理员密码、构建镜像并启动服务。首次安装通过健康检查后，窗口末尾统一显示管理地址、安装目录、Compose 项目、配置位置及初始管理员密码，避免密码被构建日志刷走。已有实例更新继续使用当前密码，不重复打印可能已被网页修改的初始密码。

有现成 Nginx Proxy Manager 时使用默认 `npm` 模式，域名及 HTTPS 证书由 NPM 管理。没有现成代理时可选择 `caddy` 自动申请和续期 HTTPS 证书，需要空闲的 80、443 端口；选择 `direct` 则直接通过 HTTP 访问应用端口。现有 UFW 已启用时，脚本为 `caddy` / `direct` 添加对应端口规则；云平台安全组仍需在平台控制台配置。

服务器配置保存在 `/opt/mail-lite/.env`，权限为 `600`。初始密码也可以从这里找回；网页修改密码后以网页保存的新密码为准。构建中途失败时，重新双击部署入口即可重试，已生成的 `.env` 会保留。

本机连接配置保存在 `mail-lite/.deploy/server.json`，不保存 SSH 密码、不加入 Git、不打进部署包。需要换服务器或修改连接信息时，在仓库根目录执行：

```powershell
.\deploy.bat -Configure
```

## 已有 Nginx Proxy Manager（推荐）

此模式适用于 **NPM 和 Mail Lite 位于同一台 Docker 主机**，NPM 使用用户自定义 bridge 网络。Linux 使用上面的显式 `--mode npm` 命令配置；Windows 部署向导中填写：

- `Website proxy` 保持默认 `npm`。
- `Website domain` 填网站域名，例如 `mail.example.com`。
- `Existing NPM Docker network` 可以留空，脚本会识别正在运行的 NPM 容器及其网络；有多个 NPM 容器或多个网络时，请填写希望应用接入的现有网络名，例如 `nginx-proxy-manager_default`。

应用会接入 NPM 已有网络，默认提供固定网络别名 `mail-lite-app`，并保留仅本机可访问的 HTTP 8080 映射。网站的 80、443 和证书均继续由 NPM 管理。

在 NPM 的 **Hosts → Proxy Hosts → Add Proxy Host** 中创建一次：

| NPM 设置 | 填写内容 |
| --- | --- |
| Domain Names | `mail.example.com`，与部署填写的域名一致 |
| Scheme | `http` |
| Forward Hostname / IP | `mail-lite-app`（使用默认 Compose 项目名时） |
| Forward Port | `8080` |
| SSL Certificate | 选择已有证书，或申请 Let's Encrypt 证书 |
| Force SSL | 开启 |

保存后访问 `https://mail.example.com/admin/`。应用的 `APP_ORIGIN` 会自动设置为这个 HTTPS 根地址，用于来源校验和登录 Cookie。NPM 容器中的 `127.0.0.1` 指向 NPM 自身，转发地址应填写上面的网络别名。

以后双击 `update.bat` 即可，NPM 的 Proxy Host 保持原设置。自定义 Compose 项目名时，上游别名默认为 `<项目名>-app`（下划线转成连字符），部署完成窗口也会显示实际值。

查看 NPM 连接的网络，可以在服务器执行：

```bash
docker ps --format 'table {{.Names}}\t{{.Image}}'
docker inspect <NPM容器名> --format '{{range $name, $network := .NetworkSettings.Networks}}{{println $name}}{{end}}'
```

自动识别失败时，把正确网络名写入服务器 `/opt/mail-lite/.env` 的 `NPM_NETWORK`，再次双击部署或更新即可。也可手动使用 `compose.yml` 加 `compose.npm.yml`，并在 `.env` 设置 `NPM_NETWORK`、`NPM_UPSTREAM` 和 `APP_ORIGIN`。

## 日常更新

修改完代码后，双击仓库根目录的 **`update.bat`**。

脚本自动打包当前源码并上传，使用 Docker 缓存重新构建前后端；构建期间旧服务继续运行。新镜像准备好后，脚本短暂停止应用，对完整 `/data` 目录做一致性备份，然后替换容器并等待 `/health/ready` 通过。代理模式、NPM 网络和 MX 开关沿用服务器 `.env`。管理员密码、数据库和加密密钥继续使用原来的持久卷。

服务器有域名时，更新通常只中断容器切换和备份所需的时间；数据量越大，停机备份耗时越长。健康检查只验证应用及已启用的 SMTP 监听器，公网 DNS、HTTPS 证书签发和外部邮件投递还取决于服务器网络。

部署包按源码目录和构建文件白名单从当前磁盘生成，包含尚未提交的源码、文档及 `.env.example`；`requirements*.txt`、`CMakeLists.txt` 等标准构建文本也会保留。实际 `.env` / `.env.*` 配置、普通邮箱导入 TXT / CSV、私钥、数据库、已编译程序、`node_modules`、缓存和链接到其他位置的文件或目录均不会上传。新增构建文件若采用白名单以外的路径或格式，需同步更新 `install-mail-lite.sh` 和 `scripts/deploy.ps1` 的打包规则。首次部署默认创建服务器新实例；迁移本机已有数据需要单独停机迁移完整数据目录。

## MX 域名收信

MX 默认关闭。Linux 上按需运行 `sudo bash install-mail-lite.sh --mx-host mx.example.com`，Windows 部署向导中可选择启用并填写收信主机名。此外需要：

- 收信主机 A 记录指向服务器公网 IP，收信域名 MX 记录指向该主机名。
- 云服务器和网络提供商允许公网入站 TCP 25。
- 在管理端“邮箱 → 添加邮箱 → MX 域名收信”添加实际收信域名。

也可修改服务器 `/opt/mail-lite/.env` 中的 `SMTP_ENABLED=true` 和 `SMTP_HOSTNAME=mx.example.com`，然后重新运行安装或更新入口。网站域名和 MX 主机名可以不同。

## 已有手动 Docker 部署

填写原来的安装目录和 Compose 项目名，继续使用原 `.env` 和持久卷。例如，之前在 `/opt/mail-lite` 执行默认 Compose 命令，项目名通常为 `mail-lite`。项目名决定数据卷身份，需要与原部署一致。

原来手动使用 `compose.mx.yml` 时，请先确认服务器 `.env` 中有 `SMTP_ENABLED=true` 和正确的 `SMTP_HOSTNAME`。

采用已有 NPM 时，在服务器 `.env` 设置 `PROXY_MODE=npm`、`NPM_NETWORK=实际网络名`、`NPM_UPSTREAM=mail-lite-app`，保留 `HTTP_BIND=127.0.0.1`，将 `APP_ORIGIN` 设为外部 HTTPS 根地址。采用宿主机上已有 Nginx / 宝塔反向代理时，可设置 `PROXY_MODE=direct`、`HTTP_BIND=127.0.0.1`，保留原外部 HTTPS `APP_ORIGIN`。

普通更新会沿用服务器 `.env`。Windows 本机向导不会覆盖已有代理模式；Linux 显式传入 `--mode` / `--domain` 可以备份并更新该配置。早期脚本创建的 `.env` 没有 `PROXY_MODE` 时，普通更新继续沿用原模式。

## 备份与故障处理

每次切换前的备份保存在 `/opt/mail-lite/.deploy/backups/<版本>/`：

- `data.tar.gz`：已有数据卷的完整停机快照，包含 `mail-lite.db`、`mx-mail.db`、`data.key` 及可能存在的 WAL 文件。
- `env`：服务器配置副本。
- `previous-image`、`previous-release`：存在旧容器或脚本管理的旧版本时记录恢复参考信息。

源码版本保存在 `/opt/mail-lite/.deploy/releases/`，最近一次成功版本由 `/opt/mail-lite/.deploy/current` 指向。历史版本、镜像和备份保留，需根据磁盘容量定期清理确认不再需要的旧版本。

构建失败时旧容器继续运行；停机备份失败时脚本尝试重新启动旧容器。替换后的健康检查失败会明确返回失败、显示日志及备份位置，保留现场供修复，不会自动用旧数据库覆盖新收到的邮件。修复后再次双击更新即可重试。

查看服务日志：

```bash
docker logs --tail=100 "$(docker ps -aq --filter label=com.docker.compose.project=mail-lite --filter label=com.docker.compose.service=mail)"
```

## 本机检查与仅打包

```powershell
# 生成部署包，不连接服务器
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\mail-lite\scripts\deploy.ps1 -PackageOnly
```

打包结果位于 `mail-lite/.deploy/`。服务器构建需要能够访问 Docker 镜像仓库、npm 和 Go 模块源。

维护脚本时可运行下面的模拟检查，不连接服务器、不依赖 Docker 守护进程：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\mail-lite\scripts\tests\deploy-client-smoke.ps1
bash .\mail-lite\scripts\tests\deploy-smoke.sh
bash .\mail-lite\scripts\tests\install-smoke.sh
```
