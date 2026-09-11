# ok-email 一键部署脚本

在 Linux 服务器上快速部署 [ok-email](https://github.com/lsh-okok/ok-email) 邮件管理服务。

脚本自包含，不需要克隆项目源码：它会自动安装 Docker Engine 与 Docker Compose Plugin，生成 `docker-compose.yml` 与 `.env`，拉取镜像并启动服务。

## 脚本清单

| 脚本 | 用途 |
|---|---|
| [`install.sh`](install.sh) | 部署 ok-email 邮件管理服务（Docker） |
| [`setup_filebrowser.sh`](setup_filebrowser.sh) | 部署 FileBrowser 网页文件管理器（原生二进制 + systemd） |

## 一键安装

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/lsh-okok/ok-email-scripts/refs/heads/main/install.sh)
```

在希望部署的目录执行即可。root 用户直接运行；非 root 用户脚本会按需调用 `sudo`。

## 参数

| 参数 | 说明 | 默认值 |
|---|---|---|
| `--v VERSION` | 镜像标签 | `latest` |
| `--n NAME` | 容器名 | `outlook-mail-reader` |
| `--p PORT` | 宿主端口（容器内固定 5000） | `5000` |
| `--install-dir PATH` | 部署目录 | 当前目录 |
| `--registry dockerhub\|ghcr` | 镜像来源 | `dockerhub` |
| `--image REPO:TAG` | 完整镜像地址，覆盖上面两项 | — |
| `--registry-username USER` | 私有仓库用户名 | — |
| `--registry-password PASS` | 上一条对应的密码 / PAT，省略则交互式输入 | — |
| `--yes`, `-y` | 非交互：自动生成凭据并挑选空闲端口 | — |
| `--show-credentials` | 结束时打印密码与 SECRET_KEY | 默认不打印 |

示例：

```bash
# 部署指定版本到 5001 端口
bash install.sh --v 2.5.2 --p 5001

# 使用 GHCR 镜像
bash install.sh --registry ghcr

# 全自动，适合脚本调用
bash install.sh --install-dir /opt/ok-email --yes

# 拉取私有镜像（GHCR）：用户名 + 交互式输入 PAT
bash install.sh --registry ghcr --registry-username lsh-okok

# 完全非交互，配合环境变量传 PAT
OK_EMAIL_REGISTRY_PASSWORD=ghp_xxx \
  bash install.sh --registry ghcr --registry-username lsh-okok --yes
```

## 支持的发行版

Ubuntu、Debian、CentOS、RHEL、Rocky Linux、AlmaLinux、Amazon Linux。需要 systemd。

## 镜像

同一套标签会发布到两个 registry：

- Docker Hub：`lsh-okok/ok-email:<tag>`（默认）
- GHCR：`ghcr.io/lsh-okok/ok-email:<tag>`

默认按匿名拉取。未显式指定 `--registry` / `--image` 时，若其中一个 registry 拉取失败，脚本会自动换另一个再试一次。

### 私有镜像

镜像可能是私有的，此时需要登录后才能拉取：

```bash
# GHCR：用户名是 GitHub 用户名，密码填有 read:packages 权限的 PAT
bash install.sh --registry ghcr --registry-username <github-user>

# Docker Hub：密码填 Access Token（不是账号密码）
bash install.sh --registry-username <dockerhub-user>
```

也可以把 PAT 放到环境变量里避免出现在命令行历史：

```bash
export OK_EMAIL_REGISTRY_USERNAME=<user>
export OK_EMAIL_REGISTRY_PASSWORD=<pat>
bash install.sh --registry ghcr
```

## 生成的文件

```text
docker-compose.yml   # 覆盖前会自动备份为 docker-compose.yml.bak.<时间戳>
.env                 # 权限 600，保存 LOGIN_PASSWORD / SECRET_KEY / OUTLOOK_EMAIL_PORT
data/                # 应用数据
```

重复执行会复用已有的 `LOGIN_PASSWORD` 和 `SECRET_KEY`，不会因重装而更换密钥。升级保留 `.env` 与 `./data`，再次执行同一条命令即可。

## 凭据

安装完成后默认不在终端打印密码，读取方式：

```bash
sudo cat .env
```

首次登录后建议立即在 Web 界面修改密码。

## 常用运维命令

```bash
docker compose ps                            # 状态
docker compose logs -f --tail 100            # 日志
docker compose pull && docker compose up -d  # 升级
```

## 安全提示

容器会挂载 `/var/run/docker.sock`，用于 Web 界面的在线自更新。这等同于把宿主机的 root 权限授予该容器。如果不接受这一点，请不要使用本脚本，改为手动部署并去掉该挂载项。

---

# setup_filebrowser.sh

在 Linux 服务器上部署 [FileBrowser](https://github.com/filebrowser/filebrowser)（网页版文件管理器），用于在线浏览、上传、下载、编辑服务器文件。

无需 Docker：直接下载官方二进制，写入 `/etc/filebrowser` 配置，注册为 systemd 服务并设置开机自启。

## 一键安装

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/lsh-okok/ok-email-scripts/refs/heads/main/setup_filebrowser.sh)
```

## 参数

| 参数 | 说明 | 默认值 |
|---|---|---|
| `-p`, `--port PORT` | 监听端口 | `8080` |
| `-r`, `--root DIR` | 文件管理根目录 | `/home/ubuntu` |
| `-u`, `--user NAME` | 管理员用户名 | `admin` |
| `-P`, `--password PASS` | 管理员密码，省略则随机生成并打印 | 随机 14 位 |
| `--update` | 仅升级二进制并重启服务 | — |
| `--uninstall` | 卸载服务与二进制，保留数据库 | — |
| `--purge` | 卸载并删除配置与数据库 | — |

示例：

```bash
# 管理 /data 目录，端口 8081，指定管理员密码
sudo bash setup_filebrowser.sh -r /data -p 8081 -u admin -P 'MyPass123'

# 升级到最新版
sudo bash setup_filebrowser.sh --update

# GitHub 下载不通时使用加速前缀
GH_PROXY=https://ghfast.top/ sudo -E bash setup_filebrowser.sh
```

## 生成的文件

```text
/usr/local/bin/filebrowser              # 二进制
/etc/filebrowser/filebrowser.db          # 数据库（用户、配置，权限 600）
/etc/systemd/system/filebrowser.service  # systemd 单元
/var/log/filebrowser.log                 # 日志
```

## 常用运维命令

```bash
systemctl status filebrowser       # 状态
systemctl restart filebrowser      # 重启
journalctl -u filebrowser -f       # 日志
```

## 注意

- 云服务器（EC2、阿里云等）需要在安全组放行对应端口才能外网访问。
- 服务以 root 运行，因此可管理整台机器的文件；暴露公网前建议加 Nginx 反代 + HTTPS，并修改默认管理员密码。
