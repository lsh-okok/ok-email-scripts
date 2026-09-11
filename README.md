# ok-email 一键部署脚本

在 Linux 服务器上快速部署 [ok-email](https://github.com/lsh-okok/ok-email) 邮件管理服务。

脚本自包含，不需要克隆项目源码：它会自动安装 Docker Engine 与 Docker Compose Plugin，生成 `docker-compose.yml` 与 `.env`，拉取镜像并启动服务。

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
```

## 支持的发行版

Ubuntu、Debian、CentOS、RHEL、Rocky Linux、AlmaLinux、Amazon Linux。需要 systemd。

## 镜像

同一套标签会同时发布到两个 registry：

- Docker Hub：`lsh-okok/ok-email:<tag>`（默认）
- GHCR：`ghcr.io/lsh-okok/ok-email:<tag>`

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
