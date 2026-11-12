# Ubuntu / Debian 一键部署与更新

支持 Linux 本机安装，以及 Windows 打包后通过 SSH 部署。两个入口共用 Docker 构建、停机备份和健康检查流程。

## Linux 本机一键安装

将当前项目上传到 Ubuntu / Debian 服务器，在仓库根目录运行：

```bash
sudo bash install-mail-lite.sh
```

脚本优先使用旁边 `mail-lite/` 目录中的当前源码，包括尚未提交的修改。首次自动检测服务器公网 IPv4，默认安装为 IP 直连，提供 `http://服务器IP:8080/admin/`；无需先填写域名、代理模式或 MX 信息，MX 默认关闭。默认安装目录为 `/opt/mail-lite`，Compose 项目名为 `mail-lite`。无需手动安装 Go 或 Node.js；缺失的 Docker / Compose 会自动安装。首次生成的管理员密码会显示在终端，配置保存在 `/opt/mail-lite/.env`。

无人值守安装也会自动检测公网 IP：

```bash
sudo bash install-mail-lite.sh --yes
```

自动检测会排除 AWS `172.31.*`、`10.*` 等私网地址，并在外部 HTTPS IP 服务超时或失败时尝试备用服务。无法检测时，交互运行会要求手动填写；无人值守运行可添加 `--host 服务器公网IP`。手动 `--host` 也可覆盖检测结果。

### 将已有代理部署切换为 IP 访问

普通更新会保留已有网站配置。已经配置 NPM / HTTPS 的实例，需要重新下载新版脚本并显式切换：

```bash
curl -fL https://raw.githubusercontent.com/lsh-okok/ok-email-scripts/main/install-mail-lite.sh -o install-mail-lite.sh
sudo bash install-mail-lite.sh --mode direct
```

脚本会自动检测公网 IP，备份原 `.env`，将 `HTTP_BIND` 改为 `0.0.0.0`，将 `APP_ORIGIN` 改为对应的 HTTP IP 地址，并清除网站域名配置。管理员密码、数据、Compose 项目名、已有 HTTP 端口和 MX 设置继续保留。默认端口为 8080，需在云安全组放行对应 TCP 端口。

### 后续配置域名和 MX

先确认 IP 地址可访问管理页面，再自行配置域名。使用 Caddy 自动 HTTPS：

```bash
sudo bash install-mail-lite.sh --yes --mode caddy --domain mail.example.com
```

请自行将域名解析到服务器，确保 TCP 80、443 可入站且端口空闲。接入现有 NPM 时运行：

```bash
sudo bash install-mail-lite.sh --mode npm --domain mail.example.com --npm-network 实际网络名
```

再按下文配置 NPM 转发和 HTTPS 证书。脚本显示的域名 URL 只表示已保存配置，应用的本机健康检查不代表公网 DNS、代理和证书已就绪。

需要 MX 收信时运行 `sudo bash install-mail-lite.sh --mx-host mx.example.com`，该选项保留现有网站访问方式；另外配置 DNS A / MX 记录和公网入站 TCP 25。

可用 `--dir` 指定安装目录、`--project` 指定 Compose 项目名、`--source` 指定源码目录；完整参数见 `bash install-mail-lite.sh --help`。普通更新沿用已有服务器 `.env` 和数据卷；显式传入 `--mode` / `--domain` 会更新网站配置，`--mx-host` 会更新 MX 配置。修改配置前会保存原 `.env` 备份，新镜像构建成功后才短暂停机备份数据并替换容器，再检查应用是否运行。

单独复制 `install-mail-lite.sh`、旁边没有源码时，脚本从 GitHub `lsh-okok/ok-email-scripts` 的 `main` 分支下载源码，可用 `--repo`、`--ref` 修改仓库和版本。该仓库的下载入口名为 `install-mail-lite.sh`，用于与原有的旧版 `install.sh` 区分；尚未推送的本地修改需上传整个项目后安装。

## 从 Windows 首次部署

Windows 入口直接打包当前工作区并通过 SSH 上传，包含尚未提交的源码，不依赖 GitHub 分支是否已推送。

1. 准备一台 Ubuntu / Debian 服务器，能够通过 SSH 登录。使用 `root`，或具有免密 `sudo` 权限的账号。本机需要 Windows OpenSSH 客户端和 `tar`（当前工作区所在电脑已具备）。
2. 如果使用域名，先把网站域名的 A 记录解析到服务器公网 IP，在云服务器安全组开放 TCP 80、443。没有域名时可先通过 `http://服务器IP:8080/admin/` 使用，对应开放 TCP 8080。
3. 双击仓库根目录的 **`deploy.bat`**，按提示填写服务器 IP、SSH 用户和端口、安装目录、Compose 项目名、代理模式、网站域名，以及是否启用 MX 收信。默认安装目录为 `/opt/mail-lite`，项目名为 `mail-lite`，代理模式为 `npm`（使用服务器现有的 Nginx Proxy Manager）。
4. 首次 SSH 连接需要确认主机指纹；使用密码登录时按 OpenSSH 提示输入服务器密码。配置 SSH 密钥后，后续更新无需重复输入服务器密码。

脚本会自动安装缺失的 Docker / Compose、创建随机管理员密码、构建镜像并启动服务。首次生成配置时窗口显示初始管理员密码，服务就绪后显示管理地址和代理设置。

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
