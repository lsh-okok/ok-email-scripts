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
curl -fsSL -o setup_filebrowser.sh \
  https://raw.githubusercontent.com/lsh-okok/ok-email-scripts/refs/heads/main/setup_filebrowser.sh
sudo bash setup_filebrowser.sh
```

也可以直接管道执行（脚本中途报错时终端输出可能不完整，排错时建议用上面的方式）：

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/lsh-okok/ok-email-scripts/refs/heads/main/setup_filebrowser.sh)
```

## 参数

| 参数 | 说明 | 默认值 |
|---|---|---|
| `-p`, `--port PORT` | 监听端口 | `8080` |
| `-r`, `--root DIR` | 文件管理根目录 | `/home/ubuntu` |
| `-u`, `--user NAME` | 管理员用户名 | `admin` |
| `-P`, `--password PASS` | 管理员密码，省略则随机生成并打印；**至少 12 位**，不足会被忽略并改为随机 | 随机 16 位 |
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

# install-dujiao-next.sh

Dujiao-Next 商城一键部署脚本（Docker Compose），依据官方文档 https://dujiao-next.com/deploy/docker-compose 编写。

- 支持 SQLite + Redis（轻量）/ PostgreSQL + Redis（生产）两种方案
- 自动生成三个彼此不同的强随机密钥，以及 Redis / PostgreSQL 随机密码
- 未安装 Docker 时自动通过阿里云镜像安装（含 Compose 插件），并校验守护进程
- 支持交互式向导与全自动非交互两种模式
- 可选生成外层 Nginx 反向代理配置

## 一键安装

```bash
# 交互式向导
curl -fsSL https://raw.githubusercontent.com/lsh-okok/ok-email-scripts/main/install-dujiao-next.sh -o install-dujiao-next.sh && chmod +x install-dujiao-next.sh && sudo bash install-dujiao-next.sh

# 全自动（全部默认值）
curl -fsSL https://raw.githubusercontent.com/lsh-okok/ok-email-scripts/main/install-dujiao-next.sh -o install-dujiao-next.sh && chmod +x install-dujiao-next.sh && sudo DJ_NONINTERACTIVE=1 bash install-dujiao-next.sh
```

## 参数

非交互模式通过环境变量设置（`DJ_NONINTERACTIVE=1` 时生效，交互式向导为同名问答项）：

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `DJ_INSTALL_DIR` | `/opt/dujiao-next` | 部署目录 |
| `DJ_DB_DRIVER` | `sqlite` | `sqlite`（轻量）/ `postgres`（生产） |
| `DJ_TAG` | `latest` | 镜像版本，如 `v1.4.7` |
| `DJ_APP_PORT` | `8080` | 应用对外端口（绑定 127.0.0.1） |
| `DJ_ADMIN_PATH` | `dj-mgmt-随机` | 后台入口路径（建议改掉默认 `/admin`） |
| `DJ_ADMIN_USER` | `admin` | 后台管理员用户名 |
| `DJ_ADMIN_PASS` | 自动生成 | 管理员密码，需含大写+小写+数字 |
| `DJ_DOMAIN` | 空 | 填域名则额外生成 Nginx 反代配置 |
| `DJ_COMPOSE_VERSION` | `v2.29.1` | 仅兜底补装 Compose 插件时用 |
| `DJ_DOCKER_MIRROR` | 空 | 设为 `Aliyun` 时用阿里云镜像装 Docker（中国大陆建议）；留空用官方源（海外建议） |
| `DJ_BIND` | `127.0.0.1` | 应用端口绑定地址：`127.0.0.1` 仅本机回环（官方安全建议，需配 Nginx 反代）；`0.0.0.0` 或留空则监听所有接口（可直接用公网/局域网 IP 访问） |

示例：

```bash
# 生产方案 + 自定义端口 + 指定管理员
sudo DJ_NONINTERACTIVE=1 \
  DJ_DB_DRIVER=postgres \
  DJ_APP_PORT=9000 \
  DJ_ADMIN_USER=boss \
  DJ_ADMIN_PASS='Ab3xYz9kQw2' \
  DJ_ADMIN_PATH='console-8k2m' \
  bash install-dujiao-next.sh
```

## 生成的文件

```
/opt/dujiao-next/
├── .env                    # 环境变量（含管理员账号、随机密码，权限 600）
├── docker-compose.yml      # Compose 配置（按所选方案生成）
├── config/
│   └── config.yml          # 应用配置（密钥、数据库、Redis 等）
└── data/
    ├── db/                 # SQLite 数据（SQLite 方案）
    ├── postgres/           # PostgreSQL 数据（PostgreSQL 方案）
    ├── redis/              # Redis 数据
    ├── uploads/            # 上传文件
    └── logs/               # 日志
```

## 常用运维命令

```bash
cd /opt/dujiao-next
docker compose --env-file .env -f docker-compose.yml ps                      # 查看状态
docker compose --env-file .env -f docker-compose.yml logs -f dujiao-next     # 看日志
docker compose --env-file .env -f docker-compose.yml restart                 # 重启
docker compose --env-file .env -f docker-compose.yml down                    # 停止

# 升级：改 .env 里的 TAG 后执行
docker compose --env-file .env -f docker-compose.yml pull
docker compose --env-file .env -f docker-compose.yml up -d
```

## 安全提示

- 登录后台后请立即修改管理员密码。
- `app.secret_key` 必须与数据库一起备份，丢失将无法解密敏感数据。
- 应用端口仅绑定 `127.0.0.1`，请通过 Nginx 反代对外提供服务。
- 后台入口路径已随机化，建议不要改回 `/admin`。
